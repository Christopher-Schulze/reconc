package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
)

const maxExplainReportBytes = 32 << 20

// runExplain implements `reconc explain [repo] (evidence flags...) | --report-file PATH
// [--format text|markdown] [--json]`.
//
// Two input modes:
//   - Fresh evidence: same flags as check; runs check and renders
//   - Saved report: --report-file PATH loads a previously-written
//     JSON report and renders it without re-running evaluation
//
// Output format defaults to text; --format markdown gives a more
// structured rendering suitable for PRs / issue bodies / docs.
//
// Always exits 0 (it's a renderer, not an enforcement command).
func runExplain(args []string, stdout, stderr io.Writer) (resultErr error) {
	repo := "."
	repoSet := false
	jsonOut := false
	outputPath := ""
	reportFile := ""
	format := "text"
	inputs := runtime.Empty()

	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc explain: --output requires a path"}
			}
			outputPath = val
		case "--report-file":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc explain: --report-file requires a path"}
			}
			reportFile = val
		case "--format":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc explain: --format requires a value (text or markdown)"}
			}
			if val != "text" && val != "markdown" {
				return &CLIError{ExitCode: 1, Message: "reconc explain: --format must be text or markdown"}
			}
			format = val
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc explain [repo] [--read PATH] [--write PATH] [--command CMD]")
			fmt.Fprintln(stdout, "                         [--claim NAME] [--format text|markdown] [--json] [--output PATH]")
			fmt.Fprintln(stdout, "       reconc explain --report-file PATH [--format text|markdown] [--json] [--output PATH]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Render a policy check report in human-readable form. Source can be fresh")
			fmt.Fprintln(stdout, "evidence (same flags as `reconc check`) or a previously-saved JSON report.")
			fmt.Fprintln(stdout, "Always exits 0 (renderer, not enforcement).")
			return nil
		case "--read":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc explain: --read requires a value"}
			}
			inputs.ReadPaths = append(inputs.ReadPaths, val)
		case "--write":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc explain: --write requires a value"}
			}
			inputs.WritePaths = append(inputs.WritePaths, val)
		case "--command":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc explain: --command requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
		case "--claim":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc explain: --claim requires a value"}
			}
			inputs.Claims = append(inputs.Claims, val)
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc explain: unknown flag %q", a)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc explain: expected at most one repo path"}
			}
			repo = a
			repoSet = true
		}
		i++
	}

	var report *runtime.CheckReport
	if reportFile != "" {
		data, err := boundedio.ReadRegularFile(reportFile, maxExplainReportBytes)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc explain: read report file: " + err.Error()}
		}
		var loaded runtime.CheckReport
		if err := json.Unmarshal(data, &loaded); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc explain: report file is not valid JSON: " + err.Error()}
		}
		report = &loaded
	} else {
		r, err := runtime.CheckRepoPolicy(repo, inputs)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc explain: " + err.Error()}
		}
		report = r
	}

	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc explain: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)

	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		return nil
	}

	switch format {
	case "markdown":
		fmt.Fprint(out, runtime.RenderCheckReportMarkdown(report))
	default:
		renderCheckText(report, out)
	}
	return nil
}

