package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/presets"
)

func Inspect(repoRoot string) (*Inspection, error) {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	stacks := detectStacks(root)
	suggestions, err := presets.SuggestForStacks(stacks)
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
		DetectedStacks: stacks, PackSuggestions: packNames,
		DetectedPlatforms: platforms, ExistingPaths: existing,
		BinaryResolution: ResolveRepoBinary(root, runtime.GOOS, runtime.GOARCH),
	}, nil
}

func canonicalRepoRoot(repoRoot string) (string, error) {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("repository path does not exist: %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repository path is not a directory: %s", abs)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve repository symlinks: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func detectStacks(root string) []string {
	stacks := []string{}
	if regularPath(filepath.Join(root, "go.mod")) {
		stacks = append(stacks, "go")
	}
	if regularPath(filepath.Join(root, "package.json")) &&
		(regularPath(filepath.Join(root, "bun.lock")) || regularPath(filepath.Join(root, "bun.lockb"))) {
		stacks = append(stacks, "bun")
	}
	if regularPath(filepath.Join(root, "Cargo.toml")) {
		stacks = append(stacks, "rust")
	}
	if regularPath(filepath.Join(root, "pyproject.toml")) ||
		regularPath(filepath.Join(root, "requirements.txt")) ||
		regularPath(filepath.Join(root, "setup.cfg")) ||
		regularPath(filepath.Join(root, "setup.py")) {
		stacks = append(stacks, "python")
	}
	sort.Strings(stacks)
	return stacks
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

func regularPath(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
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
