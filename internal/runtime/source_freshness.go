package runtime

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/presets"
)

const (
	maxFreshnessFiles        = 4096
	maxFreshnessDirectories  = 4096
	maxFreshnessDirEntries   = 4096
	maxFreshnessFileBytes    = 8 << 20
	maxFreshnessTotalBytes   = 64 << 20
	maxFreshnessIncludes     = 256
	maxFreshnessPatternBytes = 1024
	maxFreshnessRecipeBytes  = maxFreshnessIncludes * maxFreshnessPatternBytes
	freshnessCopyBufferBytes = 32 << 10
)

var withFreshnessFileSnapshot = boundedio.WithRegularFileSnapshot

type sourceFreshnessInclude struct {
	pattern string
	base    string
}

type sourceFreshnessRecipe struct {
	root     string
	includes []sourceFreshnessInclude
}

type freshnessDiscovery struct {
	RepoRoot         string
	Discovered       bool
	ClaudePath       string
	AgentsPath       string
	StartMDPath      string
	ConfigPath       string
	ConfigCandidates []string
	PolicyPaths      []string
}

type freshnessFile struct {
	Path     string
	Exists   bool
	Mode     uint32
	Size     int64
	ModTime  int64
	Identity string
	Digest   [sha256.Size]byte
}

type sourceFreshnessSeed struct {
	rootIdentity string
	rootInfo     os.FileInfo
	files        map[string]sourceFreshnessSeedFile
	runtimes     int
}

type sourceFreshnessSeedFile struct {
	digest    [sha256.Size]byte
	hasDigest bool
	expected  bool
	policy    bool
	runtime   bool
}

type sourceFreshnessStats struct {
	bytesRead int64
}

type sourceFreshnessHasher struct {
	hash    hash.Hash
	err     error
	number  [8]byte
	strings [256]byte
}

func observeRuntimeSourceFreshness(root string, plan *runtimePlan) ([sha256.Size]byte, error) {
	return observeRuntimeSourceFreshnessWithStats(root, plan, nil)
}

func observeRuntimeSourceFreshnessWithStats(root string, plan *runtimePlan, stats *sourceFreshnessStats) ([sha256.Size]byte, error) {
	if plan == nil {
		return [sha256.Size]byte{}, errors.New("runtime source freshness requires a plan")
	}
	discovery, err := ingest.DiscoverPolicyRepo(root)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return observeSourceFreshness(root, plan.sources, discovery, plan.sourceFreshness, nil, stats)
}

func observeRuntimeSourceFreshnessFromBundleWithStats(
	root string,
	plan *runtimePlan,
	bundle *ingest.SourceBundle,
	stats *sourceFreshnessStats,
) ([sha256.Size]byte, error) {
	if plan == nil || bundle == nil {
		return [sha256.Size]byte{}, errors.New("runtime source freshness requires a plan and bundle")
	}
	seed, err := newSourceFreshnessSeed(root, bundle)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return observeSourceFreshness(root, plan.sources, bundle.Discovery, plan.sourceFreshness, &seed, stats)
}

func newSourceFreshnessRecipe(root string, patterns []string) (sourceFreshnessRecipe, error) {
	if len(patterns) == 0 || len(patterns) > maxFreshnessIncludes {
		return sourceFreshnessRecipe{}, fmt.Errorf("runtime freshness recipe requires 1-%d include patterns", maxFreshnessIncludes)
	}
	recipe := sourceFreshnessRecipe{root: filepath.Clean(root), includes: make([]sourceFreshnessInclude, 0, len(patterns))}
	totalBytes := 0
	previous := ""
	for _, pattern := range patterns {
		if pattern == "" || len(pattern) > maxFreshnessPatternBytes || (previous != "" && pattern <= previous) {
			return sourceFreshnessRecipe{}, errors.New("runtime freshness recipe include patterns must be bounded, sorted, and unique")
		}
		totalBytes += len(pattern)
		if totalBytes > maxFreshnessRecipeBytes {
			return sourceFreshnessRecipe{}, fmt.Errorf("runtime freshness recipe exceeds %d bytes", maxFreshnessRecipeBytes)
		}
		base, err := freshnessGlobBase(root, pattern)
		if err != nil {
			return sourceFreshnessRecipe{}, err
		}
		recipe.includes = append(recipe.includes, sourceFreshnessInclude{pattern: pattern, base: base})
		previous = pattern
	}
	return recipe, nil
}

