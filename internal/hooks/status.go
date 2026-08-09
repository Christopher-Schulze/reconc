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

	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/boundedio"
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
	Kind           string                           `json:"kind"`
	DisplayName    string                           `json:"display_name"`
	TargetPath     string                           `json:"target_path"`
	State          ActivationState                  `json:"state"`
	Detail         string                           `json:"detail"`
	MissingEvents  []string                         `json:"missing_events,omitempty"`
	ExpectedEvents []string                         `json:"expected_events,omitempty"`
	SurfaceEvents  map[HostSurface][]string         `json:"surface_events,omitempty"`
	LiveEvents     []string                         `json:"live_events,omitempty"`
	UnseenEvents   []string                         `json:"unseen_events,omitempty"`
	LastSeen       string                           `json:"last_seen,omitempty"`
	LastEvent      string                           `json:"last_event,omitempty"`
	Observations   map[string]HookObservationStatus `json:"observations,omitempty"`
	LivenessError  string                           `json:"liveness_error,omitempty"`
	Generated      bool                             `json:"generated"`
	Installed      bool                             `json:"installed"`
	Executable     bool                             `json:"executable"`
	Configured     bool                             `json:"configured"`
	Live           bool                             `json:"live"`
	Remediation    string                           `json:"remediation,omitempty"`
	MCP            *MCPStatus                       `json:"mcp,omitempty"`
}

// HookObservationStatus is the public, source-free view of one observed
// runtime surface. It deliberately carries metadata only, never host payloads.
type HookObservationStatus struct {
	Count              uint64 `json:"count"`
	LastSeen           string `json:"last_seen"`
	WorkingDirectory   string `json:"working_directory"`
	CodeBytes          int    `json:"code_bytes"`
	ExcludeFromContext bool   `json:"exclude_from_context"`
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
		if platform.Kind == KindKimiCode {
			reports = append(reports, inspectKimiCodePlatform(platform))
			continue
		}
		report := inspectPlatform(root, platform)
		finalizePlatformStatus(root, platform, &report)
		reports = append(reports, report)
	}
	return reports, nil
}

