package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/ingest"
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
	RepoRoot         string   `json:"repo_root"`
	Discovered       bool     `json:"discovered"`
	ClaudePath       string   `json:"claude_path,omitempty"`
	AgentsPath       string   `json:"agents_path,omitempty"`
	StartMDPath      string   `json:"start_md_path,omitempty"`
	ConfigPath       string   `json:"config_path,omitempty"`
	ConfigCandidates []string `json:"config_candidates"`
	PolicyPaths      []string `json:"policy_paths"`
}

type freshnessFile struct {
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Mode     uint32 `json:"mode,omitempty"`
	Size     int64  `json:"size,omitempty"`
	ModTime  int64  `json:"mtime_ns,omitempty"`
	Identity string `json:"identity,omitempty"`
	Digest   string `json:"digest,omitempty"`
}

type freshnessDirectoryEntry struct {
	Name     string `json:"name"`
	Type     uint32 `json:"type"`
	Mode     uint32 `json:"mode,omitempty"`
	Size     int64  `json:"size,omitempty"`
	ModTime  int64  `json:"mtime_ns,omitempty"`
	Identity string `json:"identity,omitempty"`
}

type freshnessDirectory struct {
	Path     string                    `json:"path"`
	Exists   bool                      `json:"exists"`
	Mode     uint32                    `json:"mode,omitempty"`
	ModTime  int64                     `json:"mtime_ns,omitempty"`
	Identity string                    `json:"identity,omitempty"`
	Entries  []freshnessDirectoryEntry `json:"entries,omitempty"`
}

type sourceFreshnessSnapshot struct {
	Discovery       freshnessDiscovery   `json:"discovery"`
	Sources         []runtimeSource      `json:"sources"`
	IncludePatterns []string             `json:"include_patterns,omitempty"`
	VirtualPresets  []string             `json:"virtual_presets,omitempty"`
	Files           []freshnessFile      `json:"files"`
	Directories     []freshnessDirectory `json:"directories"`
}

func observeRuntimeSourceFreshness(root string, plan *runtimePlan) ([sha256.Size]byte, error) {
	if plan == nil {
		return [sha256.Size]byte{}, errors.New("runtime source freshness requires a plan")
	}
	discovery, err := ingest.DiscoverPolicyRepo(root)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return observeSourceFreshness(root, plan.sources, discovery, plan.sourceFreshness)
}

