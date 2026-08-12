package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/tui"
	"reconc.dev/reconc/internal/usercli"
)

const maxCLILockfileBytes = 16 << 20

// runDoctor implements `reconc doctor [repo] [--json]`.
//
// The default doctor path runs discovery checks. Deep mode adds source parsing,
// lockfile validation, hook checks, and release-readiness diagnostics.
func runDoctor(args []string, version string, stdout, stderr io.Writer) (resultErr error) {
	repo := "."
	repoSet := false
	deep := false
	global := false
	jsonOut := false
	outputPath := ""
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--deep":
			deep = true
		case "--global":
			global = true
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc doctor: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc doctor [repo] [--deep] [--json] [--output PATH]")
			fmt.Fprintln(stdout, "       reconc doctor --global [--json] [--output PATH]")
			fmt.Fprintln(stdout, "Inspect policy discovery state. `--deep` adds lockfile, hook, audit, ref, claim, and conflict diagnostics.")
			fmt.Fprintln(stdout, "`--global` inspects CLI ownership, PATH identity, receipt, checksum, and provenance without repository discovery.")
			fmt.Fprintln(stdout, "--output PATH: write the primary output to stdout and PATH.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc doctor: unknown flag %q", a)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc doctor: unexpected argument %q", a)}
			}
			repo = a
			repoSet = true
		}
		i++
	}

	if global {
		if deep || repoSet {
			return &CLIError{ExitCode: 1, Message: "reconc doctor: --global cannot be combined with --deep or a repository operand"}
		}
		report, err := usercli.DiagnoseGlobal(version)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc doctor: " + err.Error()}
		}
		out, closeOutput, err := teeToFile(stdout, outputPath)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc doctor: open output file: " + err.Error()}
		}
		defer joinOutputCloseError(&resultErr, closeOutput)
		if jsonOut {
			encoder := json.NewEncoder(out)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(report); err != nil {
				return &CLIError{ExitCode: 1, Message: "reconc doctor: json encode: " + err.Error()}
			}
		} else {
			renderGlobalDoctorText(out, report)
		}
		if report.Blocking() {
			return &CLIError{ExitCode: 1, Message: ""}
		}
		return nil
	}

	if deep {
		report, err := buildDoctorDeepReport(repo)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc doctor: " + err.Error()}
		}
		out, closeOutput, err := teeToFile(stdout, outputPath)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc doctor: open output file: " + err.Error()}
		}
		defer joinOutputCloseError(&resultErr, closeOutput)

		if jsonOut {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			if err := enc.Encode(report); err != nil {
				return &CLIError{ExitCode: 1, Message: "reconc doctor: json encode: " + err.Error()}
			}
		} else {
			renderDoctorDeepText(report, out)
		}
		if report.hasFail() {
			return &CLIError{ExitCode: 1, Message: ""}
		}
		return nil
	}

	result, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc doctor: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc doctor: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)

	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc doctor: json encode: " + err.Error()}
		}
		return nil
	}

	return renderDoctorText(result, out)
}

func renderGlobalDoctorText(stdout io.Writer, report *usercli.GlobalDiagnostic) {
	fmt.Fprintln(stdout, "reconc doctor --global")
	fmt.Fprintf(stdout, "Status: %s\n", report.Status)
	if report.Owner == nil {
		fmt.Fprintln(stdout, "Owner: unowned")
	} else {
		fmt.Fprintf(stdout, "Owner: %s\n", *report.Owner)
	}
	fmt.Fprintf(stdout, "Running: %s\n", report.RunningPath)
	if report.ResolvedPath == nil {
		fmt.Fprintln(stdout, "PATH: unresolved")
	} else {
		fmt.Fprintf(stdout, "PATH: %s\n", *report.ResolvedPath)
	}
	for _, check := range report.Checks {
		fmt.Fprintf(stdout, "[%s] %s: %s\n", strings.ToUpper(check.Status), check.Name, check.Detail)
	}
	fmt.Fprintf(stdout, "Next: %s\n", report.NextAction)
}

// runStatus implements `reconc status [repo] [--json]`.
//
// One-line policy health summary. Returns exit 0 always (it's a
// diagnostic, not an enforcement command).
func runStatus(args []string, stdout, stderr io.Writer) (resultErr error) {
	repo := "."
	repoSet := false
	jsonOut := false
	outputPath := ""
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc status: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc status [repo] [--json] [--output PATH]")
			fmt.Fprintln(stdout, "Quick policy health summary (one-liner). Always exits 0.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc status: unknown flag %q", a)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc status: expected at most one repo path"}
			}
			repo = a
			repoSet = true
		}
		i++
	}

	discovery, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc status: " + err.Error()}
	}

	healthy := false
	ruleCount := 0
	sourceCount := 0
	lockfileFresh := false
	defaultMode := ""
	issues := []string{}

	if !discovery.Discovered {
		issues = append(issues, "no policy markers found")
	} else {
		validation, err := validatePolicyReadOnly(discovery.RepoRoot)
		if err != nil {
			issues = append(issues, err.Error())
		} else {
			sourceCount = validation.sourceCount
			if err := runtime.ValidatePolicyLockfile(discovery.RepoRoot); err != nil {
				issues = append(issues, err.Error())
			} else if payload, err := readLockfileSummary(discovery.RepoRoot); err != nil {
				issues = append(issues, err.Error())
			} else {
				ruleCount = int(jsonNumberAsIntDefault(payload["rule_count"], 0))
				sourceCount = int(jsonNumberAsIntDefault(payload["source_count"], 0))
				defaultMode, _ = payload["default_mode"].(string)
				lockfileFresh = true
				healthy = true
			}
		}
	}

	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc status: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)

	if jsonOut {
		payload := map[string]interface{}{
			"repo_root":                  discovery.RepoRoot,
			"discovered":                 discovery.Discovered,
			"healthy":                    healthy,
			"rule_count":                 ruleCount,
			"source_count":               sourceCount,
			"lockfile_fresh":             lockfileFresh,
			"default_mode":               defaultMode,
			"issues":                     issues,
			"mcp_gateway_scope":          "explicit_routes_only",
			"mcp_external_configuration": "not_inspected",
			"mcp_bypass_routes":          "unenforced",
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(payload)
		return nil
	}

	icon := "ISSUE"
	if healthy {
		icon = "OK"
	}
	parts := []string{
		fmt.Sprintf("%d rules", ruleCount),
		fmt.Sprintf("%d sources", sourceCount),
	}
	if lockfileFresh {
		parts = append(parts, "lockfile fresh")
	} else if discovery.Discovered {
		parts = append(parts, "lockfile stale or missing")
	}
	if len(issues) > 0 {
		parts = append(parts, fmt.Sprintf("%d issue(s): %s", len(issues), issues[0]))
	}
	parts = append(parts, "external MCP configs uninspected; direct/native routes unenforced")
	fmt.Fprintf(out, "[%s] %s\n", icon, joinList(parts))
	return nil
}

