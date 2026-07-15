package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"reconc.dev/reconc/internal/runtime"
	"strings"
	"time"
)

// runCheck implements `reconc check [repo] [--read PATH...] [--write PATH...]
// [--command CMD...] [--command-success CMD...] [--command-failure CMD...]
// [--claim NAME...] [--json]`.
//
// Returns *CLIError exit 2 on a blocking decision, exit 1 on runtime
// errors, exit 0 on pass/warn.
func runCheck(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	terse := false
	outputPath := ""
	inputs := runtime.Empty()

	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--terse":
			terse = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc check: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc check [repo] [--read PATH] [--write PATH]")
			fmt.Fprintln(stdout, "                    [--command CMD] [--command-success CMD]")
			fmt.Fprintln(stdout, "                    [--command-failure CMD] [--claim NAME]")
			fmt.Fprintln(stdout, "                    [--json | --terse] [--output PATH]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Evaluate runtime evidence against the compiled policy lockfile.")
			fmt.Fprintln(stdout, "  --json   full structured report")
			fmt.Fprintln(stdout, "  --terse  minimal {decision, ok, rule_ids, actions} (~50 tokens)")
			fmt.Fprintln(stdout, "  --output PATH  write the primary output to stdout and PATH")
			fmt.Fprintln(stdout, "Exit codes: 0 = pass/warn, 1 = error, 2 = blocking violation.")
			return nil
		case "--read":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc check: --read requires a value"}
			}
			inputs.ReadPaths = append(inputs.ReadPaths, val)
		case "--write":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc check: --write requires a value"}
			}
			inputs.WritePaths = append(inputs.WritePaths, val)
		case "--command":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc check: --command requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
		case "--command-success":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc check: --command-success requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
			inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
				Command: val,
				Outcome: runtime.CommandOutcomeSuccess,
			})
		case "--command-failure":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc check: --command-failure requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
			inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
				Command: val,
				Outcome: runtime.CommandOutcomeFailure,
			})
		case "--claim":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc check: --claim requires a value"}
			}
			inputs.Claims = append(inputs.Claims, val)
		case "--auto-claim":
			// W7: detect CI environment and auto-assert `ci-green`.
			// Lets hosted CI pipelines skip the manual hook claim step.
			if detectCIEnvironment() {
				inputs.Claims = append(inputs.Claims, "ci-green")
			}
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc check: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	start := time.Now()
	report, err := runtime.CheckRepoPolicy(repo, inputs)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc check: " + err.Error()}
	}
	maybeAudit("check", report, start)
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc check: open output file: " + err.Error()}
	}
	defer func() { _ = closeOutput() }()

	switch {
	case terse:
		// Compact JSON: ~50 tokens for the most common case.
		// Designed for hook-loop calls where every token counts.
		enc := json.NewEncoder(out)
		// No indent = compact form; agents parse it just fine.
		if err := enc.Encode(report.Terse()); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc check: terse encode: " + err.Error()}
		}
	case jsonOut:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc check: json encode: " + err.Error()}
		}
	default:
		renderCheckText(report, out)
	}

	if report.Decision == runtime.DecisionBlock {
		return &CLIError{ExitCode: 2, Message: ""}
	}
	return nil
}

// runAssert implements `reconc assert <rule-id> [repo] [--var key=value ...]
// [--read PATH] [--write PATH] [--command CMD] [--claim NAME] [--json]`.
//
// Single-rule evaluation primitive. Replaces repo-specific assertion
// subcommands with one generic command driven by the lockfile.
//
// Exit codes: 0 = pass/warn (no blocking violation), 1 = error,
// 2 = blocking violation.
func runAssert(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc assert: missing required <rule-id> argument"}
	}
	ruleID := ""
	repo := "."
	jsonOut := false
	vars := map[string]string{}
	inputs := runtime.Empty()

	// First positional is the rule id.
	ruleID = args[0]
	if ruleID == "-h" || ruleID == "--help" {
		fmt.Fprintln(stdout, "Usage: reconc assert <rule-id> [repo] [--var key=value]")
		fmt.Fprintln(stdout, "                     [--read PATH] [--write PATH] [--command CMD]")
		fmt.Fprintln(stdout, "                     [--command-success CMD] [--command-failure CMD]")
		fmt.Fprintln(stdout, "                     [--claim NAME] [--json]")
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "Evaluate ONE rule by id. --var binds template variables for substitution.")
		fmt.Fprintln(stdout, "Synthesizes write_paths from rule.when_paths so the rule triggers.")
		fmt.Fprintln(stdout, "Exit codes: 0 = pass/warn, 1 = error, 2 = blocking violation.")
		return nil
	}

	i := 1
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--var":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc assert: --var requires key=value"}
			}
			parts := splitOnce(val, "=")
			if len(parts) != 2 || parts[0] == "" {
				return &CLIError{ExitCode: 1, Message: "reconc assert: --var must be key=value, got " + val}
			}
			vars[parts[0]] = parts[1]
		case "--read":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc assert: --read requires a value"}
			}
			inputs.ReadPaths = append(inputs.ReadPaths, val)
		case "--write":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc assert: --write requires a value"}
			}
			inputs.WritePaths = append(inputs.WritePaths, val)
		case "--command":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc assert: --command requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
		case "--command-success":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc assert: --command-success requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
			inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
				Command: val, Outcome: runtime.CommandOutcomeSuccess,
			})
		case "--command-failure":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc assert: --command-failure requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
			inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
				Command: val, Outcome: runtime.CommandOutcomeFailure,
			})
		case "--claim":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc assert: --claim requires a value"}
			}
			inputs.Claims = append(inputs.Claims, val)
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc assert: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	startAssert := time.Now()
	report, err := runtime.AssertRuleByID(repo, ruleID, vars, inputs)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc assert: " + err.Error()}
	}
	maybeAudit("assert", report, startAssert)

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc assert: json encode: " + err.Error()}
		}
	} else {
		fmt.Fprintf(stdout, "Rule:      %s\n", ruleID)
		if len(vars) > 0 {
			vbuf := []string{}
			for k, v := range vars {
				vbuf = append(vbuf, k+"="+v)
			}
			fmt.Fprintf(stdout, "Vars:      %s\n", joinList(vbuf))
		}
		renderCheckText(report, stdout)
	}

	if report.Decision == runtime.DecisionBlock {
		return &CLIError{ExitCode: 2, Message: ""}
	}
	return nil
}