func observeSourceFreshness(
	root string,
	sources []runtimeSource,
	discovery ingest.DiscoveryResult,
	recipe sourceFreshnessRecipe,
	seed *sourceFreshnessSeed,
	stats *sourceFreshnessStats,
) ([sha256.Size]byte, error) {
	if filepath.Clean(root) != recipe.root || len(recipe.includes) == 0 {
		return [sha256.Size]byte{}, errors.New("runtime source freshness recipe does not match repository root")
	}
	if seed != nil && seed.rootIdentity != "" {
		currentRoot, err := pathidentity.ResolveExisting(root)
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		if currentRoot != seed.rootIdentity {
			return [sha256.Size]byte{}, errors.New("runtime freshness repository root changed while preparing the runtime plan")
		}
		currentInfo, err := os.Stat(root)
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		if seed.rootInfo == nil || !currentInfo.IsDir() || !os.SameFile(seed.rootInfo, currentInfo) {
			return [sha256.Size]byte{}, errors.New("runtime freshness repository root changed while preparing the runtime plan")
		}
	}
	files := map[string]struct{}{}
	directories := map[string]struct{}{}
	virtualPresets := map[string]struct{}{}
	includePatterns := []string{}
	reconcHome, err := presets.ResolveHome()
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	addDirectory(directories, filepath.Join(root, "policies"))
	addDirectory(directories, filepath.Join(root, ".reconc", "runtimes"))
	addDirectory(directories, filepath.Join(reconcHome, "presets"))
	addFile(files, filepath.Join(reconcHome, ingest.GlobalPolicyFilename))
	for _, marker := range []string{"CLAUDE.md", "AGENTS.md", "start.md", ".reconc.yml", ".reconc.yaml"} {
		addFile(files, filepath.Join(root, marker))
	}
	for _, source := range sources {
		physical, virtual, err := freshnessSourcePath(root, source)
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		if virtual != "" {
			virtualPresets[virtual] = struct{}{}
			continue
		}
		addFile(files, physical)
		if source.Kind != policy.SourceGlobal {
			addSourceParentDirectory(directories, root, filepath.Dir(physical))
		}
	}
	for _, rel := range discovery.PolicyPaths {
		addFile(files, filepath.Join(root, filepath.FromSlash(rel)))
	}
	for _, rel := range discovery.ConfigCandidates {
		addFile(files, filepath.Join(root, filepath.FromSlash(rel)))
	}
	for _, include := range recipe.includes {
		includePatterns = append(includePatterns, include.pattern)
		if filepath.Clean(include.base) != filepath.Clean(root) {
			addDirectory(directories, include.base)
		}
		matches, err := ingest.ExpandPolicyIncludePattern(root, include.pattern)
		if err != nil {
			return [sha256.Size]byte{}, fmt.Errorf("expand freshness pattern %s: %w", include.pattern, err)
		}
		for _, match := range matches {
			if seed != nil {
				if expected := seed.files[filepath.Clean(match)]; !expected.policy {
					return [sha256.Size]byte{}, fmt.Errorf("runtime freshness policy source set changed while preparing the runtime plan")
				}
			}
			addFile(files, match)
		}
	}
	if seed != nil {
		for sourcePath, expected := range seed.files {
			if !expected.policy {
				continue
			}
			if _, present := files[sourcePath]; !present {
				return [sha256.Size]byte{}, fmt.Errorf("runtime freshness policy source set changed while preparing the runtime plan")
			}
		}
		if err := validateFreshnessRuntimeSet(root, seed); err != nil {
			return [sha256.Size]byte{}, err
		}
	}
	if len(files) > maxFreshnessFiles {
		return [sha256.Size]byte{}, fmt.Errorf("runtime freshness file set exceeds %d entries", maxFreshnessFiles)
	}
	if len(directories) > maxFreshnessDirectories {
		return [sha256.Size]byte{}, fmt.Errorf("runtime freshness directory set exceeds %d entries", maxFreshnessDirectories)
	}
	identity := newSourceFreshnessHasher()
	writeFreshnessDiscovery(identity, normalizeFreshnessDiscovery(discovery))
	writeFreshnessSources(identity, sources)
	writeFreshnessStrings(identity, "includes", includePatterns)
	writeFreshnessStrings(identity, "virtual-presets", sortedKeys(virtualPresets))
	if err := observeFreshnessFiles(identity, files, seed, stats); err != nil {
		return [sha256.Size]byte{}, err
	}
	if err := observeFreshnessDirectories(identity, directories); err != nil {
		return [sha256.Size]byte{}, err
	}
	return identity.sum()
}

