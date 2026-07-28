package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/execfile"
	"reconc.dev/reconc/internal/pathidentity"
)

// ActivationState is configuration truth, not proof that a live process has
// already loaded the artifact.
type ActivationState string

const (
	StateAbsent      ActivationState = "absent"
	StateInstalled   ActivationState = "installed"
	StateConfigured  ActivationState = "configured"
	StateDegraded    ActivationState = "degraded"
	StateShadowed    ActivationState = "shadowed"
	StateUnsupported ActivationState = "unsupported"
)

// PlatformStatus is one deterministic activation report.
type PlatformStatus struct {
	Kind           string                   `json:"kind"`
	DisplayName    string                   `json:"display_name"`
	TargetPath     string                   `json:"target_path"`
	State          ActivationState          `json:"state"`
	Detail         string                   `json:"detail"`
	MissingEvents  []string                 `json:"missing_events,omitempty"`
	ExpectedEvents []string                 `json:"expected_events,omitempty"`
	SurfaceEvents  map[HostSurface][]string `json:"surface_events,omitempty"`
	LiveEvents     []string                 `json:"live_events,omitempty"`
	UnseenEvents   []string                 `json:"unseen_events,omitempty"`
	LastSeen       string                   `json:"last_seen,omitempty"`
	LastEvent      string                   `json:"last_event,omitempty"`
	LivenessError  string                   `json:"liveness_error,omitempty"`
	Generated      bool                     `json:"generated"`
	Installed      bool                     `json:"installed"`
	Executable     bool                     `json:"executable"`
	Configured     bool                     `json:"configured"`
	Live           bool                     `json:"live"`
	Remediation    string                   `json:"remediation,omitempty"`
	MCP            *MCPStatus               `json:"mcp,omitempty"`
}

// MCPMappingStatus is the public, redacted view of one configured selector.
type MCPMappingStatus struct {
	Tool              string `json:"tool"`
	ServerFingerprint string `json:"server_fingerprint,omitempty"`
	Effect            string `json:"effect"`
	SourcePath        string `json:"source_path"`
}

// MCPStatus keeps configured policy and observed runtime facts separate.
type MCPStatus struct {
	UnclassifiedMode       string             `json:"unclassified_mode"`
	Mappings               []MCPMappingStatus `json:"mappings"`
	ClassifiedObserved     uint64             `json:"classified_observed"`
	UnclassifiedObserved   uint64             `json:"unclassified_observed"`
	Denied                 uint64             `json:"denied"`
	Failures               uint64             `json:"failures"`
	StrictUnavailable      uint64             `json:"strict_unavailable"`
	StrictUnclassifiedDeny bool               `json:"strict_unclassified_deny_available"`
	Limitation             string             `json:"limitation,omitempty"`
	ObservationError       string             `json:"observation_error,omitempty"`
}

// InspectPlatforms validates every registered artifact and activation probe.
func InspectPlatforms(repoRoot string) ([]PlatformStatus, error) {
	root, err := existingRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	reports := make([]PlatformStatus, 0, len(platformRegistry))
	for _, platform := range Platforms() {
		report := inspectPlatform(root, platform)
		finalizePlatformStatus(root, platform, &report)
		reports = append(reports, report)
	}
	return reports, nil
}

func finalizePlatformStatus(root string, platform Platform, report *PlatformStatus) {
	_, generateErr := Generate(platform.Kind)
	report.Generated = generateErr == nil
	report.Installed = platformArtifactInstalled(root, platform, report.TargetPath)
	targetExecutable := true
	if platform.Executable {
		target := report.TargetPath
		if !filepath.IsAbs(target) {
			target = filepath.Join(root, filepath.FromSlash(target))
		}
		targetExecutable = executableFile(target)
	}
	wrapperExecutable := true
	if platform.Activation.RequiresWrapper {
		wrapperExecutable = executableFile(filepath.Join(root, filepath.FromSlash(WrapperPath)))
	}
	report.Executable = report.Installed && targetExecutable && wrapperExecutable
	report.Configured = report.State == StateConfigured
	if report.Configured {
		return
	}
	force := ""
	if strings.Contains(report.Detail, "invalid JSON") || strings.Contains(report.Detail, "differs from") || strings.Contains(report.Detail, "unreadable") || strings.Contains(report.Detail, "does not enable") {
		force = " --force"
	}
	report.Remediation = "Run `reconc hook install " + platform.Kind + " " + quoteStatusArgument(root) + force + "`."
}