// runTUI implements `reconc tui [repo] [--json]`.
//
// This is a dependency-free terminal dashboard: it gives a useful inspection
// view without pulling in a framework or making daily usage heavier.
func runTUI(args []string, stdout, stderr io.Writer) (resultErr error) {
	repo := "."
	repoSet := false
	jsonOut := false
	outputPath := ""
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc tui: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc tui [repo] [--json] [--output PATH]")
			fmt.Fprintln(stdout, "Render a lightweight terminal dashboard for policy, completion, sources, rules, audit, and active session state.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc tui: unknown flag %q", a)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc tui: expected at most one repo path"}
			}
			repo = a
			repoSet = true
		}
		i++
	}

	view, err := tui.Build(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc tui: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc tui: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)

	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(view); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc tui: json encode: " + err.Error()}
		}
		return nil
	}
	width := 0
	if columns, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && columns >= 20 && columns <= 1000 {
		width = columns
	}
	fmt.Fprint(out, tui.RenderTextWidth(view, width))
	return nil
}

func readLockfileSummary(repoRoot string) (map[string]interface{}, error) {
	path := filepath.Join(repoRoot, ingest.LockfilePath)
	data, err := boundedio.ReadRegularFile(path, maxCLILockfileBytes)
	if err != nil {
		return nil, err
	}
	var payload map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("lockfile is not valid JSON: %w", err)
	}
	if payload == nil {
		return nil, fmt.Errorf("lockfile must contain a JSON object")
	}
	migrated, _, err := compiler.MigrateLockfile(payload)
	if err != nil {
		return nil, err
	}
	return migrated, nil
}

type readOnlyPolicyValidation struct {
	ruleCount    int
	sourceCount  int
	sourceDigest string
	conflicts    int
}

func validatePolicyReadOnly(repoRoot string) (*readOnlyPolicyValidation, error) {
	bundle, err := ingest.LoadPolicySources(repoRoot)
	if err != nil {
		return nil, err
	}
	parsed, err := parser.ParseRuleDocuments(bundle)
	if err != nil {
		return nil, err
	}
	conflicts := compiler.DetectConflicts(parsed.Rules)
	sourceDigest, err := compiler.ComputeSourceDigest(bundle)
	if err != nil {
		return nil, fmt.Errorf("compute source digest: %w", err)
	}
	return &readOnlyPolicyValidation{
		ruleCount:    len(parsed.Rules),
		sourceCount:  len(bundle.Sources),
		sourceDigest: sourceDigest,
		conflicts:    len(conflicts),
	}, nil
}

func jsonNumberAsIntDefault(v interface{}, def int64) int64 {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i
		}
	case float64:
		return int64(n)
	case int:
		return int64(n)
	}
	return def
}

func renderDoctorText(r ingest.DiscoveryResult, w io.Writer) error {
	fmt.Fprintf(w, "reconc doctor (Phase 1: discovery only)\n")
	fmt.Fprintf(w, "  start path:  %s\n", r.StartPath)
	fmt.Fprintf(w, "  repo root:   %s\n", r.RepoRoot)
	fmt.Fprintf(w, "  discovered:  %v\n", r.Discovered)
	fmt.Fprintf(w, "  entry file:  %s\n", renderEntryFile(r))
	fmt.Fprintf(w, "  config:      %s\n", renderOptional(r.ConfigPath))
	fmt.Fprintf(w, "  policies:    %d file(s)\n", len(r.PolicyPaths))
	fmt.Fprintf(w, "  lockfile:    %s\n", renderOptional(r.LockfilePath))
	if len(r.Warnings) > 0 {
		fmt.Fprintf(w, "  warnings (%d):\n", len(r.Warnings))
		for _, wn := range r.Warnings {
			fmt.Fprintf(w, "    - %s\n", wn)
		}
	}
	return nil
}

func renderEntryFile(r ingest.DiscoveryResult) string {
	// Prefer AGENTS.md, then start.md, then CLAUDE.md (legacy).
	if r.AgentsPath != nil {
		return *r.AgentsPath
	}
	if r.StartMDPath != nil {
		return *r.StartMDPath
	}
	if r.ClaudePath != nil {
		return *r.ClaudePath + " (legacy)"
	}
	return "<none>"
}

func renderOptional(p *string) string {
	if p == nil {
		return "<not present>"
	}
	return *p
}