func freshnessSourcePath(root string, source runtimeSource) (string, string, error) {
	if source.Kind == policy.SourceGlobal {
		home, err := presets.ResolveHome()
		if err != nil {
			return "", "", err
		}
		return filepath.Join(home, ingest.GlobalPolicyFilename), "", nil
	}
	if source.Kind == policy.SourcePreset || strings.HasPrefix(source.Path, "preset:") {
		name := strings.TrimPrefix(source.Path, "preset:")
		presetPath, presetSource, err := presets.Path(name)
		if err != nil {
			return "", "", err
		}
		if presetSource == presets.SourceBundled {
			return "", presetPath, nil
		}
		return presetPath, "", nil
	}
	if source.Path == "" || filepath.IsAbs(source.Path) || path.IsAbs(filepath.ToSlash(source.Path)) {
		return "", "", fmt.Errorf("runtime freshness source path is not repository-relative: %q", source.Path)
	}
	cleaned := filepath.Clean(filepath.FromSlash(source.Path))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("runtime freshness source path escapes repository: %q", source.Path)
	}
	return filepath.Join(root, cleaned), "", nil
}

func freshnessGlobBase(root, pattern string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(pattern))
	if cleaned == "." || filepath.IsAbs(cleaned) {
		return "", errors.New("freshness glob base must be repository-relative")
	}
	parts := strings.Split(cleaned, string(filepath.Separator))
	prefix := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.ContainsAny(part, "*?[") {
			break
		}
		prefix = append(prefix, part)
	}
	if len(prefix) == len(parts) {
		prefix = prefix[:len(prefix)-1]
	}
	if len(prefix) == 0 {
		return root, nil
	}
	return filepath.Join(append([]string{root}, prefix...)...), nil
}

