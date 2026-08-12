package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"reconc.dev/reconc/internal/audit"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/grokacp"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/presets"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
	"reconc.dev/reconc/internal/templates"
)

const (
	doctorStatusOK            = "OK"
	doctorStatusWarn          = "WARN"
	doctorStatusFail          = "FAIL"
	doctorGrokInspectMaxBytes = 4 << 20
	doctorSourceMaxBytes      = 8 << 20
	doctorSourceAggregateMax  = 64 << 20
	mcpGatewayBoundaryDetail  = "gateway enforcement covers only explicit reconc mcp gateway routes; external client configuration is not inspected; native tools and direct downstream configurations are unenforced"

	// doctorAuditWarnBytes is the live+archive ring ceiling: the writer
	// rotates the live file at audit.DefaultMaxSizeBytes and keeps
	// audit.MaxArchiveFiles archives, so exceeding the ring total means
	// rotation itself is broken, not that the user forgot housekeeping.
	doctorAuditWarnBytes = int64(audit.DefaultMaxSizeBytes) * int64(1+audit.MaxArchiveFiles)
)

var doctorInlineBlockRegex = regexp.MustCompile("(?ms)^```reconc[ \\t]*\\r?\\n(.*?)\\r?\\n```")

type doctorDeepReport struct {
	RepoRoot string        `json:"repo_root"`
	Deep     bool          `json:"deep"`
	Checks   []doctorCheck `json:"checks"`
}

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func (r *doctorDeepReport) hasFail() bool {
	for _, check := range r.Checks {
		if check.Status == doctorStatusFail {
			return true
		}
	}
	return false
}

func buildDoctorDeepReport(repo string) (*doctorDeepReport, error) {
	discovery, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		return nil, err
	}
	report := &doctorDeepReport{
		RepoRoot: discovery.RepoRoot,
		Deep:     true,
		Checks: []doctorCheck{
			doctorCheckHookRuntimeCompatibility(discovery),
			doctorCheckGrokRuntime(discovery),
			doctorCheckGrokLeaderSteering(discovery),
			doctorCheckLockfileFreshness(discovery),
			doctorCheckMCPPolicy(discovery),
			doctorCheckAuditSize(discovery),
			doctorCheckUnknownRefs(discovery),
			doctorCheckSessionClaims(discovery),
			doctorCheckConflictCount(discovery),
		},
	}
	return report, nil
}

func doctorCheckMCPPolicy(discovery ingest.DiscoveryResult) doctorCheck {
	check := doctorCheck{
		Name:   "MCP side-effect policy",
		Status: doctorStatusOK,
		Detail: "MCP policy not configured; host behavior is preserved and MCP calls produce no classified repository evidence; " + mcpGatewayBoundaryDetail,
	}
	if !discovery.Discovered {
		check.Status = doctorStatusWarn
		check.Detail = "cannot inspect MCP policy without a discovered reconc repo; " + mcpGatewayBoundaryDetail
		return check
	}
	contract, err := runtime.LoadMCPPolicy(discovery.RepoRoot)
	if err != nil {
		check.Status = doctorStatusFail
		check.Detail = err.Error() + "; " + mcpGatewayBoundaryDetail
		return check
	}
	if contract == nil {
		return check
	}
	counts := map[policy.MCPPlatform]int{}
	for _, mapping := range contract.Tools {
		counts[mapping.Platform]++
	}
	audit, auditErr := agentsession.ReadMCPAudit(discovery.RepoRoot)
	observed := uint64(0)
	if auditErr == nil {
		for _, count := range audit.Classified {
			observed += count
		}
		for _, count := range audit.Unclassified {
			observed += count
		}
	}
	mappings := make([]string, 0, len(policy.BuiltinMCPPlatforms()))
	for _, platform := range policy.BuiltinMCPPlatforms() {
		mappings = append(mappings, fmt.Sprintf("%s=%d", platform, counts[platform]))
	}
	check.Detail = fmt.Sprintf(
		"mode=%s; mappings %s; observed=%d; server locators and payloads are redacted; %s",
		contract.Unclassified,
		strings.Join(mappings, " "),
		observed,
		mcpGatewayBoundaryDetail,
	)
	if auditErr != nil {
		check.Status = doctorStatusWarn
		check.Detail += "; observation audit unavailable: " + auditErr.Error()
	}
	if contract.Unclassified == policy.MCPUnclassifiedDeny {
		check.Status = doctorStatusWarn
		check.Detail += "; Cursor, Claude Code, and Codex can block unclassified MCP calls, but OpenCode/Kilo/OMP/Pi/ZCode generic hooks expose no discriminator for unconfigured MCP identities, so strict unclassified deny is unavailable on those surfaces"
	}
	return check
}

