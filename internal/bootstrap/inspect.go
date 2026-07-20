package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/presets"
	"reconc.dev/reconc/internal/stackdetect"
)

func Inspect(repoRoot string) (*Inspection, error) {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	detection, err := stackdetect.Detect(root)
	if err != nil {
		return nil, err
	}
	suggestions, err := presets.SuggestForStacks(detection.Stacks)
	if err != nil {
		return nil, fmt.Errorf("suggest policy packs: %w", err)
	}
	packNames := make([]string, 0, len(suggestions))
	for _, suggestion := range suggestions {
		packNames = append(packNames, suggestion.Name)
	}
	platforms := []string{}
	for _, platform := range hooks.AgentPlatforms() {
		if platformDetected(root, platform) {
			platforms = append(platforms, platform.Kind)
		}
	}
	existing := []string{}
	for _, relative := range inspectionPaths() {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative))); err == nil {
			existing = append(existing, relative)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect %s: %w", relative, err)
		}
	}
	sort.Strings(packNames)
	sort.Strings(platforms)
	sort.Strings(existing)
	return &Inspection{
		FormatVersion: InspectFormatVersion, RepoRoot: root,
		DetectedStacks: detection.Stacks, PackSuggestions: packNames,
		DetectedPlatforms: platforms, ExistingPaths: existing,
		BinaryResolution: ResolveRepoBinary(root, runtime.GOOS, runtime.GOARCH),
	}, nil
}

func canonicalRepoRoot(repoRoot string) (string, error) {
	resolved, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository filesystem identity: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("repository path does not exist: %s: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repository path is not a directory: %s", resolved)
	}
	return filepath.Clean(resolved), nil
}

func platformDetected(root string, platform hooks.Platform) bool {
	for _, relative := range platform.Activation.ConfigDirs {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
		if err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func inspectionPaths() []string {
	paths := []string{
		".reconc.yml", ".reconc/policy.lock.json", ".gitignore", "AGENTS.md", "CLAUDE.md", "start.md",
		"docs/documentation.md", "docs/tasks.md", hooks.WrapperPath,
	}
	for _, platform := range hooks.Platforms() {
		paths = append(paths, platform.TargetPath)
	}
	sort.Strings(paths)
	return paths
}