func platformArtifactInstalled(root string, platform Platform, reportedPath string) bool {
	paths := []string{reportedPath, platform.TargetPath, platform.Activation.LegacyArtifactPath}
	seen := map[string]bool{}
	for _, candidate := range paths {
		if candidate == "" {
			continue
		}
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(root, filepath.FromSlash(candidate))
		}
		candidate = filepath.Clean(candidate)
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if _, err := readManagedArtifact(candidate); err == nil {
			return true
		}
	}
	return false
}

func quoteStatusArgument(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func inspectPlatform(root string, platform Platform) PlatformStatus {
	report := PlatformStatus{
		Kind:           platform.Kind,
		DisplayName:    platform.DisplayName,
		TargetPath:     platform.TargetPath,
		State:          StateAbsent,
		Detail:         "artifact not installed",
		ExpectedEvents: platformRuntimeEvents(platform),
		SurfaceEvents:  platformSurfaceEvents(platform),
	}
	target := filepath.Join(root, filepath.FromSlash(platform.TargetPath))
	defaultTarget := target
	if platform.Activation.Mode == ActivationGitPath {
		activeTarget, displayPath, err := activeGitPreCommitPath(root)
		if err != nil {
			_, gitErr := os.Stat(filepath.Join(root, ".git"))
			_, targetErr := os.Stat(defaultTarget)
			if gitErr == nil && targetErr != nil {
				report.State = StateDegraded
				report.Detail = "cannot resolve active Git hooks path: " + err.Error()
				return report
			}
		} else {
			target = activeTarget
			report.TargetPath = displayPath
		}
	}
	data, err := readManagedArtifact(target)
	if platform.Kind == KindKilo && err == nil && platform.Activation.LegacyArtifactPath != "" {
		legacyTarget := filepath.Join(root, filepath.FromSlash(platform.Activation.LegacyArtifactPath))
		if _, legacyErr := readManagedArtifact(legacyTarget); legacyErr == nil {
			report.State = StateDegraded
			report.Detail = "canonical and legacy Kilo plugins both exist; remove the legacy copy after confirming it is not user-owned"
			return report
		}
	}
	if os.IsNotExist(err) && platform.Activation.LegacyArtifactPath != "" {
		legacyTarget := filepath.Join(root, filepath.FromSlash(platform.Activation.LegacyArtifactPath))
		if legacyData, legacyErr := readManagedArtifact(legacyTarget); legacyErr == nil {
			data = legacyData
			err = nil
			target = legacyTarget
			report.TargetPath = platform.Activation.LegacyArtifactPath
			report.Detail = "legacy artifact path is selected; reinstall to migrate to " + platform.TargetPath
		}
	}
	if err != nil {
		if platform.Activation.Mode == ActivationGitPath && os.IsNotExist(err) && filepath.Clean(target) != filepath.Clean(defaultTarget) {
			if _, defaultErr := os.Stat(defaultTarget); defaultErr == nil {
				report.State = StateShadowed
				report.Detail = "git core.hooksPath selects " + report.TargetPath + " but the managed hook exists only at " + platform.TargetPath
				return report
			}
		}
		if !os.IsNotExist(err) {
			report.State = StateDegraded
			report.Detail = "artifact is unreadable: " + err.Error()
		}
		return report
	}
	if requiresJSON(platform.InstallMode) && !json.Valid(data) {
		report.State = StateDegraded
		report.Detail = "artifact is invalid JSON; reinstall the hook"
		return report
	}
	if platform.Executable && !executableFile(target) {
		report.State = StateDegraded
		report.Detail = "artifact is installed but not executable; reinstall the hook"
		return report
	}
	generated, generateErr := Generate(platform.Kind)
	if generateErr != nil {
		report.State = StateDegraded
		report.Detail = "cannot generate current artifact contract: " + generateErr.Error()
		return report
	}
	if managedArtifactRequiresExactMatch(platform.InstallMode) {
		if string(data) != generated.Content {
			report.State = StateDegraded
			report.Detail = "managed artifact differs from the current generator; reinstall the hook"
			return report
		}
	}
	contractIssues := unsupportedNativeEvents(platform, string(data))
	if platform.Kind == KindCodex {
		contractIssues = append(contractIssues, codexRouteBudgetIssues(data, platform)...)
	}
	if len(contractIssues) > 0 {
		report.State = StateDegraded
		report.Detail = "artifact contract drift: " + strings.Join(contractIssues, "; ") + "; reinstall the hook"
		return report
	}
	if requiresJSON(platform.InstallMode) && platform.InstallMode != InstallManagedJSON {
		missingEntries, entryErr := missingGeneratedJSONEntries(platform.InstallMode, []byte(generated.Content), data)
		if entryErr != nil {
			report.State = StateDegraded
			report.Detail = "artifact contract cannot be inspected: " + entryErr.Error()
			return report
		}
		if len(missingEntries) > 0 {
			report.State = StateDegraded
			report.Detail = "artifact contract drift: missing " + strings.Join(missingEntries, ", ") + "; reinstall the hook"
			return report
		}
	}

	report.MissingEvents = missingRuntimeEvents(platform, string(data))
	if len(report.MissingEvents) > 0 {
		report.State = StateDegraded
		report.Detail = fmt.Sprintf("artifact misses %d generated runtime route(s); reinstall the hook", len(report.MissingEvents))
		return report
	}
	if platform.Activation.DisabledByEnv != "" && envTruthy(platform.Activation.DisabledByEnv) {
		report.State = StateUnsupported
		report.Detail = platform.Activation.DisabledByEnv + " disables external project plugins in this process"
		return report
	}
	if platform.Activation.RequiresWrapper && !executableFile(filepath.Join(root, "tools", "reconc", "bin", "hook")) {
		report.State = StateDegraded
		report.Detail = "configuration is complete but tools/reconc/bin/hook is missing or not executable"
		return report
	}

	switch platform.Activation.Mode {
	case ActivationFlag:
		enabled, present, probeErr := tomlSectionBoolean(
			filepath.Join(root, filepath.FromSlash(platform.Activation.EnablePath)),
			platform.Activation.EnableSection,
			platform.Activation.EnableKey,
		)
		if probeErr != nil {
			report.State = StateDegraded
			report.Detail = platform.Activation.EnablePath + " is invalid: " + probeErr.Error()
			return report
		}
		if !present {
			enabled = platform.Activation.EnabledByDefault
		}
		if !enabled {
			report.State = StateInstalled
			report.Detail = "artifact is installed but " + platform.Activation.EnablePath + " does not enable " + platform.Activation.EnableSection + "." + platform.Activation.EnableKey
			return report
		}
	}

	report.State = StateConfigured
	if report.TargetPath == platform.TargetPath {
		report.Detail = "configuration is complete and host-discoverable; live execution is reported separately"
	}
	return report
}

func missingGeneratedJSONEntries(mode InstallMode, generated, installed []byte) ([]string, error) {
	var generatedDocument map[string]interface{}
	if err := json.Unmarshal(generated, &generatedDocument); err != nil {
		return nil, err
	}
	var installedDocument map[string]interface{}
	if err := json.Unmarshal(installed, &installedDocument); err != nil {
		return nil, err
	}
	generatedHooks, err := hookEventMap(mode, generatedDocument)
	if err != nil {
		return nil, err
	}
	installedHooks, err := hookEventMap(mode, installedDocument)
	if err != nil {
		return nil, err
	}
	missing := []string{}
	for event, expectedRaw := range generatedHooks {
		expected, ok := expectedRaw.([]interface{})
		if !ok {
			return nil, fmt.Errorf("generated event %s is not an array", event)
		}
		actual, _ := installedHooks[event].([]interface{})
		used := make([]bool, len(actual))
		for index, expectedEntry := range expected {
			found := false
			for actualIndex, actualEntry := range actual {
				if !used[actualIndex] && reflect.DeepEqual(actualEntry, expectedEntry) {
					used[actualIndex] = true
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, fmt.Sprintf("%s[%d]", event, index))
			}
		}
	}
	sort.Strings(missing)
	return missing, nil
}

func hookEventMap(mode InstallMode, document map[string]interface{}) (map[string]interface{}, error) {
	var raw interface{}
	switch mode {
	case InstallFlatJSON:
		return document, nil
	case InstallOwnedJSON:
		raw = document["reconc"]
	default:
		raw = document["hooks"]
	}
	events, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("hook event map is missing or invalid")
	}
	return events, nil
}

