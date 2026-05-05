package cli

import (
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
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/presets"
	"reconc.dev/reconc/internal/runtime/agentsession"
	"reconc.dev/reconc/internal/templates"
)

const (
	doctorStatusOK   = "OK"
	doctorStatusWarn = "WARN"
	doctorStatusFail = "FAIL"

	doctorAuditWarnBytes = 10 * 1024 * 1024
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
			doctorCheckLockfileFreshness(discovery),
			doctorCheckAuditSize(discovery),
			doctorCheckUnknownRefs(discovery),
			doctorCheckSessionClaims(discovery),
			doctorCheckConflictCount(discovery),
		},
	}
	return report, nil
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
	if discovery.LockfilePath == nil {
		check.Detail = "compiled lockfile missing; run `reconc compile`"
		return check
	}

	payload, err := readDoctorLockfile(discovery.RepoRoot)
	if err != nil {
		check.Detail = err.Error()
		return check
	}
	if err := validateLockfileRepoRoot(discovery.RepoRoot, payload); err != nil {
		check.Detail = err.Error()
		return check
	}
	stored, _ := payload["source_digest"].(string)
	if len(stored) != 64 {
		check.Detail = "compiled lockfile source_digest is missing or invalid; run `reconc compile`"
		return check
	}

	bundle, err := ingest.LoadPolicySources(discovery.RepoRoot)
	if err != nil {
		check.Detail = err.Error()
		return check
	}
	current := compiler.ComputeSourceDigest(bundle)
	if stored != current {
		check.Detail = fmt.Sprintf("source_digest mismatch: lock=%s current=%s; run `reconc compile`", short12(stored), short12(current))
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
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return check
		}
		check.Status = doctorStatusWarn
		check.Detail = "cannot stat audit log: " + err.Error()
		return check
	}
	if info.Size() > doctorAuditWarnBytes {
		check.Status = doctorStatusWarn
		check.Detail = fmt.Sprintf("%s is %.1f MiB (>10 MiB); rotate or archive old audit data", audit.AuditFileRelative, float64(info.Size())/1024.0/1024.0)
		return check
	}
	check.Detail = fmt.Sprintf("%s is %.1f KiB", audit.AuditFileRelative, float64(info.Size())/1024.0)
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
	if len(state.Claims) == 0 {
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

func readDoctorLockfile(repoRoot string) (map[string]interface{}, error) {
	path := filepath.Join(repoRoot, ingest.LockfilePath)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read lockfile: %w", err)
	}
	var payload map[string]interface{}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("lockfile is not valid JSON: %w", err)
	}
	return payload, nil
}

func collectDoctorRefs(discovery ingest.DiscoveryResult) ([]string, []string, error) {
	presetSet := map[string]struct{}{}
	templateSet := map[string]struct{}{}

	if discovery.ConfigPath != nil {
		configText, err := os.ReadFile(filepath.Join(discovery.RepoRoot, *discovery.ConfigPath))
		if err != nil {
			return nil, nil, fmt.Errorf("read compiler config: %w", err)
		}
		presets, err := extractPresetRefs(string(configText), *discovery.ConfigPath)
		if err != nil {
			return nil, nil, err
		}
		for _, name := range presets {
			presetSet[name] = struct{}{}
		}
	}

	sources, err := loadDoctorTemplateSources(discovery)
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

func loadDoctorTemplateSources(discovery ingest.DiscoveryResult) ([]doctorTemplateSource, error) {
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
		full := filepath.Join(discovery.RepoRoot, *entry.path)
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", *entry.path, err)
		}
		text := string(data)
		if entry.md {
			for _, block := range extractDoctorInlineBlocks(text) {
				out = append(out, doctorTemplateSource{
					label:   *entry.path + " inline block",
					content: block,
				})
			}
			continue
		}
		out = append(out, doctorTemplateSource{
			label:   *entry.path,
			content: text,
		})
	}

	for _, rel := range discovery.PolicyPaths {
		full := filepath.Join(discovery.RepoRoot, rel)
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		out = append(out, doctorTemplateSource{
			label:   rel,
			content: string(data),
		})
	}
	return out, nil
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
	fmt.Fprintln(w, "reconc doctor --deep")
	fmt.Fprintf(w, "  repo root:  %s\n", report.RepoRoot)
	for _, check := range report.Checks {
		fmt.Fprintf(w, "[%-4s] %-28s %s\n", check.Status, check.Name, check.Detail)
	}
}