// runWhy implements `reconc why <rule-id|action|mcp> [repo] [--json]` (W13).
//
// Prints everything known about a rule: kind, mode, message, all
// targeting fields, source provenance. Useful when a violation
// surfaces a rule id and the agent needs context.
func runWhy(args []string, stdout, stderr io.Writer) error {
	// Handle --help before requiring a rule-id so `reconc why --help` works.
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc why <rule-id|action|mcp> [repo] [--json|--terse]")
			fmt.Fprintln(stdout, "Show one repository rule, the canonical action plan, or its MCP compatibility view.")
			return nil
		}
	}
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc why: missing required <rule-id|action|mcp> argument"}
	}
	ruleID := args[0]
	repo := "."
	repoSet := false
	jsonOut := false
	terse := false
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--terse":
			terse = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc why <rule-id|action|mcp> [repo] [--json|--terse]")
			fmt.Fprintln(stdout, "Show one repository rule, the canonical action plan, or its MCP compatibility view.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc why: unknown flag %q", a)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc why: expected at most one repo path"}
			}
			repo = a
			repoSet = true
		}
	}

	discovery, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc why: " + err.Error()}
	}
	if !discovery.Discovered {
		return &CLIError{ExitCode: 1, Message: "reconc why: no policy markers discovered"}
	}
	if err := runtime.ValidatePolicyLockfile(discovery.RepoRoot); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc why: " + err.Error()}
	}
	if jsonOut && terse {
		return &CLIError{ExitCode: 1, Message: "reconc why: --json and --terse are mutually exclusive"}
	}
	if ruleID == "action" {
		plan, loadErr := runtime.LoadActionPlan(discovery.RepoRoot)
		if loadErr != nil {
			return &CLIError{ExitCode: 1, Message: "reconc why: " + loadErr.Error()}
		}
		return renderWhyAction(plan, jsonOut, terse, stdout)
	}
	if ruleID == "mcp" {
		contract, loadErr := runtime.LoadMCPPolicy(discovery.RepoRoot)
		if loadErr != nil {
			return &CLIError{ExitCode: 1, Message: "reconc why: " + loadErr.Error()}
		}
		return renderWhyMCP(contract, jsonOut, terse, stdout)
	}

	lockPath := filepath.Join(discovery.RepoRoot, ingest.LockfilePath)
	data, err := boundedio.ReadRegularFile(lockPath, maxCLILockfileBytes)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc why: read lockfile: " + err.Error()}
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc why: lockfile is not valid JSON: " + err.Error()}
	}
	rules, _ := payload["rules"].([]interface{})
	var target map[string]interface{}
	for _, r := range rules {
		m, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		if id, _ := m["id"].(string); id == ruleID {
			target = m
			break
		}
	}
	if target == nil {
		return &CLIError{ExitCode: 1, Message: "reconc why: rule '" + ruleID + "' not found in lockfile"}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(target)
		return nil
	}
	if terse {
		mode := strOrEmpty(target["mode"])
		if mode == "" {
			mode = "(default)"
		}
		path := firstRulePath(target)
		if path == "" {
			path = "-"
		}
		fmt.Fprintf(stdout, "kind=%s mode=%s path=%s msg=%s\n",
			strOrEmpty(target["kind"]),
			mode,
			path,
			truncateMessageLines(strOrEmpty(target["message"]), 4))
		return nil
	}

	// Pretty text rendering of the rule.
	fmt.Fprintf(stdout, "Rule:    %s\n", strOrEmpty(target["id"]))
	fmt.Fprintf(stdout, "Kind:    %s\n", strOrEmpty(target["kind"]))
	if mode, ok := target["mode"].(string); ok && mode != "" {
		fmt.Fprintf(stdout, "Mode:    %s\n", mode)
	} else {
		fmt.Fprintf(stdout, "Mode:    (default)\n")
	}
	fmt.Fprintf(stdout, "Source:  %s\n", strOrEmpty(target["source_path"]))
	if blockID, ok := target["source_block_id"].(string); ok && blockID != "" {
		fmt.Fprintf(stdout, "Block:   %s\n", blockID)
	}
	fmt.Fprintf(stdout, "Message: %s\n", strOrEmpty(target["message"]))
	// W31: surface deprecation loud and early, right after Message.
	if dep, ok := target["deprecated"].(bool); ok && dep {
		line := "DEPRECATED"
		if since, ok := target["deprecated_since"].(string); ok && since != "" {
			line += " (since " + since + ")"
		}
		if rep, ok := target["deprecated_replaced_by"].(string); ok && rep != "" {
			line += "; replaced by '" + rep + "'"
		}
		if reason, ok := target["deprecated_reason"].(string); ok && reason != "" {
			line += ": " + reason
		}
		fmt.Fprintf(stdout, "Status:  %s\n", line)
	}
	for _, key := range []string{"paths", "before_paths", "when_paths", "commands", "claims"} {
		if list, ok := target[key].([]interface{}); ok && len(list) > 0 {
			items := []string{}
			for _, x := range list {
				if s, ok := x.(string); ok {
					items = append(items, s)
				}
			}
			fmt.Fprintf(stdout, "%-9s %s\n", key+":", joinList(items))
		}
	}
	if rf, ok := target["required_files"].([]interface{}); ok && len(rf) > 0 {
		fmt.Fprintf(stdout, "required_files:\n")
		for _, e := range rf {
			if m, ok := e.(map[string]interface{}); ok {
				fmt.Fprintf(stdout, "  - path: %s, max_age_hours: %v\n", m["path"], m["max_age_hours"])
			}
		}
	}
	if ev, ok := target["evidence"].([]interface{}); ok && len(ev) > 0 {
		fmt.Fprintf(stdout, "evidence:\n")
		for _, e := range ev {
			if m, ok := e.(map[string]interface{}); ok {
				fmt.Fprintf(stdout, "  - file: %s\n", m["file"])
			}
		}
	}
	if ck, ok := target["checks"].([]interface{}); ok && len(ck) > 0 {
		fmt.Fprintf(stdout, "checks (%d sub-checks):\n", len(ck))
		for i, e := range ck {
			if m, ok := e.(map[string]interface{}); ok {
				fmt.Fprintf(stdout, "  %d. kind=%s\n", i+1, m["kind"])
			}
		}
	}
	if script, ok := target["script"].(string); ok && script != "" {
		fmt.Fprintf(stdout, "Script:  %s\n", script)
	}
	return nil
}