// InspectPlatform validates one registered artifact and activation probe.
// Callers that need a single readiness decision should not inspect unrelated
// global or repository-local hosts.
func InspectPlatform(repoRoot, kind string) (PlatformStatus, error) {
	root, err := existingRepoRoot(repoRoot)
	if err != nil {
		return PlatformStatus{}, err
	}
	platform, ok := PlatformForKind(kind)
	if !ok {
		return PlatformStatus{}, fmt.Errorf("unknown hook kind %q", kind)
	}
	if platform.Kind == KindKimiCode {
		return inspectKimiCodePlatform(platform), nil
	}
	report := inspectPlatform(root, platform)
	finalizePlatformStatus(root, platform, &report)
	return report, nil
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
	if report.Remediation != "" {
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
	if platform.Kind == KindZCode {
		contractIssues = append(contractIssues, zcodeConfigIssues(data)...)
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
	if platform.Kind == KindZCode {
		if _, err := exec.LookPath("sh"); err != nil {
			report.State = StateDegraded
			report.Detail = "configuration is complete but ZCode's process executor cannot resolve sh on PATH"
			return report
		}
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
	if platform.Kind == KindPi {
		trusted, detail, remediation, probeErr := inspectPiProjectTrust(root)
		if probeErr != nil {
			report.State = StateDegraded
			report.Detail = detail + ": " + probeErr.Error()
			report.Remediation = remediation
			return report
		}
		if !trusted {
			report.State = StateInstalled
			report.Detail = detail
			report.Remediation = remediation
			return report
		}
	}

	report.State = StateConfigured
	if report.TargetPath == platform.TargetPath {
		report.Detail = "configuration is complete and host-discoverable; live execution is reported separately"
	}
	return report
}

const maxPiTrustConfigBytes = 1 << 20
const maxGitPathOutputBytes = 1 << 20

func inspectPiProjectTrust(root string) (bool, string, string, error) {
	agentDir, err := piAgentDir(root)
	if err != nil {
		return false, "Pi project trust cannot be inspected", "Set PI_CODING_AGENT_DIR to a valid Pi agent directory, then rerun `reconc hook status`.", err
	}
	trustPath := filepath.Join(agentDir, "trust.json")
	trust := map[string]*bool{}
	trustData, err := readBoundedPiJSON(trustPath)
	if err == nil {
		err = json.Unmarshal(trustData, &trust)
	}
	if err != nil && !os.IsNotExist(err) {
		return false, "Pi trust store is invalid or unreadable", "Repair " + trustPath + " with Pi, then rerun `reconc hook status`.", err
	}
	canonicalRoot := root
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		canonicalRoot = resolved
	}
	for current := filepath.Clean(canonicalRoot); ; current = filepath.Dir(current) {
		if decision, present := trust[current]; present && decision != nil {
			if *decision {
				return true, "configuration is complete, project trust is saved, and Pi can discover the extension; live execution is reported separately", "", nil
			}
			return false,
				"artifact is installed but Pi has an explicit untrusted decision for " + current,
				"Use Pi's interactive project-trust flow to trust this repository, restart Pi, or pass `pi --approve` for one non-interactive run; Reconc never changes user trust.", nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}

	settingsPath := filepath.Join(agentDir, "settings.json")
	settings := struct {
		DefaultProjectTrust string `json:"defaultProjectTrust"`
	}{}
	settingsData, err := readBoundedPiJSON(settingsPath)
	if err == nil {
		err = json.Unmarshal(settingsData, &settings)
	}
	if err != nil && !os.IsNotExist(err) {
		return false, "Pi global settings are invalid or unreadable", "Repair " + settingsPath + " with Pi, then rerun `reconc hook status`.", err
	}
	switch settings.DefaultProjectTrust {
	case "always":
		return true, "configuration is complete and Pi's global defaultProjectTrust=always makes the extension host-discoverable; live execution is reported separately", "", nil
	case "never":
		return false,
			"artifact is installed but Pi's global defaultProjectTrust=never skips unapproved project extensions",
			"Use Pi's interactive project-trust flow to save trust for this repository or pass `pi --approve` for one non-interactive run; Reconc never changes user trust.", nil
	default:
		return false,
			"artifact is installed; Pi will ask for project trust interactively and skips it in non-interactive modes until trust is saved or --approve is used",
			"Start `pi` in this repository, approve the project-trust prompt, and restart Pi; non-interactive runs may pass `pi --approve` for that run.", nil
	}
}

func piAgentDir(root string) (string, error) {
	if configured := os.Getenv("PI_CODING_AGENT_DIR"); configured != "" {
		if configured == "~" || strings.HasPrefix(configured, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			if configured == "~" {
				configured = home
			} else {
				configured = filepath.Join(home, configured[2:])
			}
		}
		if !filepath.IsAbs(configured) {
			configured = filepath.Join(root, configured)
		}
		return filepath.Clean(configured), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

func readBoundedPiJSON(path string) ([]byte, error) {
	// Status inspection must not block on a FIFO or read an unbounded special
	// file, and the file identity must not change between the size check and
	// the read. boundedio.ReadFile enforces exactly that.
	//
	// Unlike readManagedArtifact, the final symlink is followed on purpose:
	// these are Pi's own user-owned configs, and a symlinked ~/.pi/agent entry
	// is the normal dotfile-manager layout. Refusing it would report a healthy
	// trust store as unreadable.
	return boundedio.ReadFile(path, int64(maxPiTrustConfigBytes))
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
	case InstallNestedEventsJSON:
		hooksSettings, ok := document["hooks"].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("hook settings map is missing or invalid")
		}
		raw = hooksSettings["events"]
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
	case InstallNestedJSON, InstallNestedEventsJSON, InstallFlatJSON, InstallOwnedJSON, InstallManagedJSON:
		return true
	default:
		return false
	}
}

func zcodeConfigIssues(data []byte) []string {
	var document struct {
		Hooks struct {
			Enabled *bool `json:"enabled"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return []string{"invalid ZCode hook settings"}
	}
	if document.Hooks.Enabled == nil || !*document.Hooks.Enabled {
		return []string{"hooks.enabled must be true"}
	}
	return nil
}

func missingRuntimeEvents(platform Platform, content string) []string {
	missing := []string{}
	for _, capability := range platform.Capabilities {
		for _, binding := range capability.Bindings {
			if binding.RuntimeEvent != "" && !binding.Compatibility && !containsRuntimeEventToken(content, binding.RuntimeEvent) {
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

// containsRuntimeEventToken reports whether the artifact carries event as a
// complete route token.
//
// Plain substring matching under-reports: many registered routes are prefixes
// of another route, for example `claude-stop` of `claude-stop-failure` and
// every `<platform>-post-tool-use` of its `-failure` sibling. An artifact that
// installs only the longer route would then hide the shorter one from status
// instead of reporting it as missing. Route tokens are lowercase, digits, and
// dashes, so a match counts only when neither neighbouring byte can continue
// the token.
func containsRuntimeEventToken(content, event string) bool {
	if event == "" {
		return false
	}
	for offset := 0; offset <= len(content)-len(event); {
		index := strings.Index(content[offset:], event)
		if index < 0 {
			return false
		}
		start := offset + index
		if !routeTokenByte(content, start-1) && !routeTokenByte(content, start+len(event)) {
			return true
		}
		offset = start + 1
	}
	return false
}

func routeTokenByte(content string, index int) bool {
	if index < 0 || index >= len(content) {
		return false
	}
	value := content[index]
	switch {
	case value >= 'a' && value <= 'z', value >= 'A' && value <= 'Z',
		value >= '0' && value <= '9', value == '-', value == '_':
		return true
	default:
		return false
	}
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
	output, err := boundedexec.Output(command, maxGitPathOutputBytes)
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
	output, err := boundedexec.Output(command, maxGitPathOutputBytes)
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
