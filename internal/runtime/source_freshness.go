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

	"gopkg.in/yaml.v3"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/presets"
)

const (
	maxFreshnessFiles       = 4096
	maxFreshnessDirectories = 4096
	maxFreshnessDirEntries  = 4096
	maxFreshnessFileBytes   = 8 << 20
	maxFreshnessTotalBytes  = 64 << 20
)

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
	return observeSourceFreshness(root, plan.sources, discovery)
}

func observeRuntimeSourceFreshnessFromBundle(root string, plan *runtimePlan, bundle *ingest.SourceBundle) ([sha256.Size]byte, error) {
	if plan == nil || bundle == nil {
		return [sha256.Size]byte{}, errors.New("runtime source freshness requires a plan and bundle")
	}
	return observeSourceFreshness(root, plan.sources, bundle.Discovery)
}

func observeSourceFreshness(root string, sources []runtimeSource, discovery ingest.DiscoveryResult) ([sha256.Size]byte, error) {
	files := map[string]struct{}{}
	directories := map[string]struct{}{}
	virtualPresets := map[string]struct{}{}
	includePatterns := []string{}
	addDirectory(directories, filepath.Join(root, "policies"))
	addDirectory(directories, filepath.Join(root, ".reconc", "runtimes"))
	addDirectory(directories, filepath.Join(presets.Home(), "presets"))
	addFile(files, filepath.Join(presets.Home(), ingest.GlobalPolicyFilename))
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
	if discovery.ConfigPath != nil {
		configPath := filepath.Join(root, filepath.FromSlash(*discovery.ConfigPath))
		patterns, err := freshnessIncludePatterns(configPath)
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		includePatterns = append(includePatterns, patterns...)
		for _, pattern := range patterns {
			base, err := freshnessGlobBase(root, pattern)
			if err != nil {
				return [sha256.Size]byte{}, err
			}
			if filepath.Clean(base) != filepath.Clean(root) {
				addDirectory(directories, base)
			}
			matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
			if err != nil {
				return [sha256.Size]byte{}, fmt.Errorf("expand freshness pattern %s: %w", pattern, err)
			}
			for _, match := range matches {
				info, statErr := os.Lstat(match)
				if statErr != nil {
					return [sha256.Size]byte{}, statErr
				}
				if info.IsDir() {
					addDirectory(directories, match)
					continue
				}
				if info.Mode().IsRegular() {
					addFile(files, match)
				}
			}
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
		return filepath.Join(presets.Home(), ingest.GlobalPolicyFilename), "", nil
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

func freshnessIncludePatterns(configPath string) ([]string, error) {
	data, err := boundedio.ReadRegularFile(configPath, maxFreshnessFileBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var document map[string]interface{}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse compiler config for freshness: %w", err)
	}
	raw, ok := document["include"]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, errors.New("compiler config include must be a list")
	}
	out := make([]string, 0, len(list)+len(ingest.DefaultPolicyGlobs))
	out = append(out, ingest.DefaultPolicyGlobs...)
	for _, item := range list {
		pattern, ok := item.(string)
		pattern = strings.TrimSpace(pattern)
		if !ok || pattern == "" || path.IsAbs(pattern) || filepath.IsAbs(pattern) || strings.Contains(pattern, "..") {
			return nil, errors.New("compiler config include contains an invalid pattern")
		}
		out = append(out, pattern)
	}
	sort.Strings(out)
	return slicesUnique(out), nil
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
	var totalBytes int64
	for _, filePath := range ordered {
		observation, err := observeFreshnessFile(filePath, &totalBytes)
		if err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	return observations, nil
}

func observeFreshnessFile(path string, totalBytes *int64) (freshnessFile, error) {
	observation := freshnessFile{Path: path}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return observation, nil
	}
	if err != nil {
		return observation, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return observation, fmt.Errorf("runtime freshness source must be a non-symlink regular file: %s", path)
	}
	if info.Size() > maxFreshnessFileBytes || *totalBytes > maxFreshnessTotalBytes-info.Size() {
		return observation, fmt.Errorf("runtime freshness files exceed bounded byte budget")
	}
	*totalBytes += info.Size()
	hash := sha256.New()
	var readBytes int64
	err = boundedio.WithRegularFileSnapshot(path, maxFreshnessFileBytes, func(file *os.File, opened os.FileInfo) error {
		var copyErr error
		readBytes, copyErr = io.Copy(hash, io.LimitReader(file, maxFreshnessFileBytes+1))
		if copyErr != nil {
			return copyErr
		}
		if readBytes != opened.Size() {
			return fmt.Errorf("runtime freshness source changed while reading: %s", path)
		}
		return nil
	})
	if err != nil {
		return observation, err
	}
	observation.Exists = true
	observation.Mode = uint32(info.Mode())
	observation.Size = info.Size()
	observation.ModTime = info.ModTime().UnixNano()
	observation.Identity = freshnessIdentity(info)
	observation.Digest = hex.EncodeToString(hash.Sum(nil))
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

func slicesUnique(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