type whyActionPlan struct {
	FormatVersion string          `json:"format_version"`
	Defaults      action.Defaults `json:"defaults"`
	Tools         []action.Tool   `json:"tools"`
	Rules         []whyActionRule `json:"rules"`
}

type whyActionRule struct {
	ID              string              `json:"id"`
	Selector        action.Selector     `json:"selector"`
	When            *whyActionCondition `json:"when,omitempty"`
	Decision        action.Decision     `json:"decision"`
	OnIndeterminate action.Decision     `json:"on_indeterminate"`
	Cache           action.CachePolicy  `json:"cache"`
	Message         string              `json:"message,omitempty"`
	SourceIdentity  string              `json:"source_identity"`
}

type whyActionCondition struct {
	All       []whyActionCondition `json:"all,omitempty"`
	Any       []whyActionCondition `json:"any,omitempty"`
	Not       *whyActionCondition  `json:"not,omitempty"`
	Predicate *whyActionPredicate  `json:"predicate,omitempty"`
}

type whyActionPredicate struct {
	Source            action.ValueSource `json:"source"`
	Pointer           string             `json:"pointer"`
	MinimumProvenance action.Provenance  `json:"minimum_provenance,omitempty"`
	Op                action.Operator    `json:"op"`
	Value             *whyActionOperand  `json:"value,omitempty"`
}

type whyActionOperand struct {
	Redacted       bool             `json:"redacted"`
	Kind           action.ValueKind `json:"kind"`
	CanonicalBytes int              `json:"canonical_bytes"`
}