func platformRuntimeEvents(platform Platform) []string {
	events := []string{}
	for _, capability := range platform.Capabilities {
		if capability.Support == SupportUnsupported {
			continue
		}
		for _, binding := range capability.Bindings {
			if binding.RuntimeEvent != "" {
				events = append(events, binding.RuntimeEvent)
			}
		}
	}
	return events
}

func platformSurfaceEvents(platform Platform) map[HostSurface][]string {
	surfaces := map[HostSurface][]string{}
	for _, capability := range platform.Capabilities {
		if capability.Support == SupportUnsupported {
			continue
		}
		for _, binding := range capability.Bindings {
			if binding.RuntimeEvent == "" {
				continue
			}
			for _, surface := range binding.Surfaces {
				surfaces[surface] = append(surfaces[surface], binding.RuntimeEvent)
			}
		}
	}
	if len(surfaces) == 0 {
		return nil
	}
	return surfaces
}

func unsupportedNativeEvents(platform Platform, content string) []string {
	var unexpected []string
	for _, capability := range platform.Capabilities {
		if capability.Support != SupportUnsupported {
			continue
		}
		for _, binding := range capability.Bindings {
			if binding.NativeEvent != "" && strings.Contains(content, `"`+binding.NativeEvent+`"`) {
				unexpected = append(unexpected, "unsupported "+binding.NativeEvent)
			}
		}
	}
	return unexpected
}