func newSourceFreshnessSeed(root string, bundle *ingest.SourceBundle) (sourceFreshnessSeed, error) {
	seed := sourceFreshnessSeed{
		rootIdentity: bundle.RootIdentity(),
		rootInfo:     bundle.RootInfo(),
		files:        make(map[string]sourceFreshnessSeedFile, len(bundle.Sources)+6),
	}
	reconcHome, err := presets.ResolveHome()
	if err != nil {
		return sourceFreshnessSeed{}, err
	}
	seed.files[filepath.Clean(filepath.Join(reconcHome, ingest.GlobalPolicyFilename))] = sourceFreshnessSeedFile{}
	for _, marker := range []string{"CLAUDE.md", "AGENTS.md", "start.md", ".reconc.yml", ".reconc.yaml"} {
		seed.files[filepath.Clean(filepath.Join(root, marker))] = sourceFreshnessSeedFile{}
	}
	for _, source := range bundle.Sources {
		physical, _, err := freshnessSourcePath(root, runtimeSource{Kind: source.Kind, Path: source.Path})
		if err != nil {
			return sourceFreshnessSeed{}, err
		}
		if physical == "" {
			continue
		}
		physical = filepath.Clean(physical)
		record := seed.files[physical]
		record.expected = true
		if !record.hasDigest {
			record.digest = sha256.Sum256([]byte(source.Content))
			record.hasDigest = true
		}
		switch source.Kind {
		case policy.SourcePolicyFile:
			record.policy = true
		case policy.SourceCustomRuntime:
			if !record.runtime {
				seed.runtimes++
			}
			record.runtime = true
		}
		seed.files[physical] = record
	}
	for _, candidate := range bundle.Discovery.ConfigCandidates {
		candidatePath := filepath.Clean(filepath.Join(root, filepath.FromSlash(candidate)))
		record := seed.files[candidatePath]
		record.expected = true
		seed.files[candidatePath] = record
	}
	return seed, nil
}

func validateFreshnessRuntimeSet(root string, seed *sourceFreshnessSeed) error {
	directory := filepath.Join(root, ".reconc", "runtimes")
	entries, err := boundedio.ReadDirNoSymlink(directory, maxFreshnessDirEntries)
	if errors.Is(err, os.ErrNotExist) {
		if seed.runtimes == 0 {
			return nil
		}
		return errors.New("runtime freshness custom-runtime source set changed while preparing the runtime plan")
	}
	if err != nil {
		return err
	}
	actual := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			actual[filepath.Clean(filepath.Join(directory, entry.Name()))] = struct{}{}
		}
	}
	if len(actual) != seed.runtimes {
		return errors.New("runtime freshness custom-runtime source set changed while preparing the runtime plan")
	}
	for sourcePath, expected := range seed.files {
		if !expected.runtime {
			continue
		}
		if _, present := actual[sourcePath]; !present {
			return errors.New("runtime freshness custom-runtime source set changed while preparing the runtime plan")
		}
	}
	return nil
}

func observeFreshnessFiles(
	identity *sourceFreshnessHasher,
	paths map[string]struct{},
	seed *sourceFreshnessSeed,
	stats *sourceFreshnessStats,
) error {
	ordered := sortedKeys(paths)
	identity.writeString("files")
	identity.writeUint64(uint64(len(ordered)))
	var totalBytes int64
	copyBuffer := make([]byte, freshnessCopyBufferBytes)
	for _, filePath := range ordered {
		var digest [sha256.Size]byte
		hasDigest := false
		expected := false
		hasExpectation := false
		if seed != nil {
			if value, present := seed.files[filePath]; present {
				digest = value.digest
				hasDigest = value.hasDigest
				expected = value.expected
				hasExpectation = true
			}
		}
		observation, err := observeFreshnessFileSeeded(
			filePath, &totalBytes, copyBuffer, digest, hasDigest, expected, hasExpectation, stats,
		)
		if err != nil {
			return err
		}
		writeFreshnessFile(identity, observation)
	}
	return nil
}

func observeFreshnessFile(path string, totalBytes *int64, copyBuffer []byte) (freshnessFile, error) {
	return observeFreshnessFileSeeded(path, totalBytes, copyBuffer, [sha256.Size]byte{}, false, false, false, nil)
}