// runFix implements `reconc fix [repo] [same evidence flags as check] [--json]`.
//
// Wraps check + BuildFixPlan to produce action-focused remediation
// output. Same exit codes as check (0 = pass/warn, 2 = block).
func runFix(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	nextOnly := false
	outputPath := ""
	inputs := runtime.Empty()

	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--next":
			nextOnly = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc fix: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc fix [repo] [--read PATH] [--write PATH]")
			fmt.Fprintln(stdout, "                  [--command CMD] [--command-success CMD]")
			fmt.Fprintln(stdout, "                  [--command-failure CMD] [--claim NAME] [--json] [--next] [--output PATH]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Same evidence as `reconc check` but emits a structured remediation plan")
			fmt.Fprintln(stdout, "with per-violation steps + suggested commands/claims/files. Exit codes")
			fmt.Fprintln(stdout, "match check (0 = pass/warn, 2 = block).")
			return nil
		case "--read":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc fix: --read requires a value"}
			}
			inputs.ReadPaths = append(inputs.ReadPaths, val)
		case "--write":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc fix: --write requires a value"}
			}
			inputs.WritePaths = append(inputs.WritePaths, val)
		case "--command":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc fix: --command requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
		case "--command-success":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc fix: --command-success requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
			inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
				Command: val, Outcome: runtime.CommandOutcomeSuccess,
			})
		case "--command-failure":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc fix: --command-failure requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
			inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
				Command: val, Outcome: runtime.CommandOutcomeFailure,
			})
		case "--claim":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc fix: --claim requires a value"}
			}
			inputs.Claims = append(inputs.Claims, val)
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc fix: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	startFix := time.Now()
	report, err := runtime.CheckRepoPolicy(repo, inputs)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc fix: " + err.Error()}
	}
	maybeAudit("fix", report, startFix)
	plan := runtime.BuildFixPlan(report)
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc fix: open output file: " + err.Error()}
	}
	defer func() { _ = closeOutput() }()
	if nextOnly {
		next := nextRemediation(plan)
		if next == nil {
			if jsonOut {
				payload := map[string]interface{}{
					"summary":           plan.Summary,
					"remediation_count": 0,
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(payload); err != nil {
					return err
				}
			} else {
				fmt.Fprintln(out, "No remediation needed.")
			}
		} else if jsonOut {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			if err := enc.Encode(next); err != nil {
				return err
			}
		} else {
			fmt.Fprint(out, renderNextRemediationText(next))
		}
		if report.Decision == runtime.DecisionBlock {
			return &CLIError{ExitCode: 2, Message: ""}
		}
		return nil
	}

	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(plan); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc fix: json encode: " + err.Error()}
		}
	} else {
		fmt.Fprint(out, runtime.RenderFixPlanText(plan))
	}

	if report.Decision == runtime.DecisionBlock {
		return &CLIError{ExitCode: 2, Message: ""}
	}
	return nil
}

func runNext(args []string, stdout, stderr io.Writer) error {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc next [repo] [--read PATH] [--write PATH]")
			fmt.Fprintln(stdout, "                   [--command CMD] [--command-success CMD]")
			fmt.Fprintln(stdout, "                   [--command-failure CMD] [--claim NAME] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Terse alias for `reconc fix --next`: print only the next remediation.")
			return nil
		}
	}
	return runFix(append(append([]string{}, args...), "--next"), stdout, stderr)
}

