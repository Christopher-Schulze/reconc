package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/runtime"
	"strings"
)

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
		data, err := os.ReadFile(reportFile)
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

// runWhy implements `reconc why <rule-id|mcp> [repo] [--json]` (W13).
//
// Prints everything known about a rule: kind, mode, message, all
// targeting fields, source provenance. Useful when a violation
// surfaces a rule id and the agent needs context.
func runWhy(args []string, stdout, stderr io.Writer) error {
	// Handle --help before requiring a rule-id so `reconc why --help` works.
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc why <rule-id|mcp> [repo] [--json|--terse]")
			fmt.Fprintln(stdout, "Show one rule or the MCP side-effect contract from the compiled lockfile.")
			return nil
		}
	}
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc why: missing required <rule-id> argument"}
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
			fmt.Fprintln(stdout, "Usage: reconc why <rule-id|mcp> [repo] [--json|--terse]")
			fmt.Fprintln(stdout, "Show one rule or the MCP side-effect contract from the compiled lockfile.")
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

	lockPath := filepath.Join(discovery.RepoRoot, ingest.LockfilePath)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc why: read lockfile: " + err.Error()}
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc why: lockfile is not valid JSON: " + err.Error()}
	}
	if ruleID == "mcp" {
		return renderWhyMCP(payload["mcp"], jsonOut, terse, stdout)
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
	if jsonOut && terse {
		return &CLIError{ExitCode: 1, Message: "reconc why: --json and --terse are mutually exclusive"}
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

func renderWhyMCP(raw interface{}, jsonOut, terse bool, stdout io.Writer) error {
	if jsonOut && terse {
		return &CLIError{ExitCode: 1, Message: "reconc why: --json and --terse are mutually exclusive"}
	}
	if raw == nil {
		if jsonOut {
			_, _ = fmt.Fprintln(stdout, "null")
		} else {
			_, _ = fmt.Fprintln(stdout, "MCP policy: not configured; host behavior remains in control and no MCP call is classified as repository evidence.")
		}
		return nil
	}
	contract, ok := raw.(map[string]interface{})
	if !ok {
		return &CLIError{ExitCode: 1, Message: "reconc why: compiled MCP contract is invalid"}
	}
	if jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(contract)
		return nil
	}
	tools, _ := contract["tools"].([]interface{})
	if terse {
		fmt.Fprintf(stdout, "unclassified=%s mappings=%d\n", strOrEmpty(contract["unclassified"]), len(tools))
		return nil
	}
	fmt.Fprintf(stdout, "MCP unclassified: %s\n", strOrEmpty(contract["unclassified"]))
	fmt.Fprintf(stdout, "Mappings: %d\n", len(tools))
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]interface{})
		if !ok {
			continue
		}
		identity := strOrEmpty(tool["platform"]) + ":" + strOrEmpty(tool["tool"])
		if fingerprint := strOrEmpty(tool["server_fingerprint"]); fingerprint != "" {
			identity += "@" + fingerprint
		}
		fmt.Fprintf(stdout, "  - %s -> %s (%s)\n", identity, strOrEmpty(tool["effect"]), strOrEmpty(tool["source_path"]))
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
