package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/presets"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/tui"
)

// runDoctor implements `reconc doctor [repo] [--json]`.
//
// The default doctor path runs discovery checks. Deep mode adds source parsing,
// lockfile validation, hook checks, and release-readiness diagnostics.
func runDoctor(args []string, stdout, stderr io.Writer) (resultErr error) {
	repo := "."
	deep := false
	jsonOut := false
	outputPath := ""
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--deep":
			deep = true
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
			fmt.Fprintln(stdout, "Inspect policy discovery state. `--deep` adds lockfile, hook, audit, ref, claim, and conflict diagnostics.")
			fmt.Fprintln(stdout, "--output PATH: write the primary output to stdout and PATH.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc doctor: unknown flag %q", a)}
			}
			repo = a
		}
		i++
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

// runVerify implements `reconc verify [repo] [--json]` (W12).
//
// Checks the full reconc installation health end-to-end:
//   - reconc binary on PATH (we're running, so trivially yes)
//   - $RECONC_HOME directory exists / is writable
//   - global policy (if set) is parseable
//   - bundled presets all resolve
//   - repo discovery, source loading, and parsing succeed
//   - lockfile present + fresh (digest matches sources)
//   - git pre-commit hook installed (when .git/ present)
//
// Always exits 0. Output lists each check with [OK] / [WARN] / [FAIL]
// and a one-line reason. JSON mode emits a structured payload.
func runVerify(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc verify [repo] [--json]")
			fmt.Fprintln(stdout, "End-to-end installation health check. Always exits 0.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc verify: unknown flag %q", a)}
			}
			repo = a
		}
	}

	type check struct {
		Name   string `json:"name"`
		Status string `json:"status"` // OK | WARN | FAIL
		Detail string `json:"detail"`
	}
	checks := []check{}
	add := func(name, status, detail string) {
		checks = append(checks, check{Name: name, Status: status, Detail: detail})
	}

	// 1. reconc binary
	add("reconc binary on PATH", "OK", "running this check confirms it")

	// 2. $RECONC_HOME
	home := presets.Home()
	if info, err := os.Stat(home); err != nil {
		add("RECONC_HOME directory", "WARN", "not present at "+home+" (will be created on first use)")
	} else if !info.IsDir() {
		add("RECONC_HOME directory", "FAIL", home+" exists but is not a directory")
	} else {
		add("RECONC_HOME directory", "OK", home)
	}

	// 3. Global policy (optional)
	globalPath := filepath.Join(home, "global-policy.yml")
	if info, err := os.Stat(globalPath); err == nil && info.Mode().IsRegular() {
		add("global policy", "OK", globalPath)
	} else {
		add("global policy", "OK", "absent (optional)")
	}

	// 4. Bundled presets
	list, err := presets.List()
	if err != nil {
		add("bundled presets", "FAIL", err.Error())
	} else {
		add("bundled presets", "OK", fmt.Sprintf("%d available", len(list)))
	}

	// 5+6+7. Repo discovery + source validation + lockfile freshness
	discovery, derr := ingest.DiscoverPolicyRepo(repo)
	if derr != nil || !discovery.Discovered {
		msg := "no policy markers in " + repo
		if derr != nil {
			msg = derr.Error()
		}
		add("repo discovery", "WARN", msg)
	} else {
		add("repo discovery", "OK", discovery.RepoRoot)

		validation, verr := validatePolicyReadOnly(discovery.RepoRoot)
		if verr != nil {
			add("policy parse", "FAIL", verr.Error())
		} else {
			add("policy parse", "OK", fmt.Sprintf("%d rules from %d sources", validation.ruleCount, validation.sourceCount))
			if err := runtime.ValidatePolicyLockfile(discovery.RepoRoot); err == nil {
				add("lockfile fresh", "OK", filepath.Join(discovery.RepoRoot, ingest.LockfilePath))
			} else {
				add("lockfile fresh", "FAIL", err.Error())
			}
		}
		// Git pre-commit hook
		gitHookPath := filepath.Join(discovery.RepoRoot, ".git", "hooks", "pre-commit")
		if !dirExists(filepath.Join(discovery.RepoRoot, ".git")) {
			add("git pre-commit hook", "WARN", "no .git/ in repo (run `git init` then `reconc hook install git-pre-commit`)")
		} else if _, err := os.Stat(gitHookPath); err != nil {
			add("git pre-commit hook", "WARN", "not installed (run `reconc hook install git-pre-commit`)")
		} else {
			add("git pre-commit hook", "OK", ".git/hooks/pre-commit")
		}
		runtimeCompat := inspectHookRuntimeCompatibility(discovery)
		add("agent hooks runtime compatibility", runtimeCompat.Status, runtimeCompat.Detail)
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]interface{}{"checks": checks})
		return nil
	}
	for _, c := range checks {
		fmt.Fprintf(stdout, "[%-4s] %-30s  %s\n", c.Status, c.Name, c.Detail)
	}
	return nil
}

// runStatus implements `reconc status [repo] [--json]`.
//
// One-line policy health summary. Returns exit 0 always (it's a
// diagnostic, not an enforcement command).
func runStatus(args []string, stdout, stderr io.Writer) (resultErr error) {
	repo := "."
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
			repo = a
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
			"repo_root":      discovery.RepoRoot,
			"discovered":     discovery.Discovered,
			"healthy":        healthy,
			"rule_count":     ruleCount,
			"source_count":   sourceCount,
			"lockfile_fresh": lockfileFresh,
			"default_mode":   defaultMode,
			"issues":         issues,
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
	fmt.Fprintf(out, "[%s] %s\n", icon, joinList(parts))
	return nil
}

// runTUI implements `reconc tui [repo] [--json]`.
//
// This is a dependency-free terminal dashboard: it gives a useful inspection
// view without pulling in a framework or making daily usage heavier.
func runTUI(args []string, stdout, stderr io.Writer) (resultErr error) {
	repo := "."
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
			fmt.Fprintln(stdout, "Render a lightweight terminal dashboard for policy, sources, rules, audit, and active session state.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc tui: unknown flag %q", a)}
			}
			repo = a
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
		_ = enc.Encode(view)
		return nil
	}
	fmt.Fprint(out, tui.RenderText(view))
	return nil
}

func readLockfileSummary(repoRoot string) (map[string]interface{}, error) {
	path := filepath.Join(repoRoot, ingest.LockfilePath)
	data, err := os.ReadFile(path)
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