var doctorGrokInspect = func(ctx context.Context, repoRoot string) ([]byte, error) {
	return grokacp.InspectJSON(ctx, repoRoot, "grok")
}

var doctorProbeGrokNativeStop = grokacp.ProbeNativeStopGate

func doctorCheckGrokRuntime(discovery ingest.DiscoveryResult) doctorCheck {
	check := doctorCheck{
		Name:   "Grok native hook",
		Status: doctorStatusOK,
		Detail: "native Grok hook not installed",
	}
	if !discovery.Discovered {
		check.Status = doctorStatusWarn
		check.Detail = "cannot inspect Grok hook without a discovered reconc repo"
		return check
	}
	target := filepath.Join(discovery.RepoRoot, filepath.FromSlash(hooks.GrokHooksPath))
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return check
	} else if err != nil {
		check.Status = doctorStatusWarn
		check.Detail = "cannot stat native Grok hook: " + err.Error()
		return check
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := doctorGrokInspect(ctx, discovery.RepoRoot)
	if len(output) > doctorGrokInspectMaxBytes {
		check.Status = doctorStatusWarn
		check.Detail = fmt.Sprintf("grok inspect output exceeds %d bytes", doctorGrokInspectMaxBytes)
		return check
	}
	if err != nil {
		check.Status = doctorStatusWarn
		check.Detail = "cannot execute `grok inspect --json`: " + err.Error()
		if detail := strings.TrimSpace(string(output)); detail != "" {
			check.Detail += "; stdout: " + detail
		}
		return check
	}
	var inspection struct {
		GrokVersion    string `json:"grokVersion"`
		ProjectTrusted bool   `json:"projectTrusted"`
		Hooks          []struct {
			Target string `json:"target"`
			Source struct {
				Type string `json:"type"`
				Path string `json:"path"`
			} `json:"source"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(output, &inspection); err != nil {
		check.Status = doctorStatusWarn
		check.Detail = "invalid `grok inspect --json` output: " + err.Error()
		return check
	}
	if !inspection.ProjectTrusted {
		check.Status = doctorStatusWarn
		check.Detail = "project hook is installed but Grok does not trust this folder; run `/hooks-trust` or launch Grok with `--trust`"
		return check
	}
	expected := hooks.GrokRuntimeEvents()
	seen := map[string]bool{}
	grokSource := filepath.Clean(filepath.Join(discovery.RepoRoot, filepath.Dir(filepath.FromSlash(hooks.GrokHooksPath))))
	for _, hook := range inspection.Hooks {
		if hook.Source.Type != "project" || !doctorPathWithin(grokSource, hook.Source.Path) {
			continue
		}
		for _, event := range expected {
			if hooks.GrokTargetHasRuntimeEvent(hook.Target, event) {
				seen[event] = true
			}
		}
	}
	missing := []string{}
	for _, event := range expected {
		if !seen[event] {
			missing = append(missing, event)
		}
	}
	if len(missing) > 0 {
		check.Status = doctorStatusWarn
		check.Detail = "Grok did not load native Reconc routes: " + strings.Join(missing, ", ") + "; reload `/hooks` and verify project trust"
		return check
	}
	version := strings.TrimSpace(inspection.GrokVersion)
	displayVersion := version
	if displayVersion == "" {
		displayVersion = "unknown version"
	}
	capability := doctorProbeGrokNativeStop()
	if !capability.Supported {
		check.Status = doctorStatusWarn
		check.Detail = fmt.Sprintf("Grok %s loaded all %d native Reconc routes, but %s; guarded PreToolUse gates remain hard and optional leader steering supplies the strict Stop fallback", displayVersion, len(expected), capability.Detail)
		return check
	}
	check.Detail = fmt.Sprintf("Grok %s loaded all %d native Reconc routes from .grok/hooks/reconc.json; native no-leader Stop enforcement is active and capability-probed from %s", displayVersion, len(expected), capability.DocumentationPath)
	return check
}

func doctorPathWithin(root, candidate string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(candidate))
	if cleaned == "." || cleaned == "" {
		return false
	}
	rel, err := filepath.Rel(root, cleaned)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

var doctorProbeGrokLeader = grokacp.ProbeLeaderSteering

func doctorCheckGrokLeaderSteering(discovery ingest.DiscoveryResult) doctorCheck {
	check := doctorCheck{
		Name:   "Grok leader steering",
		Status: doctorStatusOK,
	}
	if !discovery.Discovered {
		check.Detail = "cannot probe Grok leader without a discovered reconc repo"
		return check
	}
	if _, err := os.Stat(filepath.Join(discovery.RepoRoot, filepath.FromSlash(hooks.GrokHooksPath))); err != nil {
		check.Detail = "native Grok hook not installed; steering not applicable"
		return check
	}
	if grokacp.SteeringDisabled() {
		check.Detail = "optional leader steering disabled via " + grokacp.SteerEnv + "; native Stop capability is reported separately"
		return check
	}
	probe := doctorProbeGrokLeader(2 * time.Second)
	switch {
	case probe.Endpoint == "" && probe.Detail != "":
		check.Status = doctorStatusWarn
		check.Detail = "Grok leader discovery failed: " + probe.Detail
	case probe.Endpoint == "":
		check.Detail = "no Grok leader endpoint; optional backward-compatible steering is inactive and native Stop capability is reported separately"
	case probe.Compatible:
		version := "unknown"
		if probe.ProtocolVersion != nil {
			version = fmt.Sprintf("%d", *probe.ProtocolVersion)
		}
		binary := strings.TrimSpace(probe.BinaryVersion)
		if binary == "" {
			binary = "unknown"
		}
		if doctorProbeGrokNativeStop().Supported {
			check.Detail = fmt.Sprintf("Grok leader compatible at %s (protocol %s, binary %s); native Stop is active and duplicate leader interjection is suppressed", probe.Endpoint, version, binary)
		} else {
			check.Detail = fmt.Sprintf("Grok leader compatible at %s (protocol %s, binary %s); backward-compatible TUI Stop steering is active", probe.Endpoint, version, binary)
		}
	case probe.Reachable:
		check.Status = doctorStatusWarn
		check.Detail = "Grok leader " + probe.Endpoint + " reachable but incompatible: " + probe.Detail
	default:
		check.Status = doctorStatusWarn
		check.Detail = "Grok leader endpoint " + probe.Endpoint + " present but handshake failed: " + probe.Detail
	}
	return check
}

func doctorCheckHookRuntimeCompatibility(discovery ingest.DiscoveryResult) doctorCheck {
	result := inspectHookRuntimeCompatibility(discovery)
	return doctorCheck{
		Name:   "hook runtime compatibility",
		Status: result.Status,
		Detail: result.Detail,
	}
}

func doctorCheckLockfileFreshness(discovery ingest.DiscoveryResult) doctorCheck {
	check := doctorCheck{
		Name:   "lockfile freshness",
		Status: doctorStatusFail,
	}
	if !discovery.Discovered {
		check.Detail = firstDiscoveryWarning(discovery, "no reconc policy markers discovered")
		return check
	}
	if err := runtime.ValidatePolicyLockfile(discovery.RepoRoot); err != nil {
		check.Detail = err.Error()
		return check
	}

	check.Status = doctorStatusOK
	check.Detail = "compiled lockfile matches current policy sources"
	return check
}

func doctorCheckAuditSize(discovery ingest.DiscoveryResult) doctorCheck {
	check := doctorCheck{
		Name:   "audit log size",
		Status: doctorStatusOK,
		Detail: "audit log absent",
	}
	if !discovery.Discovered {
		check.Status = doctorStatusWarn
		check.Detail = "cannot inspect audit log without a discovered reconc repo"
		return check
	}

	path := filepath.Join(discovery.RepoRoot, audit.AuditFileRelative)
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return check
		}
		check.Status = doctorStatusWarn
		check.Detail = "cannot stat audit log: " + err.Error()
		return check
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		check.Status = doctorStatusWarn
		check.Detail = "audit log must be a non-symlink regular file"
		return check
	}
	// Measure the whole ring: live file plus rotation archives.
	total := info.Size()
	for index := 1; index <= audit.MaxArchiveFiles; index++ {
		archivePath := fmt.Sprintf("%s.%d", path, index)
		if archiveInfo, archiveErr := os.Lstat(archivePath); archiveErr == nil {
			if archiveInfo.Mode()&os.ModeSymlink != 0 || !archiveInfo.Mode().IsRegular() {
				check.Status = doctorStatusWarn
				check.Detail = fmt.Sprintf("audit archive %s must be a non-symlink regular file", filepath.Base(archivePath))
				return check
			}
			total += archiveInfo.Size()
		} else if !os.IsNotExist(archiveErr) {
			check.Status = doctorStatusWarn
			check.Detail = "cannot inspect audit archive: " + archiveErr.Error()
			return check
		}
	}
	if total > doctorAuditWarnBytes {
		check.Status = doctorStatusWarn
		check.Detail = fmt.Sprintf("%s ring is %.1f MiB (> %.0f MiB ceiling); rotation appears broken - inspect the live file and archives", audit.AuditFileRelative, float64(total)/1024.0/1024.0, float64(doctorAuditWarnBytes)/1024.0/1024.0)
		return check
	}
	check.Detail = fmt.Sprintf("%s ring is %.1f KiB (live + %d archives)", audit.AuditFileRelative, float64(total)/1024.0, audit.MaxArchiveFiles)
	return check
}

func doctorCheckUnknownRefs(discovery ingest.DiscoveryResult) doctorCheck {
	check := doctorCheck{
		Name:   "preset/template references",
		Status: doctorStatusFail,
	}
	if !discovery.Discovered {
		check.Detail = firstDiscoveryWarning(discovery, "no reconc policy markers discovered")
		return check
	}

	presetRefs, templateRefs, err := collectDoctorRefs(discovery)
	if err != nil {
		check.Detail = err.Error()
		return check
	}

	unknownPresets := make([]string, 0)
	for _, name := range presetRefs {
		if _, err := presets.Load(name); err != nil {
			unknownPresets = append(unknownPresets, name)
		}
	}
	unknownTemplates := make([]string, 0)
	for _, name := range templateRefs {
		if _, err := templates.Resolve(name); err != nil {
			unknownTemplates = append(unknownTemplates, name)
		}
	}

	if len(unknownPresets) == 0 && len(unknownTemplates) == 0 {
		check.Status = doctorStatusOK
		check.Detail = fmt.Sprintf("resolved %d preset ref(s) and %d template ref(s)", len(presetRefs), len(templateRefs))
		return check
	}

	parts := make([]string, 0, 2)
	if len(unknownPresets) > 0 {
		parts = append(parts, "unknown presets: "+strings.Join(unknownPresets, ", "))
	}
	if len(unknownTemplates) > 0 {
		parts = append(parts, "unknown templates: "+strings.Join(unknownTemplates, ", "))
	}
	check.Detail = strings.Join(parts, "; ")
	return check
}

func doctorCheckSessionClaims(discovery ingest.DiscoveryResult) doctorCheck {
	check := doctorCheck{
		Name:   "session claim age",
		Status: doctorStatusOK,
		Detail: "no active session claims",
	}
	if !discovery.Discovered {
		check.Status = doctorStatusWarn
		check.Detail = "cannot inspect session claims without a discovered reconc repo"
		return check
	}

	sessionID, err := agentsession.ResolveActiveSessionID(discovery.RepoRoot)
	if err != nil {
		check.Status = doctorStatusWarn
		check.Detail = "cannot resolve active session: " + err.Error()
		return check
	}
	if sessionID == "" {
		return check
	}

	state, err := agentsession.LoadSessionState(discovery.RepoRoot, sessionID)
	if err != nil {
		check.Status = doctorStatusWarn
		check.Detail = "cannot load active session state: " + err.Error()
		return check
	}
	evidence, err := agentsession.ActiveEvidence(discovery.RepoRoot)
	if err != nil {
		check.Status = doctorStatusWarn
		check.Detail = "cannot load complete active-session evidence: " + err.Error()
		return check
	}
	if len(evidence.Claims) == 0 {
		check.Detail = "active session has no recorded claims"
		return check
	}

	statePath := deriveSessionStatePath(state.ReportPath)
	info, err := os.Stat(statePath)
	if err != nil {
		check.Status = doctorStatusWarn
		check.Detail = "claim timestamps unavailable: cannot stat session state; current schema stores claims without per-claim timestamps"
		return check
	}
	age := time.Since(info.ModTime())
	if age > 24*time.Hour {
		check.Status = doctorStatusWarn
		check.Detail = fmt.Sprintf("%d claim(s) in active session; last session update %s ago (session-level heuristic only)", len(state.Claims), age.Round(time.Hour))
		return check
	}
	check.Detail = fmt.Sprintf("%d claim(s) in active session; last session update %s ago", len(state.Claims), age.Round(time.Minute))
	return check
}

func doctorCheckConflictCount(discovery ingest.DiscoveryResult) doctorCheck {
	check := doctorCheck{
		Name:   "rule conflicts",
		Status: doctorStatusFail,
	}
	if !discovery.Discovered {
		check.Detail = firstDiscoveryWarning(discovery, "no reconc policy markers discovered")
		return check
	}

	bundle, err := ingest.LoadPolicySources(discovery.RepoRoot)
	if err != nil {
		check.Detail = err.Error()
		return check
	}
	parsed, err := parser.ParseRuleDocuments(bundle)
	if err != nil {
		check.Detail = err.Error()
		return check
	}
	conflicts := compiler.DetectConflicts(parsed.Rules)
	if len(conflicts) == 0 {
		check.Status = doctorStatusOK
		check.Detail = "no static rule conflicts detected"
		return check
	}
	check.Status = doctorStatusWarn
	check.Detail = fmt.Sprintf("%d static rule conflict(s) detected", len(conflicts))
	return check
}

func collectDoctorRefs(discovery ingest.DiscoveryResult) ([]string, []string, error) {
	presetSet := map[string]struct{}{}
	templateSet := map[string]struct{}{}
	totalBytes := int64(0)
	if discovery.ConfigPath != nil {
		body, err := readDoctorTemplateSource(discovery.RepoRoot, *discovery.ConfigPath, &totalBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("read compiler config: %w", err)
		}
		names, err := extractPresetRefs(string(body), *discovery.ConfigPath)
		if err != nil {
			return nil, nil, err
		}
		for _, name := range names {
			presetSet[name] = struct{}{}
		}
	}
	sources, err := loadDoctorTemplateSources(discovery, &totalBytes)
	if err != nil {
		return nil, nil, err
	}
	for _, source := range sources {
		names, err := extractTemplateRefs(source.content, source.label)
		if err != nil {
			return nil, nil, err
		}
		for _, name := range names {
			templateSet[name] = struct{}{}
		}
	}

	return sortedStringSet(presetSet), sortedStringSet(templateSet), nil
}

type doctorTemplateSource struct {
	label   string
	content string
}

func loadDoctorTemplateSources(discovery ingest.DiscoveryResult, totalBytes *int64) ([]doctorTemplateSource, error) {
	out := []doctorTemplateSource{}
	for _, entry := range []struct {
		path *string
		md   bool
	}{
		{path: discovery.AgentsPath, md: true},
		{path: discovery.ClaudePath, md: true},
		{path: discovery.StartMDPath, md: true},
		{path: discovery.ConfigPath, md: false},
	} {
		if entry.path == nil {
			continue
		}
		body, err := readDoctorTemplateSource(discovery.RepoRoot, *entry.path, totalBytes)
		if err != nil {
			return nil, err
		}
		text := string(body)
		if entry.md {
			for _, block := range extractDoctorInlineBlocks(text) {
				out = append(out, doctorTemplateSource{label: *entry.path + " inline block", content: block})
			}
			continue
		}
		out = append(out, doctorTemplateSource{label: *entry.path, content: text})
	}
	for _, relative := range discovery.PolicyPaths {
		body, err := readDoctorTemplateSource(discovery.RepoRoot, relative, totalBytes)
		if err != nil {
			return nil, err
		}
		out = append(out, doctorTemplateSource{label: relative, content: string(body)})
	}
	return out, nil
}

func readDoctorTemplateSource(root, relative string, totalBytes *int64) ([]byte, error) {
	body, err := boundedio.ReadRegularFile(filepath.Join(root, relative), doctorSourceMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", relative, err)
	}
	*totalBytes += int64(len(body))
	if *totalBytes > doctorSourceAggregateMax {
		return nil, fmt.Errorf("doctor template source aggregate exceeds %d bytes", doctorSourceAggregateMax)
	}
	return body, nil
}

func extractDoctorInlineBlocks(text string) []string {
	matches := doctorInlineBlockRegex.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		out = append(out, strings.TrimSpace(match[1]))
	}
	return out
}

func extractPresetRefs(raw, context string) ([]string, error) {
	doc, err := decodeDoctorYAML(raw, context)
	if err != nil {
		return nil, err
	}
	root, ok := doc.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("%s must be a YAML mapping", context)
	}
	rawExtends, ok := root["extends"]
	if !ok || rawExtends == nil {
		return nil, nil
	}
	list, ok := rawExtends.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%s: extends must be a list of preset names", context)
	}
	set := map[string]struct{}{}
	for i, item := range list {
		name, ok := item.(string)
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("%s: extends[%d] must be a non-empty string", context, i)
		}
		cleaned := strings.TrimSpace(name)
		if strings.HasPrefix(cleaned, "preset:") {
			cleaned = strings.TrimSpace(strings.TrimPrefix(cleaned, "preset:"))
		}
		if cleaned == "" {
			return nil, fmt.Errorf("%s: extends[%d] is missing a preset name", context, i)
		}
		set[cleaned] = struct{}{}
	}
	return sortedStringSet(set), nil
}

func extractTemplateRefs(raw, context string) ([]string, error) {
	doc, err := decodeDoctorYAML(raw, context)
	if err != nil {
		return nil, err
	}
	set := map[string]struct{}{}
	collectTemplateRefsRecursive(doc, set)
	return sortedStringSet(set), nil
}

func decodeDoctorYAML(raw, context string) (interface{}, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]interface{}{}, nil
	}
	var doc interface{}
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("invalid YAML in %s: %w", context, err)
	}
	return normalizeDoctorValue(doc), nil
}

func normalizeDoctorValue(v interface{}) interface{} {
	switch typed := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, value := range typed {
			out[key] = normalizeDoctorValue(value)
		}
		return out
	case map[interface{}]interface{}:
		out := map[string]interface{}{}
		for key, value := range typed {
			name, ok := key.(string)
			if !ok {
				continue
			}
			out[name] = normalizeDoctorValue(value)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, value := range typed {
			out[i] = normalizeDoctorValue(value)
		}
		return out
	default:
		return v
	}
}

func collectTemplateRefsRecursive(node interface{}, out map[string]struct{}) {
	switch typed := node.(type) {
	case map[string]interface{}:
		if value, ok := typed["template"].(string); ok && strings.TrimSpace(value) != "" {
			out[strings.TrimSpace(value)] = struct{}{}
		}
		for _, value := range typed {
			collectTemplateRefsRecursive(value, out)
		}
	case []interface{}:
		for _, value := range typed {
			collectTemplateRefsRecursive(value, out)
		}
	}
}

func sortedStringSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func deriveSessionStatePath(reportPath string) string {
	projectDir := filepath.Dir(filepath.Dir(reportPath))
	return filepath.Join(projectDir, "sessions", filepath.Base(reportPath))
}

func firstDiscoveryWarning(discovery ingest.DiscoveryResult, fallback string) string {
	if len(discovery.Warnings) > 0 {
		return discovery.Warnings[0]
	}
	return fallback
}

func renderDoctorDeepText(report *doctorDeepReport, w io.Writer) {
	style := newTextStyler(w)
	fmt.Fprintln(w, "reconc doctor --deep")
	fmt.Fprintf(w, "  repo root:  %s\n", report.RepoRoot)
	for _, check := range report.Checks {
		fmt.Fprintf(w, "[%s] %-28s %s\n", style.statusTag(check.Status, 4), check.Name, check.Detail)
	}
}
