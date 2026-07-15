package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/execfile"
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
	Kind          string          `json:"kind"`
	DisplayName   string          `json:"display_name"`
	TargetPath    string          `json:"target_path"`
	State         ActivationState `json:"state"`
	Detail        string          `json:"detail"`
	MissingEvents []string        `json:"missing_events,omitempty"`
	LastSeen      string          `json:"last_seen,omitempty"`
	LastEvent     string          `json:"last_event,omitempty"`
	LivenessError string          `json:"liveness_error,omitempty"`
}

// InspectPlatforms validates every registered artifact and activation probe.
func InspectPlatforms(repoRoot string) ([]PlatformStatus, error) {
	root, err := existingRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	reports := make([]PlatformStatus, 0, len(platformRegistry))
	for _, platform := range Platforms() {
		reports = append(reports, inspectPlatform(root, platform))
	}
	return reports, nil
}

func inspectPlatform(root string, platform Platform) PlatformStatus {
	report := PlatformStatus{Kind: platform.Kind, DisplayName: platform.DisplayName, TargetPath: platform.TargetPath, State: StateAbsent, Detail: "artifact not installed"}
	target := filepath.Join(root, filepath.FromSlash(platform.TargetPath))
	data, err := os.ReadFile(target)
	if os.IsNotExist(err) && platform.Activation.LegacyArtifactPath != "" {
		legacyTarget := filepath.Join(root, filepath.FromSlash(platform.Activation.LegacyArtifactPath))
		if legacyData, legacyErr := os.ReadFile(legacyTarget); legacyErr == nil {
			data = legacyData
			err = nil
			report.TargetPath = platform.Activation.LegacyArtifactPath
			report.Detail = "legacy artifact path is selected; reinstall to migrate to " + platform.TargetPath
		}
	}
	if err != nil {
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
	if managedArtifactRequiresExactMatch(platform.InstallMode) {
		generated, generateErr := Generate(platform.Kind)
		if generateErr != nil || string(data) != generated.Content {
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

	report.MissingEvents = missingRuntimeEvents(platform, string(data))
	if len(report.MissingEvents) > 0 {
		report.State = StateDegraded
		report.Detail = fmt.Sprintf("artifact misses %d generated runtime route(s); reinstall the hook", len(report.MissingEvents))
		return report
	}
	if platform.Kind == KindCopilot && jsonBoolean(data, "disableAllHooks") {
		report.State = StateUnsupported
		report.Detail = "this Copilot hook file sets disableAllHooks=true"
		return report
	}
	if platform.Activation.DisabledByEnv != "" && envTruthy(platform.Activation.DisabledByEnv) {
		report.State = StateUnsupported
		report.Detail = platform.Activation.DisabledByEnv + " disables external project plugins in this process"
		return report
	}
	if platform.Kind == KindCopilot && copilotRepositoryHooksDisabled(root) {
		report.State = StateShadowed
		report.Detail = "repository Copilot settings disable all non-policy hooks"
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
	case ActivationGitPath:
		if shadowPath := gitHooksShadowPath(root); shadowPath != "" {
			report.State = StateShadowed
			report.Detail = "git core.hooksPath=" + shadowPath + " bypasses " + platform.TargetPath
			return report
		}
	}

	report.State = StateConfigured
	if report.TargetPath == platform.TargetPath {
		report.Detail = "configuration is complete and host-discoverable; live execution is reported separately"
	}
	return report
}

func unsupportedNativeEvents(platform Platform, content string) []string {
	var unexpected []string
	for _, capability := range platform.Capabilities {
		if capability.Support == SupportUnsupported && capability.NativeEvent != "" && strings.Contains(content, `"`+capability.NativeEvent+`"`) {
			unexpected = append(unexpected, "unsupported "+capability.NativeEvent)
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
		if capability.Support == SupportUnsupported || len(capability.RuntimeEvents) == 0 {
			continue
		}
		for _, runtimeEvent := range capability.RuntimeEvents {
			matches := 0
			for _, group := range document.Hooks[capability.NativeEvent] {
				for _, handler := range group.Hooks {
					if !strings.Contains(handler.Command, runtimeEvent) {
						continue
					}
					matches++
					if handler.Timeout == nil || *handler.Timeout != capability.TimeoutSeconds {
						issues = append(issues, fmt.Sprintf("%s timeout must be %ds", runtimeEvent, capability.TimeoutSeconds))
					}
				}
			}
			if matches != 1 {
				issues = append(issues, fmt.Sprintf("%s route count is %d", runtimeEvent, matches))
			}
		}
	}
	return issues
}

func managedArtifactRequiresExactMatch(mode InstallMode) bool {
	switch mode {
	case InstallExecutable, InstallManagedJSON, InstallPlugin:
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
		for _, event := range capability.RuntimeEvents {
			if !strings.Contains(content, event) {
				missing = append(missing, event)
			}
		}
	}
	if platform.Kind == KindGitPreCommit &&
		(!strings.Contains(content, "reconc ci") || !strings.Contains(content, "--staged")) {
		missing = append(missing, "git-pre-commit")
	}
	return missing
}

func jsonBoolean(data []byte, key string) bool {
	var raw map[string]interface{}
	if json.Unmarshal(data, &raw) != nil {
		return false
	}
	value, _ := raw[key].(bool)
	return value
}

func envTruthy(name string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return value != "" && value != "0" && value != "false" && value != "no"
}

func executableFile(path string) bool {
	return execfile.Is(path)
}

func tomlSectionBoolean(path, section, key string) (bool, bool, error) {
	data, err := os.ReadFile(path)
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

func gitHooksShadowPath(root string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", root, "config", "--get", "core.hooksPath")
	output, err := command.Output()
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(output))
	if value == "" || value == ".git/hooks" {
		return ""
	}
	return value
}

func copilotRepositoryHooksDisabled(root string) bool {
	for _, relative := range []string{".github/copilot/settings.json", ".github/copilot/settings.local.json"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err == nil && jsonBoolean(data, "disableAllHooks") {
			return true
		}
	}
	return false
}