func observeFreshnessFileSeeded(
	path string,
	totalBytes *int64,
	copyBuffer []byte,
	knownDigest [sha256.Size]byte,
	hasKnownDigest bool,
	expected bool,
	hasExpectation bool,
	stats *sourceFreshnessStats,
) (freshnessFile, error) {
	if len(copyBuffer) == 0 && !hasKnownDigest {
		return freshnessFile{}, errors.New("runtime freshness copy buffer is empty")
	}
	observation := freshnessFile{Path: path}
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if hasExpectation && expected {
			return observation, fmt.Errorf("runtime freshness source disappeared while preparing the runtime plan: %s", path)
		}
		return observation, nil
	}
	if err != nil {
		return observation, err
	}
	if hasExpectation && !expected {
		return observation, fmt.Errorf("runtime freshness source appeared while preparing the runtime plan: %s", path)
	}
	var contentHash hash.Hash
	if !hasKnownDigest {
		contentHash = sha256.New()
	}
	var readBytes int64
	var openedInfo os.FileInfo
	var openedIdentity string
	err = withFreshnessFileSnapshot(path, maxFreshnessFileBytes, func(file *os.File, opened os.FileInfo) error {
		if *totalBytes > maxFreshnessTotalBytes-opened.Size() {
			return fmt.Errorf("runtime freshness files exceed bounded byte budget")
		}
		openedInfo = opened
		if !hasKnownDigest {
			var copyErr error
			readBytes, copyErr = io.CopyBuffer(contentHash, io.LimitReader(file, maxFreshnessFileBytes+1), copyBuffer)
			if copyErr != nil {
				return copyErr
			}
			if readBytes != opened.Size() {
				return fmt.Errorf("runtime freshness source changed while reading: %s", path)
			}
		}
		var identityErr error
		openedIdentity, identityErr = freshnessFileIdentity(file, opened)
		return identityErr
	})
	if err != nil {
		return observation, err
	}
	*totalBytes += openedInfo.Size()
	observation.Exists = true
	observation.Mode = uint32(openedInfo.Mode())
	observation.Size = openedInfo.Size()
	observation.ModTime = openedInfo.ModTime().UnixNano()
	observation.Identity = openedIdentity
	if hasKnownDigest {
		observation.Digest = knownDigest
	} else {
		contentHash.Sum(observation.Digest[:0])
		if stats != nil {
			stats.bytesRead += readBytes
		}
	}
	return observation, nil
}

func observeFreshnessDirectories(identity *sourceFreshnessHasher, paths map[string]struct{}) error {
	ordered := sortedKeys(paths)
	identity.writeString("directories")
	identity.writeUint64(uint64(len(ordered)))
	for _, directoryPath := range ordered {
		identity.writeString(directoryPath)
		info, err := os.Lstat(directoryPath)
		if errors.Is(err, os.ErrNotExist) {
			identity.writeBool(false)
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("runtime freshness directory must be non-symlink: %s", directoryPath)
		}
		entries, err := boundedio.ReadDirNoSymlink(directoryPath, maxFreshnessDirEntries)
		if err != nil {
			return err
		}
		identity.writeBool(true)
		identity.writeUint64(uint64(info.Mode()))
		identity.writeInt64(info.ModTime().UnixNano())
		identity.writeString(freshnessIdentity(info))
		identity.writeUint64(uint64(len(entries)))
		for _, entry := range entries {
			entryInfo, infoErr := entry.Info()
			if infoErr != nil {
				return infoErr
			}
			identity.writeString(entry.Name())
			identity.writeUint64(uint64(entry.Type()))
			identity.writeUint64(uint64(entryInfo.Mode()))
			identity.writeInt64(entryInfo.Size())
			identity.writeInt64(entryInfo.ModTime().UnixNano())
			identity.writeString(freshnessIdentity(entryInfo))
		}
	}
	return nil
}

func newSourceFreshnessHasher() *sourceFreshnessHasher {
	identity := &sourceFreshnessHasher{hash: sha256.New()}
	identity.writeString("reconc-runtime-source-freshness-v2")
	return identity
}

func (h *sourceFreshnessHasher) write(data []byte) {
	if h.err != nil {
		return
	}
	n, err := h.hash.Write(data)
	if err != nil {
		h.err = err
		return
	}
	if n != len(data) {
		h.err = io.ErrShortWrite
	}
}

