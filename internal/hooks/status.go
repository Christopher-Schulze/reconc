package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ActivationState is configuration truth, not proof that a live process has
// already loaded the artifact.
type ActivationState string

const (
	StateAbsent      ActivationState = "absent"
	StateInstalled   ActivationState = "installed"
	StateActive      ActivationState = "active"
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
			report.Detail = "legacy artifact path is active; reinstall to migrate to " + platform.TargetPath
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
		if !activationTokenPresent(filepath.Join(root, filepath.FromSlash(platform.Activation.EnablePath)), platform.Activation.EnableToken) {
			report.State = StateInstalled
			report.Detail = "artifact is installed but " + platform.Activation.EnablePath + " does not enable hooks"
			return report
		}
	case ActivationGitPath:
		if shadowPath := gitHooksShadowPath(root); shadowPath != "" {
			report.State = StateShadowed
			report.Detail = "git core.hooksPath=" + shadowPath + " bypasses " + platform.TargetPath
			return report
		}
	}

	report.State = StateActive
	if report.TargetPath == platform.TargetPath {
		report.Detail = "configuration is complete and auto-discoverable"
	}
	return report
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
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}

func activationTokenPresent(path, token string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	want := strings.ReplaceAll(token, " ", "")
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.SplitN(line, "#", 2)[0]
		line = strings.NewReplacer(" ", "", "\t", "").Replace(line)
		if line == want {
			return true
		}
	}
	return false
}

func gitHooksShadowPath(root string) string {
	command := exec.Command("git", "-C", root, "config", "--get", "core.hooksPath")
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