func codexRouteBudgetIssues(data []byte, platform Platform) []string {
	var document struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Timeout *int   `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return []string{"invalid Codex route structure"}
	}
	var issues []string
	for _, capability := range platform.Capabilities {
		if capability.Support == SupportUnsupported || len(capability.Bindings) == 0 {
			continue
		}
		for _, binding := range capability.Bindings {
			if binding.RuntimeEvent == "" || binding.Compatibility {
				continue
			}
			matches := 0
			for _, group := range document.Hooks[binding.NativeEvent] {
				for _, handler := range group.Hooks {
					if !strings.Contains(handler.Command, binding.RuntimeEvent) {
						continue
					}
					matches++
					if handler.Timeout == nil || *handler.Timeout != capability.TimeoutSeconds {
						issues = append(issues, fmt.Sprintf("%s timeout must be %ds", binding.RuntimeEvent, capability.TimeoutSeconds))
					}
				}
			}
			if matches != 1 {
				issues = append(issues, fmt.Sprintf("%s route count is %d", binding.RuntimeEvent, matches))
			}
		}
	}
	return issues
}

func managedArtifactRequiresExactMatch(mode InstallMode) bool {
	switch mode {
	case InstallExecutable, InstallPlugin, InstallManagedJSON:
		return true
	default:
		return false
	}
}

func requiresJSON(mode InstallMode) bool {
	switch mode {
	case InstallNestedJSON, InstallFlatJSON, InstallOwnedJSON, InstallManagedJSON:
		return true
	default:
		return false
	}
}

func missingRuntimeEvents(platform Platform, content string) []string {
	missing := []string{}
	for _, capability := range platform.Capabilities {
		for _, binding := range capability.Bindings {
			if binding.RuntimeEvent != "" && !binding.Compatibility && !strings.Contains(content, binding.RuntimeEvent) {
				missing = append(missing, binding.RuntimeEvent)
			}
		}
	}
	if platform.Kind == KindGitPreCommit &&
		(!strings.Contains(content, "reconc ci") || !strings.Contains(content, "--staged")) {
		missing = append(missing, "git-pre-commit")
	}
	return missing
}

func envTruthy(name string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return value != "" && value != "0" && value != "false" && value != "no"
}

func executableFile(path string) bool {
	return execfile.Is(path)
}

func tomlSectionBoolean(path, section, key string) (bool, bool, error) {
	data, err := readManagedArtifact(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, err
	}
	currentSection := ""
	sectionSeen := false
	found := false
	enabled := false
	for lineNumber, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if strings.HasPrefix(line, "[[") || strings.HasSuffix(line, "]]") {
				currentSection = ""
				continue
			}
			currentSection = strings.TrimSpace(line[1 : len(line)-1])
			if currentSection == section {
				if sectionSeen {
					return false, false, fmt.Errorf("duplicate [%s] table", section)
				}
				sectionSeen = true
			}
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != key {
			continue
		}
		if currentSection != section {
			if currentSection == "" {
				return false, false, fmt.Errorf("line %d places %s at the TOML root; expected [%s]", lineNumber+1, key, section)
			}
			continue
		}
		if found {
			return false, false, fmt.Errorf("duplicate %s.%s", section, key)
		}
		found = true
		switch strings.TrimSpace(parts[1]) {
		case "true":
			enabled = true
		case "false":
			enabled = false
		default:
			return false, false, fmt.Errorf("%s.%s must be a boolean", section, key)
		}
	}
	return enabled, found, nil
}

func activeGitPreCommitPath(root string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--git-path", "hooks")
	output, err := command.Output()
	if err != nil {
		return "", "", err
	}
	hooksPath := strings.TrimSpace(string(output))
	if hooksPath == "" {
		return "", "", fmt.Errorf("git returned an empty hooks path")
	}
	if !filepath.IsAbs(hooksPath) {
		hooksPath = filepath.Join(root, hooksPath)
	}
	target := filepath.Clean(filepath.Join(hooksPath, "pre-commit"))
	display := target
	if rel, relErr := filepath.Rel(root, target); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		display = filepath.ToSlash(rel)
	}
	return target, display, nil
}

func gitHookTargetIsRepositoryOwned(root, target string) (bool, error) {
	owned, err := resolvedPathWithinDirectory(root, target)
	if err != nil {
		return false, err
	}
	if owned {
		return true, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--git-common-dir")
	output, err := command.Output()
	if err != nil {
		return false, err
	}
	commonDir := strings.TrimSpace(string(output))
	if commonDir == "" {
		return false, fmt.Errorf("git returned an empty common directory")
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(root, commonDir)
	}
	return resolvedPathWithinDirectory(filepath.Clean(commonDir), target)
}

func pathWithinDirectory(root, target string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func resolvedPathWithinDirectory(root, target string) (bool, error) {
	resolvedRoot, err := resolveProspectivePath(root)
	if err != nil {
		return false, err
	}
	resolvedTarget, err := resolveProspectivePath(target)
	if err != nil {
		return false, err
	}
	return pathWithinDirectory(resolvedRoot, resolvedTarget), nil
}

func resolveProspectivePath(path string) (string, error) {
	return pathidentity.ResolveProspective(path)
}