func (h *sourceFreshnessHasher) writeUint64(value uint64) {
	binary.BigEndian.PutUint64(h.number[:], value)
	h.write(h.number[:])
}

func (h *sourceFreshnessHasher) writeInt64(value int64) {
	h.writeUint64(uint64(value))
}

func (h *sourceFreshnessHasher) writeBool(value bool) {
	h.number[0] = 0
	if value {
		h.number[0] = 1
	}
	h.write(h.number[:1])
}

func (h *sourceFreshnessHasher) writeString(value string) {
	h.writeUint64(uint64(len(value)))
	for len(value) > 0 {
		count := copy(h.strings[:], value)
		h.write(h.strings[:count])
		value = value[count:]
	}
}

func (h *sourceFreshnessHasher) writeDigest(value [sha256.Size]byte) {
	copy(h.strings[:sha256.Size], value[:])
	h.write(h.strings[:sha256.Size])
}

func (h *sourceFreshnessHasher) sum() ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	if h.err != nil {
		return sum, h.err
	}
	h.hash.Sum(sum[:0])
	return sum, nil
}

func writeFreshnessDiscovery(identity *sourceFreshnessHasher, discovery freshnessDiscovery) {
	identity.writeString("discovery")
	identity.writeString(discovery.RepoRoot)
	identity.writeBool(discovery.Discovered)
	identity.writeString(discovery.ClaudePath)
	identity.writeString(discovery.AgentsPath)
	identity.writeString(discovery.StartMDPath)
	identity.writeString(discovery.ConfigPath)
	writeFreshnessStrings(identity, "config-candidates", discovery.ConfigCandidates)
	writeFreshnessStrings(identity, "policy-paths", discovery.PolicyPaths)
}

func writeFreshnessSources(identity *sourceFreshnessHasher, sources []runtimeSource) {
	identity.writeString("sources")
	identity.writeUint64(uint64(len(sources)))
	for _, source := range sources {
		identity.writeString(string(source.Kind))
		identity.writeString(source.Path)
		identity.writeString(source.ContentSHA256)
		identity.writeString(source.BlockID)
		identity.writeInt64(int64(source.LineStart))
	}
}

func writeFreshnessStrings(identity *sourceFreshnessHasher, label string, values []string) {
	identity.writeString(label)
	identity.writeUint64(uint64(len(values)))
	for _, value := range values {
		identity.writeString(value)
	}
}

func writeFreshnessFile(identity *sourceFreshnessHasher, file freshnessFile) {
	identity.writeString(file.Path)
	identity.writeBool(file.Exists)
	identity.writeUint64(uint64(file.Mode))
	identity.writeInt64(file.Size)
	identity.writeInt64(file.ModTime)
	identity.writeString(file.Identity)
	identity.writeDigest(file.Digest)
}

func normalizeFreshnessDiscovery(discovery ingest.DiscoveryResult) freshnessDiscovery {
	return freshnessDiscovery{
		RepoRoot: discovery.RepoRoot, Discovered: discovery.Discovered,
		ClaudePath: optionalString(discovery.ClaudePath), AgentsPath: optionalString(discovery.AgentsPath),
		StartMDPath: optionalString(discovery.StartMDPath), ConfigPath: optionalString(discovery.ConfigPath),
		ConfigCandidates: discovery.ConfigCandidates,
		PolicyPaths:      discovery.PolicyPaths,
	}
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func addFile(paths map[string]struct{}, path string) {
	paths[filepath.Clean(path)] = struct{}{}
}

func addDirectory(paths map[string]struct{}, path string) {
	paths[filepath.Clean(path)] = struct{}{}
}

func addSourceParentDirectory(paths map[string]struct{}, root, directory string) {
	cleaned := filepath.Clean(directory)
	if cleaned == filepath.Clean(root) || cleaned == filepath.Join(filepath.Clean(root), ".reconc") {
		return
	}
	addDirectory(paths, cleaned)
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