func renderWhyAction(plan action.Plan, jsonOut, terse bool, stdout io.Writer) error {
	view := whyActionPlan{
		FormatVersion: plan.FormatVersion,
		Defaults:      plan.Defaults,
		Tools:         make([]action.Tool, len(plan.Tools)),
		Rules:         make([]whyActionRule, len(plan.Rules)),
	}
	copy(view.Tools, plan.Tools)
	for index, rule := range plan.Rules {
		condition, err := summarizeActionCondition(rule.When)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc why: summarize action operand: " + err.Error()}
		}
		view.Rules[index] = whyActionRule{
			ID: rule.ID, Selector: rule.Selector, When: condition,
			Decision: rule.Decision, OnIndeterminate: rule.OnIndeterminate,
			Cache: rule.Cache, Message: rule.Message, SourceIdentity: rule.SourceIdentity,
		}
	}
	if jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(view)
		return nil
	}
	if terse {
		fmt.Fprintf(stdout, "format=%s tools=%d rules=%d declared=%s gateway-unmatched=%s host-unmatched=%s\n",
			view.FormatVersion, len(view.Tools), len(view.Rules), view.Defaults.DeclaredTool,
			view.Defaults.GatewayUnmatched, view.Defaults.HostUnmatched)
		return nil
	}
	fmt.Fprintf(stdout, "Action plan: %s\n", view.FormatVersion)
	fmt.Fprintf(stdout, "Defaults: declared=%s gateway-unmatched=%s host-unmatched=%s evaluation-error=%s post-error=%s progress-error=%s cache=%s\n",
		view.Defaults.DeclaredTool, view.Defaults.GatewayUnmatched, view.Defaults.HostUnmatched,
		view.Defaults.EvaluationError, view.Defaults.PostError, view.Defaults.ProgressError, view.Defaults.Cache)
	fmt.Fprintf(stdout, "Tools: %d\n", len(view.Tools))
	for _, tool := range view.Tools {
		identity := string(tool.Platform)
		if identity == "" {
			identity = tool.ServerLabel
		}
		if tool.ServerFingerprint != "" {
			identity += "@" + tool.ServerFingerprint
		}
		fmt.Fprintf(stdout, "  - %s %s:%s -> %s origin=%s source=%s\n",
			tool.ID, identity, tool.Tool, tool.Effect.Kind, tool.Origin, tool.SourceIdentity)
	}
	fmt.Fprintf(stdout, "Rules: %d\n", len(view.Rules))
	for _, rule := range view.Rules {
		fmt.Fprintf(stdout, "  - %s -> %s indeterminate=%s cache=%s selector=%s source=%s",
			rule.ID, rule.Decision, rule.OnIndeterminate, rule.Cache,
			summarizeActionSelector(rule.Selector), rule.SourceIdentity)
		if rule.When != nil {
			fmt.Fprintf(stdout, " when=%s", summarizeConditionText(rule.When))
		}
		fmt.Fprintln(stdout)
	}
	return nil
}

func summarizeActionSelector(selector action.Selector) string {
	parts := make([]string, 0, 8)
	appendValues := func(name string, values []string) {
		if len(values) > 0 {
			quoted := make([]string, len(values))
			for index := range values {
				quoted[index] = strconv.Quote(values[index])
			}
			parts = append(parts, name+"="+strings.Join(quoted, ","))
		}
	}
	appendValues("tool_ids", selector.ToolIDs)
	transports := make([]string, len(selector.Transports))
	for index := range selector.Transports {
		transports[index] = string(selector.Transports[index])
	}
	appendValues("transports", transports)
	platforms := make([]string, len(selector.Platforms))
	for index := range selector.Platforms {
		platforms[index] = string(selector.Platforms[index])
	}
	appendValues("platforms", platforms)
	appendValues("server_labels", selector.ServerLabels)
	appendValues("server_fingerprints", selector.ServerFingerprints)
	appendValues("tools", selector.Tools)
	appendValues("tool_contract_digests", selector.ToolContractDigests)
	phases := make([]string, len(selector.Phases))
	for index := range selector.Phases {
		phases[index] = string(selector.Phases[index])
	}
	appendValues("phases", phases)
	if len(parts) == 0 {
		return "*"
	}
	return strings.Join(parts, ";")
}

func summarizeActionCondition(condition *action.Condition) (*whyActionCondition, error) {
	if condition == nil {
		return nil, nil
	}
	view := &whyActionCondition{}
	if condition.All != nil {
		view.All = make([]whyActionCondition, len(condition.All))
		for index := range condition.All {
			child, err := summarizeActionCondition(&condition.All[index])
			if err != nil {
				return nil, err
			}
			view.All[index] = *child
		}
	}
	if condition.Any != nil {
		view.Any = make([]whyActionCondition, len(condition.Any))
		for index := range condition.Any {
			child, err := summarizeActionCondition(&condition.Any[index])
			if err != nil {
				return nil, err
			}
			view.Any[index] = *child
		}
	}
	if condition.Not != nil {
		child, err := summarizeActionCondition(condition.Not)
		if err != nil {
			return nil, err
		}
		view.Not = child
	}
	if condition.Predicate != nil {
		predicate := condition.Predicate
		view.Predicate = &whyActionPredicate{
			Source: predicate.Source, Pointer: predicate.Pointer,
			MinimumProvenance: predicate.MinimumProvenance, Op: predicate.Op,
		}
		if predicate.Value != nil {
			canonical, err := predicate.Value.MarshalJSON()
			if err != nil {
				return nil, err
			}
			view.Predicate.Value = &whyActionOperand{Redacted: true, Kind: predicate.Value.Kind(), CanonicalBytes: len(canonical)}
		}
	}
	return view, nil
}

