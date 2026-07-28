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
	statuses, err := hooks.InspectPlatforms(root)
	if err != nil {
		return nil, fmt.Errorf("inspect hook platforms: %w", err)
	}
	statusByKind := map[string]hooks.PlatformStatus{}
	for _, status := range statuses {
		statusByKind[status.Kind] = status
	}
	platforms := []string{}
	platformStatuses := []PlatformInspection{}
	for _, platform := range hooks.Platforms() {
		status := statusByKind[platform.Kind]
		evidence := platformDetectionEvidence(root, platform, status)
		if len(evidence) == 0 && status.State == hooks.StateAbsent {
			continue
		}
		platforms = append(platforms, platform.Kind)
		platformStatuses = append(platformStatuses, PlatformInspection{
			Kind: platform.Kind, DisplayName: platform.DisplayName, TargetPath: status.TargetPath,
			State: string(status.State), Detail: status.Detail, Evidence: evidence,
			Generated: status.Generated, Installed: status.Installed, Executable: status.Executable,
			Configured: status.Configured, Remediation: status.Remediation,
		})
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
	detectionState := "known"
	if len(detection.Ambiguities) > 0 {
		detectionState = "ambiguous"
	} else if len(detection.Stacks) == 0 && len(detection.PackageManagers) == 0 {
		detectionState = "unknown"
	}
	return &Inspection{
		FormatVersion: InspectFormatVersion, RepoRoot: root,
		DetectionState: detectionState, DetectedStacks: detection.Stacks,
		StackEvidence: detectionEvidence(detection.Evidence), PackageManagers: detectionEvidence(detection.PackageManagers),
		RepositoryMarkers: detection.RepositoryMarkers, Ambiguities: detection.Ambiguities,
		PackSuggestions: packNames, DetectedPlatforms: platforms, PlatformStatuses: platformStatuses, ExistingPaths: existing,
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

func platformDetectionEvidence(root string, platform hooks.Platform, status hooks.PlatformStatus) []string {
	evidence := []string{}
	for _, relative := range platform.Activation.ConfigDirs {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative))); err == nil {
			evidence = append(evidence, relative)
		}
	}
	if status.State != hooks.StateAbsent && !containsInspectionPath(evidence, status.TargetPath) {
		evidence = append(evidence, status.TargetPath)
	}
	sort.Strings(evidence)
	return evidence
}

func containsInspectionPath(paths []string, wanted string) bool {
	for _, path := range paths {
		if path == wanted {
			return true
		}
	}
	return false
}

func detectionEvidence(values map[string][]string) []DetectionEvidence {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]DetectionEvidence, 0, len(names))
	for _, name := range names {
		result = append(result, DetectionEvidence{Name: name, Paths: append([]string{}, values[name]...)})
	}
	return result
}

func inspectionPaths() []string {
	paths := []string{
		".reconc.yml", ".reconc/install.lock.json", ".reconc/policy.lock.json", ".gitignore", "AGENTS.md", "CLAUDE.md", "start.md",
		"docs/documentation.md", "docs/tasks.md", hooks.WrapperPath,
	}
	for _, platform := range hooks.Platforms() {
		paths = append(paths, platform.TargetPath)
	}
	sort.Strings(paths)
	return paths
}