// runCan implements `reconc can <action> <path> [repo] [--terse|--json|--why]` (W41).
//
// Ultra-terse binary yes/no. Designed for fast-path agent decisions
// before writing a file.
//
// Currently supported actions: write
// (read/command/claim could be added later if needed)
//
// Default text output:
//
//	yes
//	no: <rule-id> <recommended_action>
//
// Exit codes: 0 = yes, 2 = no, 1 = error.
func runCan(args []string, stdout, stderr io.Writer) error {
	// Handle --help before arg-count check so `reconc can --help` works.
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc can <action> <path> [repo] [--why] [--json]")
			fmt.Fprintln(stdout, "Binary yes/no for a single proposed action.")
			fmt.Fprintln(stdout, "Actions: write")
			fmt.Fprintln(stdout, "Exit codes: 0 = yes, 2 = no, 1 = error.")
			return nil
		}
	}
	if len(args) < 2 {
		return &CLIError{ExitCode: 1, Message: "reconc can: usage: reconc can <action> <path> [repo] [--why|--json]"}
	}
	action := args[0]
	path := args[1]
	repo := "."
	showWhy := false
	jsonOut := false
	for i := 2; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--why":
			showWhy = true
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc can <action> <path> [repo] [--why] [--json]")
			fmt.Fprintln(stdout, "Binary yes/no for a single proposed action.")
			fmt.Fprintln(stdout, "Actions: write")
			fmt.Fprintln(stdout, "Exit codes: 0 = yes, 2 = no, 1 = error.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc can: unknown flag %q", a)}
			}
			repo = a
		}
	}

	if action != "write" {
		return &CLIError{ExitCode: 1, Message: "reconc can: action must be 'write' (other actions not yet supported)"}
	}

	inputs := runtime.Empty()
	inputs.WritePaths = []string{path}
	startCan := time.Now()
	report, err := runtime.CheckRepoPolicy(repo, inputs)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc can: " + err.Error()}
	}
	maybeAudit("can", report, startCan)

	yes := report.Decision != runtime.DecisionBlock
	if jsonOut {
		payload := map[string]interface{}{
			"yes":      yes,
			"decision": report.Decision,
			"action":   action,
			"path":     path,
		}
		if !yes && len(report.Violations) > 0 {
			v := report.Violations[0]
			payload["rule_id"] = v.RuleID
			payload["why"] = v.Explanation
			payload["recommended_action"] = v.RecommendedAction
		}
		enc := json.NewEncoder(stdout)
		_ = enc.Encode(payload)
	} else {
		if yes {
			fmt.Fprintln(stdout, "yes")
		} else {
			v := report.Violations[0]
			fmt.Fprintf(stdout, "no: %s %s\n", v.RuleID, v.RecommendedAction)
			if showWhy {
				fmt.Fprintf(stdout, "why: %s\n", v.Explanation)
			}
		}
	}

	if !yes {
		return &CLIError{ExitCode: 2, Message: ""}
	}
	return nil
}

func nextRemediation(plan *runtime.FixPlan) *runtime.Remediation {
	if plan == nil || len(plan.Remediations) == 0 {
		return nil
	}
	for i := range plan.Remediations {
		if plan.Remediations[i].Priority == "blocking" {
			return &plan.Remediations[i]
		}
	}
	return &plan.Remediations[0]
}

func renderNextRemediationText(remediation *runtime.Remediation) string {
	if remediation == nil {
		return "No remediation needed.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "next: [%s|%s] %s\n", remediation.Priority, remediation.Kind, remediation.RuleID)
	fmt.Fprintf(&b, "why: %s\n", remediation.Why)
	fmt.Fprintf(&b, "do: %s\n", remediation.RecommendedAction)
	return b.String()
}

func renderCheckText(r *runtime.CheckReport, w io.Writer) {
	fmt.Fprintf(w, "Decision:  %s\n", r.Decision)
	fmt.Fprintf(w, "Repo:      %s\n", r.RepoRoot)
	fmt.Fprintf(w, "Lockfile:  %s\n", r.LockfilePath)
	fmt.Fprintf(w, "Default:   %s\n", r.DefaultMode)
	fmt.Fprintf(w, "Summary:   %s\n", r.Summary)
	if r.NextAction != "" {
		fmt.Fprintf(w, "Next:      %s\n", r.NextAction)
	}
	if r.ViolationCount == 0 {
		return
	}
	fmt.Fprintf(w, "\nViolations (%d total, %d blocking):\n", r.ViolationCount, r.BlockingViolationCount)
	for i, v := range r.Violations {
		fmt.Fprintf(w, "  %d. [%s | %s] %s\n", i+1, v.Mode, v.Kind, v.RuleID)
		fmt.Fprintf(w, "     %s\n", v.Explanation)
		fmt.Fprintf(w, "     -> %s\n", v.RecommendedAction)
	}
}