func summarizeConditionText(condition *whyActionCondition) string {
	switch {
	case condition == nil:
		return "true"
	case condition.Predicate != nil:
		predicate := condition.Predicate
		value := ""
		if predicate.Value != nil {
			value = fmt.Sprintf(" value=%s/%dB/redacted", predicate.Value.Kind, predicate.Value.CanonicalBytes)
		}
		provenance := ""
		if predicate.MinimumProvenance != "" {
			provenance = " provenance=" + string(predicate.MinimumProvenance)
		}
		return fmt.Sprintf("%s:%s %s%s%s", predicate.Source, predicate.Pointer, predicate.Op, provenance, value)
	case condition.All != nil:
		return fmt.Sprintf("all(%d)", len(condition.All))
	case condition.Any != nil:
		return fmt.Sprintf("any(%d)", len(condition.Any))
	case condition.Not != nil:
		return "not(" + summarizeConditionText(condition.Not) + ")"
	default:
		return "invalid"
	}
}

func renderWhyMCP(contract *policy.MCPPolicy, jsonOut, terse bool, stdout io.Writer) error {
	if jsonOut && terse {
		return &CLIError{ExitCode: 1, Message: "reconc why: --json and --terse are mutually exclusive"}
	}
	if contract == nil {
		if jsonOut {
			_, _ = fmt.Fprintln(stdout, "null")
		} else {
			_, _ = fmt.Fprintln(stdout, "MCP policy: not configured; host behavior remains in control and no MCP call is classified as repository evidence.")
		}
		return nil
	}
	if err := contract.Validate(); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc why: compiled MCP compatibility view is invalid: " + err.Error()}
	}
	if jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(contract)
		return nil
	}
	if terse {
		fmt.Fprintf(stdout, "unclassified=%s mappings=%d\n", contract.Unclassified, len(contract.Tools))
		return nil
	}
	fmt.Fprintf(stdout, "MCP unclassified: %s\n", contract.Unclassified)
	fmt.Fprintf(stdout, "Mappings: %d\n", len(contract.Tools))
	for _, tool := range contract.Tools {
		identity := string(tool.Platform) + ":" + tool.Tool
		if tool.ServerFingerprint != "" {
			identity += "@" + tool.ServerFingerprint
		}
		fmt.Fprintf(stdout, "  - %s -> %s (%s)\n", identity, tool.Effect, tool.SourcePath)
	}
	return nil
}

// strOrEmpty returns v as a string, or "" when v isn't one.
func strOrEmpty(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func firstRulePath(target map[string]interface{}) string {
	for _, key := range []string{"paths", "before_paths", "when_paths"} {
		if list, ok := target[key].([]interface{}); ok && len(list) > 0 {
			if s, ok := list[0].(string); ok && s != "" {
				return s
			}
		}
	}
	if files, ok := target["required_files"].([]interface{}); ok && len(files) > 0 {
		if entry, ok := files[0].(map[string]interface{}); ok {
			if s, ok := entry["path"].(string); ok && s != "" {
				return s
			}
		}
	}
	if evidence, ok := target["evidence"].([]interface{}); ok && len(evidence) > 0 {
		if entry, ok := evidence[0].(map[string]interface{}); ok {
			if s, ok := entry["file"].(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func truncateMessageLines(message string, maxLines int) string {
	lines := strings.Split(strings.TrimSpace(message), "\n")
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, " / ")
}