func observeRuntimeSourceFreshnessFromBundle(root string, plan *runtimePlan, bundle *ingest.SourceBundle) ([sha256.Size]byte, error) {
	if plan == nil || bundle == nil {
		return [sha256.Size]byte{}, errors.New("runtime source freshness requires a plan and bundle")
	}
	return observeSourceFreshness(root, plan.sources, bundle.Discovery, plan.sourceFreshness)
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

func observeSourceFreshness(root string, sources []runtimeSource, discovery ingest.DiscoveryResult, recipe sourceFreshnessRecipe) ([sha256.Size]byte, error) {
	if filepath.Clean(root) != recipe.root || len(recipe.includes) == 0 {
		return [sha256.Size]byte{}, errors.New("runtime source freshness recipe does not match repository root")
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
			addFile(files, match)
		}
	}
	if len(files) > maxFreshnessFiles {
		return [sha256.Size]byte{}, fmt.Errorf("runtime freshness file set exceeds %d entries", maxFreshnessFiles)
	}
	if len(directories) > maxFreshnessDirectories {
		return [sha256.Size]byte{}, fmt.Errorf("runtime freshness directory set exceeds %d entries", maxFreshnessDirectories)
	}
	fileObservations, err := observeFreshnessFiles(files)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	directoryObservations, err := observeFreshnessDirectories(directories)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	snapshot := sourceFreshnessSnapshot{
		Discovery:       normalizeFreshnessDiscovery(discovery),
		Sources:         append([]runtimeSource(nil), sources...),
		IncludePatterns: append([]string(nil), includePatterns...),
		VirtualPresets:  sortedKeys(virtualPresets),
		Files:           fileObservations,
		Directories:     directoryObservations,
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(data), nil
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

func observeFreshnessFiles(paths map[string]struct{}) ([]freshnessFile, error) {
	ordered := sortedKeys(paths)
	observations := make([]freshnessFile, 0, len(ordered))
	if len(ordered) == 0 {
		return observations, nil
	}
	var totalBytes int64
	copyBuffer := make([]byte, freshnessCopyBufferBytes)
	for _, filePath := range ordered {
		observation, err := observeFreshnessFile(filePath, &totalBytes, copyBuffer)
		if err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	return observations, nil
}

func observeFreshnessFile(path string, totalBytes *int64, copyBuffer []byte) (freshnessFile, error) {
	if len(copyBuffer) == 0 {
		return freshnessFile{}, errors.New("runtime freshness copy buffer is empty")
	}
	observation := freshnessFile{Path: path}
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return observation, nil
	}
	if err != nil {
		return observation, err
	}
	hash := sha256.New()
	var readBytes int64
	var openedInfo os.FileInfo
	var openedIdentity string
	err = withFreshnessFileSnapshot(path, maxFreshnessFileBytes, func(file *os.File, opened os.FileInfo) error {
		if *totalBytes > maxFreshnessTotalBytes-opened.Size() {
			return fmt.Errorf("runtime freshness files exceed bounded byte budget")
		}
		openedInfo = opened
		var copyErr error
		readBytes, copyErr = io.CopyBuffer(hash, io.LimitReader(file, maxFreshnessFileBytes+1), copyBuffer)
		if copyErr != nil {
			return copyErr
		}
		if readBytes != opened.Size() {
			return fmt.Errorf("runtime freshness source changed while reading: %s", path)
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
	var digest [sha256.Size]byte
	sum := hash.Sum(digest[:0])
	var encoded [sha256.Size * 2]byte
	hex.Encode(encoded[:], sum)
	observation.Digest = string(encoded[:])
	return observation, nil
}

func observeFreshnessDirectories(paths map[string]struct{}) ([]freshnessDirectory, error) {
	ordered := sortedKeys(paths)
	observations := make([]freshnessDirectory, 0, len(ordered))
	for _, directoryPath := range ordered {
		observation := freshnessDirectory{Path: directoryPath}
		info, err := os.Lstat(directoryPath)
		if errors.Is(err, os.ErrNotExist) {
			observations = append(observations, observation)
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("runtime freshness directory must be non-symlink: %s", directoryPath)
		}
		entries, err := boundedio.ReadDirNoSymlink(directoryPath, maxFreshnessDirEntries)
		if err != nil {
			return nil, err
		}
		entryObservations := make([]freshnessDirectoryEntry, 0, len(entries))
		for _, entry := range entries {
			entryObservation := freshnessDirectoryEntry{Name: entry.Name(), Type: uint32(entry.Type())}
			entryInfo, infoErr := entry.Info()
			if infoErr != nil {
				return nil, infoErr
			}
			entryObservation.Mode = uint32(entryInfo.Mode())
			entryObservation.Size = entryInfo.Size()
			entryObservation.ModTime = entryInfo.ModTime().UnixNano()
			entryObservation.Identity = freshnessIdentity(entryInfo)
			entryObservations = append(entryObservations, entryObservation)
		}
		observation.Exists = true
		observation.Mode = uint32(info.Mode())
		observation.ModTime = info.ModTime().UnixNano()
		observation.Identity = freshnessIdentity(info)
		observation.Entries = entryObservations
		observations = append(observations, observation)
	}
	return observations, nil
}

func normalizeFreshnessDiscovery(discovery ingest.DiscoveryResult) freshnessDiscovery {
	return freshnessDiscovery{
		RepoRoot: discovery.RepoRoot, Discovered: discovery.Discovered,
		ClaudePath: optionalString(discovery.ClaudePath), AgentsPath: optionalString(discovery.AgentsPath),
		StartMDPath: optionalString(discovery.StartMDPath), ConfigPath: optionalString(discovery.ConfigPath),
		ConfigCandidates: append([]string(nil), discovery.ConfigCandidates...),
		PolicyPaths:      append([]string(nil), discovery.PolicyPaths...),
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
