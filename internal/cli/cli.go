// Package cli implements the argparse-equivalent CLI dispatcher for reconc.
//
// Run dispatches argv to the appropriate subcommand. It returns nil on
// success or a CLIError carrying an exit code for the main binary to
// surface to the shell.
//
// Exit codes:
//
//	0 -- clean run, or a non-blocking decision (pass/warn)
//	1 -- runtime or input error
//	2 -- at least one blocking policy violation (block)
package cli

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/adopt"
	"reconc.dev/reconc/internal/agentguide"
	"reconc.dev/reconc/internal/audit"
	"reconc.dev/reconc/internal/changelog"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/completion"
	"reconc.dev/reconc/internal/contextsize"
	"reconc.dev/reconc/internal/extractor"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/lockdiff"
	"reconc.dev/reconc/internal/manpage"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/presets"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
	"reconc.dev/reconc/internal/scaffold"
	"reconc.dev/reconc/internal/templates"
	"reconc.dev/reconc/internal/tui"
)

// CLIError carries an exit code alongside an error message so the CLI
// layer can map non-zero outcomes to the correct shell exit.
type CLIError struct {
	ExitCode int
	Message  string
}

func (e *CLIError) Error() string {
	return e.Message
}

// ExitCode extracts a shell exit code from any error returned by Run.
// A nil error means exit 0. A *CLIError carries its own code. Any other
// error maps to exit 1 (runtime error).
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ce *CLIError
	if stderrors.As(err, &ce) {
		return ce.ExitCode
	}
	return 1
}

// Run parses argv and dispatches to the matching subcommand. stdout and
// stderr are explicit so tests can capture output without touching os.Stdout.
func Run(argv []string, version string, stdout, stderr io.Writer) error {
	if len(argv) == 0 {
		printUsage(stdout, version)
		return nil
	}
	switch argv[0] {
	case "--version", "-V", "version":
		return runVersion(argv[1:], version, stdout)
	case "--help", "-h", "help":
		printUsage(stdout, version)
		return nil
	case "doctor":
		return runDoctor(argv[1:], stdout, stderr)
	case "compile":
		return runCompile(argv[1:], version, stdout, stderr)
	case "check":
		return runCheck(argv[1:], stdout, stderr)
	case "assert":
		return runAssert(argv[1:], stdout, stderr)
	case "init":
		return runInit(argv[1:], stdout, stderr)
	case "status":
		return runStatus(argv[1:], stdout, stderr)
	case "ci":
		return runCI(argv[1:], stdout, stderr)
	case "hook":
		return runHook(argv[1:], stdout, stderr)
	case "preset":
		return runPreset(argv[1:], stdout, stderr)
	case "bootstrap":
		return runBootstrap(argv[1:], version, stdout, stderr)
	case "setup":
		return runBootstrap(argv[1:], version, stdout, stderr)
	case "fix":
		return runFix(argv[1:], stdout, stderr)
	case "next":
		return runNext(argv[1:], stdout, stderr)
	case "explain":
		return runExplain(argv[1:], stdout, stderr)
	case "verify":
		return runVerify(argv[1:], stdout, stderr)
	case "why":
		return runWhy(argv[1:], stdout, stderr)
	case "can":
		return runCan(argv[1:], stdout, stderr)
	case "adopt":
		return runAdopt(argv[1:], stdout, stderr)
	case "changelog":
		return runChangelog(argv[1:], stdout, stderr)
	case "agent-intro":
		return runAgentIntro(argv[1:], stdout, stderr)
	case "audit":
		return runAudit(argv[1:], stdout, stderr)
	case "template":
		return runTemplate(argv[1:], stdout, stderr)
	case "session-briefing":
		return runSessionBriefing(argv[1:], stdout, stderr)
	case "context":
		return runContext(argv[1:], stdout, stderr)
	case "start":
		return runStart(argv[1:], stdout, stderr)
	case "post-task-check":
		return runPostTaskCheck(argv[1:], stdout, stderr)
	case "delta":
		return runDelta(argv[1:], stdout, stderr)
	case "done":
		return runDone(argv[1:], stdout, stderr)
	case "spec":
		return runSpec(argv[1:], stdout, stderr)
	case "coverage":
		return runCoverage(argv[1:], stdout, stderr)
	case "extract":
		return runExtract(argv[1:], stdout, stderr)
	case "diff":
		return runDiff(argv[1:], stdout, stderr)
	case "watch":
		return runWatch(argv[1:], stdout, stderr)
	case "tui":
		return runTUI(argv[1:], stdout, stderr)
	case "completion":
		return runCompletion(argv[1:], stdout, stderr)
	case "manpage":
		return runManpage(argv[1:], version, stdout)
	default:
		return &CLIError{
			ExitCode: 1,
			Message:  fmt.Sprintf("reconc: subcommand %q is not yet implemented in this build; run `reconc --help` for the current surface", argv[0]),
		}
	}
}

func teeToFile(w io.Writer, path string) (io.Writer, func() error, error) {
	if path == "" {
		return w, func() error { return nil }, nil
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, err
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return io.MultiWriter(w, file), file.Close, nil
}

// runCompile implements `reconc compile [repo] [--json]`.
//
// Loads sources, parses rules, computes the digest, and writes
// .reconc/policy.lock.json. Returns a CLIError with exit 1 on any
// pipeline failure (PolicySourceError, RuleValidationError, etc.).
func runCompile(args []string, version string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	strictConflicts := false
	outputPath := ""
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--strict-conflicts":
			strictConflicts = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc compile: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc compile [repo] [--json] [--strict-conflicts] [--output PATH]")
			fmt.Fprintln(stdout, "Compile policy sources into .reconc/policy.lock.json.")
			fmt.Fprintln(stdout, "--strict-conflicts: exit 1 if any rule conflicts are detected.")
			fmt.Fprintln(stdout, "--output PATH: write the primary output to stdout and PATH.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc compile: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	compiled, err := compiler.CompileRepoPolicy(repo, version)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc compile: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc compile: open output file: " + err.Error()}
	}
	defer func() { _ = closeOutput() }()

	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(compiled); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc compile: json encode: " + err.Error()}
		}
		if strictConflicts && len(compiled.Conflicts) > 0 {
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc compile: %d rule conflict(s) detected under --strict-conflicts", len(compiled.Conflicts))}
		}
		return nil
	}

	fmt.Fprintf(out, "Compiled %d rules from %d sources into %s for %s\n",
		compiled.RuleCount, compiled.SourceCount, compiled.LockfilePath, compiled.RepoRoot)
	fmt.Fprintf(out, "Default mode:  %s\n", compiled.DefaultMode)
	fmt.Fprintf(out, "Source digest: %s\n", compiled.SourceDigest)
	if len(compiled.Warnings) > 0 {
		fmt.Fprintf(out, "Warnings (%d):\n", len(compiled.Warnings))
		for _, w := range compiled.Warnings {
			fmt.Fprintf(out, "  - %s\n", w)
		}
	}
	if len(compiled.Conflicts) > 0 {
		fmt.Fprintf(out, "Conflicts (%d):\n", len(compiled.Conflicts))
		for _, cf := range compiled.Conflicts {
			fmt.Fprintf(out, "  - [%s] %s\n", cf.Kind, cf.Description)
		}
	}
	if strictConflicts && len(compiled.Conflicts) > 0 {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc compile: %d rule conflict(s) detected under --strict-conflicts", len(compiled.Conflicts))}
	}
	return nil
}

// runDoctor implements `reconc doctor [repo] [--json]`.
//
// The default doctor path runs discovery checks. Deep mode adds source parsing,
// lockfile validation, hook checks, and release-readiness diagnostics.
func runDoctor(args []string, stdout, stderr io.Writer) error {
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
		defer func() { _ = closeOutput() }()

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
	defer func() { _ = closeOutput() }()

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

// nextArgValue advances i and returns the next argument as the value
// for a flag. Returns ("", false) if i is at the end.
func nextArgValue(args []string, i *int, flag string) (string, bool) {
	*i++
	if *i >= len(args) {
		return "", false
	}
	return args[*i], true
}

// runAssert implements `reconc assert <rule-id> [repo] [--var key=value ...]
// [--read PATH] [--write PATH] [--command CMD] [--claim NAME] [--json]`.
//
// Single-rule evaluation primitive. Replaces Golem-Office's per-assertion
// subcommands (assert stage, assert task-done, assert sequence, assert
// force-multipliers) with one generic command driven by the lockfile.
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

// splitOnce splits s on the first occurrence of sep into 2 parts.
// Returns one part if sep is absent.
func splitOnce(s, sep string) []string {
	for i := 0; i < len(s); i++ {
		if i+len(sep) <= len(s) && s[i:i+len(sep)] == sep {
			return []string{s[:i], s[i+len(sep):]}
		}
	}
	return []string{s}
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
func runExplain(args []string, stdout, stderr io.Writer) error {
	repo := "."
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
			fmt.Fprintln(stdout, "Usage: reconc explain [repo] [evidence flags...] [--format text|markdown] [--json] [--output PATH]")
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
			repo = a
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
	defer func() { _ = closeOutput() }()

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

// runVerify implements `reconc verify [repo] [--json]` (W12).
//
// Checks the full reconc setup health end-to-end:
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
			fmt.Fprintln(stdout, "End-to-end setup health check. Always exits 0.")
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
			if discovery.LockfilePath == nil {
				add("lockfile fresh", "WARN", "no lockfile (run `reconc compile`)")
			} else if payload, err := readLockfileSummary(discovery.RepoRoot); err != nil {
				add("lockfile fresh", "FAIL", err.Error())
			} else if err := validateLockfileRepoRoot(discovery.RepoRoot, payload); err != nil {
				add("lockfile fresh", "FAIL", err.Error())
			} else if storedDigest, _ := payload["source_digest"].(string); storedDigest == validation.sourceDigest {
				add("lockfile fresh", "OK", filepath.Join(discovery.RepoRoot, ingest.LockfilePath))
			} else {
				add("lockfile fresh", "FAIL", "stale lockfile (run `reconc compile`)")
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

// runWhy implements `reconc why <rule-id> [repo] [--json]` (W13).
//
// Prints everything known about a rule: kind, mode, message, all
// targeting fields, source provenance. Useful when a violation
// surfaces a rule id and the agent needs context.
func runWhy(args []string, stdout, stderr io.Writer) error {
	// Handle --help before requiring a rule-id so `reconc why --help` works.
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc why <rule-id> [repo] [--json|--terse]")
			fmt.Fprintln(stdout, "Show full details of one rule from the compiled lockfile.")
			return nil
		}
	}
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc why: missing required <rule-id> argument"}
	}
	ruleID := args[0]
	repo := "."
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
			fmt.Fprintln(stdout, "Usage: reconc why <rule-id> [repo] [--json|--terse]")
			fmt.Fprintln(stdout, "Show full details of one rule from the compiled lockfile.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc why: unknown flag %q", a)}
			}
			repo = a
		}
	}

	discovery, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc why: " + err.Error()}
	}
	if !discovery.Discovered || discovery.LockfilePath == nil {
		return &CLIError{ExitCode: 1, Message: "reconc why: no compiled lockfile; run `reconc compile` first"}
	}

	lockPath := filepath.Join(discovery.RepoRoot, *discovery.LockfilePath)
	data, err := os.ReadFile(lockPath)
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

func firstStringOrDash(values []string) string {
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return "-"
	}
	return values[0]
}

// runAdopt implements `reconc adopt [repo] [--yaml|--json|--apply]` (W15).
//
// Scans the repo for common tooling (package.json, pyproject.toml,
// Cargo.toml, go.mod, .github/workflows/, dist/, build/, generated/)
// and suggests matching reconc rules.
//
// Modes:
//
//	default:  human-readable text summary + next-steps hints
//	--yaml:   YAML snippet suitable for pasting into .reconc.yml rules:
//	--json:   machine-readable report (agent consumption)
//	--apply:  append suggestions to .reconc.yml (creates the file if absent)
//
// All suggested rules default to mode: warn so adoption doesn't
// immediately break workflows; the user can flip to block once green.
func runAdopt(args []string, stdout, stderr io.Writer) error {
	// --help short-circuit.
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc adopt [repo] [--yaml|--json|--apply]")
			fmt.Fprintln(stdout, "Scan the repo for existing tooling and suggest reconc rules.")
			fmt.Fprintln(stdout, "All suggestions are warn-mode by default. Flip to block once green.")
			return nil
		}
	}

	repo := "."
	yamlOut := false
	jsonOut := false
	applyOut := false
	for _, a := range args {
		switch a {
		case "--yaml":
			yamlOut = true
		case "--json":
			jsonOut = true
		case "--apply":
			applyOut = true
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc adopt: unknown flag %q", a)}
			}
			repo = a
		}
	}
	// Mutually exclusive output modes except that --apply can combine
	// with --json (useful for agents that want to know what was added).
	if yamlOut && jsonOut {
		return &CLIError{ExitCode: 1, Message: "reconc adopt: --yaml and --json are mutually exclusive"}
	}
	if yamlOut && applyOut {
		return &CLIError{ExitCode: 1, Message: "reconc adopt: --yaml and --apply are mutually exclusive"}
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc adopt: " + err.Error()}
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return &CLIError{ExitCode: 1, Message: "reconc adopt: not a directory: " + abs}
	}

	report := adopt.Scan(abs)

	if applyOut {
		added, err := adopt.Apply(abs, report)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc adopt --apply: " + err.Error()}
		}
		if jsonOut {
			payload := map[string]interface{}{
				"repo_root":   abs,
				"added":       added,
				"suggestions": report.Suggestions,
				"detected":    report.Detected,
				"config_path": filepath.Join(abs, ".reconc.yml"),
			}
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}
		if len(added) == 0 {
			fmt.Fprintf(stdout, "reconc adopt --apply: no new rules (all %d suggestions already present or no conventions detected)\n", len(report.Suggestions))
			return nil
		}
		fmt.Fprintf(stdout, "reconc adopt --apply: added %d rule(s) to %s\n", len(added), filepath.Join(abs, ".reconc.yml"))
		for _, id := range added {
			fmt.Fprintf(stdout, "  - %s\n", id)
		}
		fmt.Fprintln(stdout, "\nNext: reconc compile")
		return nil
	}

	if jsonOut {
		payload, err := adopt.ToJSON(report, true)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc adopt --json: " + err.Error()}
		}
		_, _ = stdout.Write(payload)
		_, _ = stdout.Write([]byte("\n"))
		return nil
	}

	if yamlOut {
		fmt.Fprint(stdout, adopt.RenderYAML(report))
		return nil
	}

	fmt.Fprint(stdout, adopt.RenderText(report))
	return nil
}

// runChangelog implements `reconc changelog <rotate|list-archives>` (W45).
//
// Rotates docs/changelog.md into docs/changelog/archive/YYYY-QN.md
// when the file exceeds the configured line threshold. Keeps the
// auto-loaded changelog small so agent session-start token budget
// stays under control, without losing history.
func runChangelog(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc changelog: missing subcommand (rotate | list-archives)"}
	}
	// --help short-circuit either before or after the subcommand.
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage:")
			fmt.Fprintln(stdout, "  reconc changelog rotate [repo] [--force] [--lines N] [--json]")
			fmt.Fprintln(stdout, "  reconc changelog list-archives [repo] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Keeps docs/changelog.md small by moving older ## sections into")
			fmt.Fprintln(stdout, "docs/changelog/archive/YYYY-QN.md. Non-destructive: no-op when the")
			fmt.Fprintln(stdout, "file is already under the threshold (default 200 lines).")
			return nil
		}
	}

	sub := args[0]
	rest := args[1:]
	switch sub {
	case "rotate":
		return runChangelogRotate(rest, stdout, stderr)
	case "list-archives":
		return runChangelogListArchives(rest, stdout, stderr)
	default:
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc changelog: unknown subcommand %q (expected rotate or list-archives)", sub)}
	}
}

func runChangelogRotate(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	opts := changelog.Options{}
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--force":
			opts.Force = true
		case "--lines":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc changelog rotate: --lines requires an integer argument"}
			}
			n, err := atoi(args[i+1])
			if err != nil || n <= 0 {
				return &CLIError{ExitCode: 1, Message: "reconc changelog rotate: --lines must be a positive integer, got " + args[i+1]}
			}
			opts.ThresholdLines = n
			i++
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc changelog rotate: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc changelog rotate: " + err.Error()}
	}

	result, err := changelog.Rotate(abs, opts)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc changelog rotate: " + err.Error()}
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	if result.Rotated {
		fmt.Fprintf(stdout, "Rotated %s\n", result.ChangelogPath)
		fmt.Fprintf(stdout, "  - lines:    %d -> %d\n", result.LinesBefore, result.LinesAfter)
		fmt.Fprintf(stdout, "  - archive:  %s (%d sections moved)\n", result.ArchivePath, result.SectionsArchived)
		if len(result.ArchivedIDs) > 0 {
			fmt.Fprintln(stdout, "  - archived sections:")
			for _, id := range result.ArchivedIDs {
				fmt.Fprintf(stdout, "      - %s\n", id)
			}
		}
	} else {
		fmt.Fprintf(stdout, "No rotation needed for %s: %s\n", result.ChangelogPath, result.Reason)
	}
	return nil
}

func runChangelogListArchives(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc changelog list-archives: unknown flag %q", a)}
			}
			repo = a
		}
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc changelog list-archives: " + err.Error()}
	}

	archives, err := changelog.ListArchives(abs, changelog.Options{})
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc changelog list-archives: " + err.Error()}
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(archives)
	}

	if len(archives) == 0 {
		fmt.Fprintln(stdout, "No archive files found.")
		return nil
	}
	fmt.Fprintf(stdout, "Archives (%d total):\n", len(archives))
	for _, a := range archives {
		fmt.Fprintf(stdout, "  - %s  (%d bytes, modified %s)\n", a.Path, a.SizeBytes, a.ModTime)
	}
	return nil
}

// runAgentIntro prints the embedded reconc agent guide (W11). Designed
// as the one-stop answer to "how does an agent use reconc?".
//
// Modes:
//
//	default:           full markdown guide to stdout
//	--section NAME:    one ## (or ###) section whose heading matches NAME
//	--list-sections:   print top-level headings, one per line
//	--json:            structured payload with body + sections[]
func runAgentIntro(args []string, stdout, stderr io.Writer) error {
	section := ""
	listSections := false
	jsonOut := false
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc agent-intro [--section NAME] [--list-sections] [--json]")
			fmt.Fprintln(stdout, "Print the embedded reconc agent integration guide.")
			return nil
		case "--section":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc agent-intro: --section requires a name argument"}
			}
			section = args[i+1]
			i++
		case "--list-sections":
			listSections = true
		case "--json":
			jsonOut = true
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc agent-intro: unknown flag %q", a)}
			}
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc agent-intro: unexpected argument %q", a)}
		}
		i++
	}

	if listSections {
		sections := agentguide.Sections()
		if jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{"sections": sections})
		}
		for _, s := range sections {
			fmt.Fprintln(stdout, s)
		}
		return nil
	}

	if section != "" {
		body := agentguide.Section(section)
		if body == "" {
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc agent-intro: section %q not found (try --list-sections)", section)}
		}
		if jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{"section": section, "body": body})
		}
		fmt.Fprint(stdout, body)
		if !strings.HasSuffix(body, "\n") {
			fmt.Fprintln(stdout)
		}
		return nil
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"body":     agentguide.Markdown(),
			"sections": agentguide.Sections(),
		})
	}

	fmt.Fprint(stdout, agentguide.Markdown())
	return nil
}

// runAudit implements `reconc audit <tail|stats|export>` (W29).
//
// The audit log is the append-only history of every enforcement
// decision. When enabled (RECONC_AUDIT=1 env or future
// `.reconc.yml: audit.enabled: true`), each check/ci/assert/can call
// appends one JSONL line to .reconc/audit.jsonl. These commands
// consume that log.
//
// Exit codes: 0 ok, 1 error. The log being absent is not an error:
// tail/stats return empty output.
func runAudit(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc audit: missing subcommand (tail | stats | export)"}
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage:")
			fmt.Fprintln(stdout, "  reconc audit tail [repo] [-n N] [--rule ID] [--since RFC3339]")
			fmt.Fprintln(stdout, "                     [--decision pass|warn|block] [--json] [--compact]")
			fmt.Fprintln(stdout, "  reconc audit stats [repo] [--json]")
			fmt.Fprintln(stdout, "  reconc audit export [repo]   # JSONL to stdout")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Enable logging: RECONC_AUDIT=1 reconc check ...")
			fmt.Fprintln(stdout, "Log location:   .reconc/audit.jsonl (repo-local, rotated at 50 MiB)")
			return nil
		}
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "tail":
		return runAuditTail(rest, stdout, stderr)
	case "stats":
		return runAuditStats(rest, stdout, stderr)
	case "export":
		return runAuditExport(rest, stdout, stderr)
	default:
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc audit: unknown subcommand %q (expected tail, stats, or export)", sub)}
	}
}

func runAuditTail(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	compact := false
	opts := audit.TailOptions{N: 20}
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--compact":
			compact = true
		case "-n":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc audit tail: -n requires an integer"}
			}
			n, err := atoi(args[i+1])
			if err != nil || n < 0 {
				return &CLIError{ExitCode: 1, Message: "reconc audit tail: -n must be a non-negative integer"}
			}
			opts.N = n
			i++
		case "--rule":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc audit tail: --rule requires a value"}
			}
			opts.RuleID = args[i+1]
			i++
		case "--since":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc audit tail: --since requires a value"}
			}
			opts.Since = args[i+1]
			i++
		case "--decision":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc audit tail: --decision requires a value"}
			}
			opts.Decision = args[i+1]
			i++
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc audit tail: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc audit tail: " + err.Error()}
	}
	entries, err := audit.Tail(abs, opts)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc audit tail: " + err.Error()}
	}
	if jsonOut && compact {
		return &CLIError{ExitCode: 1, Message: "reconc audit tail: --json and --compact are mutually exclusive"}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "No audit entries.")
		return nil
	}
	if compact {
		for _, e := range entries {
			fmt.Fprintf(stdout, "%s %s %s %s\n", e.Timestamp, e.Event, e.Decision, firstStringOrDash(e.RuleIDs))
		}
		return nil
	}
	for _, e := range entries {
		fmt.Fprintf(stdout, "%s  %-6s  %-5s  rules=%v  paths=%v\n",
			e.Timestamp, e.Event, e.Decision, e.RuleIDs, e.WritePaths)
	}
	return nil
}

func runAuditStats(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc audit stats: unknown flag %q", a)}
			}
			repo = a
		}
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc audit stats: " + err.Error()}
	}
	stats, err := audit.Stats(abs)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc audit stats: " + err.Error()}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(stats)
	}
	if stats.TotalEntries == 0 {
		fmt.Fprintln(stdout, "No audit entries.")
		return nil
	}
	fmt.Fprintf(stdout, "Audit stats (%d entries, %s -> %s):\n",
		stats.TotalEntries, stats.FirstTS, stats.LastTS)
	fmt.Fprintf(stdout, "  Blocking fires: %d\n", stats.BlockingFires)
	if len(stats.ByDecision) > 0 {
		fmt.Fprintln(stdout, "  By decision:")
		for _, k := range sortedKeys(stats.ByDecision) {
			fmt.Fprintf(stdout, "    %-7s %d\n", k+":", stats.ByDecision[k])
		}
	}
	if len(stats.ByEvent) > 0 {
		fmt.Fprintln(stdout, "  By event:")
		for _, k := range sortedKeys(stats.ByEvent) {
			fmt.Fprintf(stdout, "    %-10s %d\n", k+":", stats.ByEvent[k])
		}
	}
	if len(stats.TopRules) > 0 {
		fmt.Fprintln(stdout, "  Top rules:")
		for _, r := range stats.TopRules {
			fmt.Fprintf(stdout, "    %-40s %d\n", r.RuleID, r.Count)
		}
	}
	return nil
}

func runAuditExport(args []string, stdout, stderr io.Writer) error {
	repo := "."
	for _, a := range args {
		if len(a) > 0 && a[0] == '-' {
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc audit export: unknown flag %q", a)}
		}
		repo = a
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc audit export: " + err.Error()}
	}
	if err := audit.ExportJSONL(abs, stdout); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc audit export: " + err.Error()}
	}
	return nil
}

// runTemplate implements `reconc template <list|show>` (W18).
//
// Templates are reusable rule shapes that users can reference by name
// in .reconc.yml via `template: NAME`. At parse time the template's
// fields merge in as defaults. Handy for the same rule pattern across
// many paths (tests-follow-source, docs-follow-code, etc.).
func runTemplate(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc template: missing subcommand (list | show)"}
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage:")
			fmt.Fprintln(stdout, "  reconc template list [--json]")
			fmt.Fprintln(stdout, "  reconc template show <name> [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Templates live in $RECONC_HOME/templates/ (user) and the embedded")
			fmt.Fprintln(stdout, "builtin/ set. User templates override builtins on name collision.")
			return nil
		}
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		return runTemplateList(rest, stdout, stderr)
	case "show":
		return runTemplateShow(rest, stdout, stderr)
	default:
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc template: unknown subcommand %q (expected list or show)", sub)}
	}
}

func runTemplateList(args []string, stdout, stderr io.Writer) error {
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		default:
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc template list: unknown flag %q", a)}
		}
	}
	list, err := templates.List()
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc template list: " + err.Error()}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(list)
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "No templates available.")
		return nil
	}
	fmt.Fprintf(stdout, "Templates (%d total):\n", len(list))
	for _, t := range list {
		fmt.Fprintf(stdout, "  %-30s [%s]  %s\n", t.Name, t.Source, t.Description)
	}
	return nil
}

func runTemplateShow(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc template show: missing <name> argument"}
	}
	name := args[0]
	jsonOut := false
	for _, a := range args[1:] {
		switch a {
		case "--json":
			jsonOut = true
		default:
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc template show: unknown flag %q", a)}
		}
	}
	tmpl, err := templates.Resolve(name)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc template show: " + err.Error()}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(tmpl)
	}
	fmt.Fprintf(stdout, "Template: %s [%s]\n", tmpl.Name, tmpl.Source)
	fmt.Fprintf(stdout, "Source:   %s\n", tmpl.Path)
	fmt.Fprintf(stdout, "About:    %s\n", tmpl.Description)
	fmt.Fprintln(stdout, "Body:")
	for _, k := range sortedMapKeys(tmpl.Body) {
		if k == "description" {
			continue
		}
		fmt.Fprintf(stdout, "  %s: %v\n", k, tmpl.Body[k])
	}
	return nil
}

// runSessionBriefing implements `reconc session-briefing [repo] [--json]` (W44).
//
// One-shot replacement for the multi-file session-start read-list.
// Reads discovery + lockfile + audit-log aggregates and emits a
// compact (~400 token) summary so an agent knows in one decode:
//   - is policy loaded and fresh?
//   - what were the last enforcement decisions?
//   - which rules are firing most?
//   - are there open conflicts?
//
// Intentionally skips project-convention-specific inputs (todo.md,
// spec.md, changelog.md) so the command works in any repo without
// configuration. Those can be added by the caller's wrapper.
func runSessionBriefing(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc session-briefing [repo] [--json]")
			fmt.Fprintln(stdout, "Compact session-start dump: lockfile state + recent audit activity.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc session-briefing: unknown flag %q", a)}
			}
			repo = a
		}
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc session-briefing: " + err.Error()}
	}

	briefing := buildSessionBriefing(abs)

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(briefing)
	}

	// Text form: one-screen compact summary, explicit about what's
	// present vs missing so the agent never has to guess.
	fmt.Fprintf(stdout, "Session briefing for %s\n", briefing["repo_root"])
	fmt.Fprintf(stdout, "  Lockfile:      %s\n", briefing["lockfile_status"])
	if v, ok := briefing["rule_count"].(int); ok && v > 0 {
		fmt.Fprintf(stdout, "  Rules:         %d active, %d sources\n", v, briefing["source_count"])
	}
	if v, ok := briefing["conflicts"].(int); ok && v > 0 {
		fmt.Fprintf(stdout, "  Conflicts:     %d (run `reconc compile` to see)\n", v)
	} else {
		fmt.Fprintln(stdout, "  Conflicts:     none")
	}
	if v, ok := briefing["audit_enabled"].(bool); ok && v {
		fmt.Fprintf(stdout, "  Audit log:     %d entries (%d last hour, %d blocking)\n",
			briefing["audit_total"], briefing["audit_last_hour"], briefing["audit_blocking_24h"])
		if top, ok := briefing["audit_top_rule"].(string); ok && top != "" {
			fmt.Fprintf(stdout, "  Top rule:      %s (%d fires)\n", top, briefing["audit_top_rule_count"])
		}
	} else {
		fmt.Fprintln(stdout, "  Audit log:     not enabled (set RECONC_AUDIT=1 to record)")
	}
	if nextAction, ok := briefing["next_action"].(string); ok && nextAction != "" {
		fmt.Fprintf(stdout, "  Next action:   %s\n", nextAction)
	}
	return nil
}

// buildSessionBriefing collects the facts a session-start agent needs
// in one decode. Returns a map so text + JSON output render from the
// same source.
func buildSessionBriefing(repoRoot string) map[string]interface{} {
	out := map[string]interface{}{
		"repo_root":       repoRoot,
		"lockfile_status": "unknown",
		"audit_enabled":   false,
	}

	discovery, err := ingest.DiscoverPolicyRepo(repoRoot)
	if err != nil {
		out["lockfile_status"] = "discovery error: " + err.Error()
		out["next_action"] = "Fix the discovery error (is this a real directory?)"
		return out
	}
	if !discovery.Discovered {
		out["lockfile_status"] = "no reconc config found"
		out["next_action"] = "run `reconc init " + repoRoot + "` to scaffold a starting config"
		return out
	}
	out["repo_root"] = discovery.RepoRoot

	if discovery.LockfilePath == nil {
		out["lockfile_status"] = "config found but no lockfile"
		out["next_action"] = "run `reconc compile " + repoRoot + "`"
		return out
	}
	lockPath := filepath.Join(discovery.RepoRoot, *discovery.LockfilePath)
	lockInfo, err := os.Stat(lockPath)
	if err != nil {
		out["lockfile_status"] = "lockfile missing: " + err.Error()
		out["next_action"] = "run `reconc compile " + repoRoot + "`"
		return out
	}
	out["lockfile_modified"] = lockInfo.ModTime().UTC().Format(time.RFC3339)

	// Try to read rule_count + source_count from the lockfile (both
	// are already summarised at the top level by the compiler).
	if data, err := os.ReadFile(lockPath); err == nil {
		var payload map[string]interface{}
		if err := json.Unmarshal(data, &payload); err == nil {
			if rc, ok := payload["rule_count"].(float64); ok {
				out["rule_count"] = int(rc)
			}
			if sc, ok := payload["source_count"].(float64); ok {
				out["source_count"] = int(sc)
			}
		}
	}

	if validation, err := validatePolicyReadOnly(discovery.RepoRoot); err != nil {
		out["lockfile_status"] = "source error: " + err.Error()
		out["next_action"] = "fix policy source parsing, then run `reconc compile " + repoRoot + "`"
	} else {
		out["source_count"] = validation.sourceCount
		if payload, err := readLockfileSummary(discovery.RepoRoot); err != nil {
			out["lockfile_status"] = "lockfile unreadable: " + err.Error()
			out["next_action"] = "run `reconc compile " + repoRoot + "`"
		} else if storedDigest, _ := payload["source_digest"].(string); storedDigest == validation.sourceDigest {
			out["lockfile_status"] = "fresh"
		} else {
			out["lockfile_status"] = "stale"
			out["next_action"] = "run `reconc compile " + repoRoot + "`"
		}
		out["conflicts"] = validation.conflicts
	}

	// Audit stats: if the log exists, summarise the last 24 hours.
	if stats, err := audit.Stats(discovery.RepoRoot); err == nil && stats.TotalEntries > 0 {
		out["audit_enabled"] = true
		out["audit_total"] = stats.TotalEntries
		out["audit_blocking_24h"] = stats.BlockingFires
		// Count last-hour entries without re-scanning: use Tail with Since filter.
		since := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
		if hourly, err := audit.Tail(discovery.RepoRoot, audit.TailOptions{Since: since}); err == nil {
			out["audit_last_hour"] = len(hourly)
		} else {
			out["audit_last_hour"] = 0
		}
		if len(stats.TopRules) > 0 {
			out["audit_top_rule"] = stats.TopRules[0].RuleID
			out["audit_top_rule_count"] = stats.TopRules[0].Count
		}
	}

	// Suggest a next action if one is obvious.
	if cnt, ok := out["conflicts"].(int); ok && cnt > 0 {
		out["next_action"] = "address " + itoaCLI(cnt) + " rule conflict(s) (run `reconc compile` for details)"
	}
	return out
}

// runContext implements `reconc context size [repo] [--limit N] [--json]` (W43).
//
// Guards the per-session token budget by reporting the combined size
// of files the agent auto-loads at session start (AGENTS.md,
// docs/changelog.md, etc). Exits 1 when total approx tokens exceed the
// budget so CI gates can block PRs that grow the budget silently.
//
// Subcommand design (instead of a flat `reconc context`) leaves room
// for future related commands (trim, audit-loaded-list, etc) without
// breaking the flag surface.
func runContext(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc context: missing subcommand (size)"}
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage:")
			fmt.Fprintln(stdout, "  reconc context size [repo] [--limit N] [--files PATH[,PATH...]] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Check auto-loaded session files vs a token budget.")
			fmt.Fprintln(stdout, "Default budget: 20000 approximate tokens (bytes / 4).")
			fmt.Fprintln(stdout, "Exit 1 when budget is exceeded.")
			return nil
		}
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "size":
		return runContextSize(rest, stdout, stderr)
	default:
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc context: unknown subcommand %q (expected 'size')", sub)}
	}
}

func runContextSize(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	limit := contextsize.DefaultTokenBudget
	var files []string
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--limit":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc context size: --limit requires an integer"}
			}
			n, err := atoi(args[i+1])
			if err != nil || n <= 0 {
				return &CLIError{ExitCode: 1, Message: "reconc context size: --limit must be a positive integer"}
			}
			limit = n
			i++
		case "--files":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc context size: --files requires a comma-separated list"}
			}
			files = splitCommaList(args[i+1])
			i++
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc context size: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc context size: " + err.Error()}
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return &CLIError{ExitCode: 1, Message: "reconc context size: not a directory: " + abs}
	}

	report := contextsize.Scan(abs, files, limit)

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc context size: json encode: " + err.Error()}
		}
		if report.OverBudget {
			return &CLIError{ExitCode: 1, Message: ""}
		}
		return nil
	}

	// Text: one line of summary, then a size table of files that exist.
	status := "OK"
	if report.OverBudget {
		status = "OVER BUDGET"
	}
	fmt.Fprintf(stdout, "Context size [%s]: %d / %d approx tokens (%d bytes total)\n",
		status, report.TotalApproxTokens, report.TokenBudget, report.TotalBytes)
	if report.Largest != "" {
		fmt.Fprintf(stdout, "Largest: %s\n", report.Largest)
	}
	fmt.Fprintln(stdout, "")
	fmt.Fprintf(stdout, "  %-32s %10s  %10s\n", "file", "bytes", "~tokens")
	for _, f := range report.Files {
		marker := "  "
		if !f.Exists {
			marker = "· " // unicode middle dot for absent entries
		}
		fmt.Fprintf(stdout, "%s%-32s %10d  %10d\n", marker, f.Path, f.SizeBytes, f.ApproxTokens)
	}
	if report.OverBudget {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

// runStart implements `reconc start [repo] [--write PATH] [--json]` (W51).
//
// Renders a canonical, self-contained onboarding / reentry markdown
// document an agent (or a human) can read at session start to know
// exactly where the repo is: compiled rules, recent enforcement
// activity, and where to look for more context. Essentially a
// "welcome + status" page that composes session-briefing + audit
// tail + links to agent-intro.
//
// Two modes:
//
//	reconc start [repo]                   -> stdout
//	reconc start [repo] --write start.md  -> writes to <repo>/start.md
//
// Never overwrites an existing start.md without --force (same safety
// contract as init / hook install).
func runStart(args []string, stdout, stderr io.Writer) error {
	repo := "."
	writePath := ""
	force := false
	jsonOut := false
	minimal := false
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--minimal":
			minimal = true
		case "--force":
			force = true
		case "--write":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc start: --write requires a path"}
			}
			writePath = args[i+1]
			i++
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc start [repo] [--write PATH] [--force] [--json] [--minimal]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Render a canonical start.md with the current repo state.")
			fmt.Fprintln(stdout, "--write PATH    write the rendered doc to PATH under the repo")
			fmt.Fprintln(stdout, "--force         overwrite an existing file at --write")
			fmt.Fprintln(stdout, "--json          emit the structured data behind the doc")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc start: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc start: " + err.Error()}
	}
	data := buildStartData(abs)
	if jsonOut && minimal {
		return &CLIError{ExitCode: 1, Message: "reconc start: --json and --minimal are mutually exclusive"}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}
	md := renderStartMarkdown(data)
	if minimal {
		md = renderStartMinimal(data)
	}
	if writePath != "" {
		target := filepath.Join(abs, writePath)
		if _, err := os.Stat(target); err == nil && !force {
			return &CLIError{ExitCode: 1, Message: target + " already exists; pass --force to overwrite"}
		}
		if err := os.WriteFile(target, []byte(md), 0o644); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc start: write " + target + ": " + err.Error()}
		}
		fmt.Fprintf(stdout, "Wrote %s (%d bytes)\n", target, len(md))
		return nil
	}
	_, _ = stdout.Write([]byte(md))
	return nil
}

// buildStartData gathers the facts start.md needs. Returns a map so
// text + JSON render from the same source of truth.
func buildStartData(repoRoot string) map[string]interface{} {
	briefing := buildSessionBriefing(repoRoot)
	briefing["generated_at"] = time.Now().UTC().Format(time.RFC3339)

	// Recent audit entries: last 5 decisions if the log is enabled.
	if _, ok := briefing["audit_enabled"].(bool); ok {
		if recent, err := audit.Tail(repoRoot, audit.TailOptions{N: 5}); err == nil && len(recent) > 0 {
			lines := make([]string, 0, len(recent))
			for _, e := range recent {
				lines = append(lines, fmt.Sprintf("%s  %s  %s  %v",
					e.Timestamp, e.Event, e.Decision, e.RuleIDs))
			}
			briefing["recent_decisions"] = lines
		}
	}
	return briefing
}

// renderStartMarkdown formats the start-data map as a human / agent
// readable markdown document. Deterministic output for the same data.
func renderStartMarkdown(d map[string]interface{}) string {
	var b strings.Builder
	b.WriteString("# Session Start\n\n")
	b.WriteString("_Auto-generated by `reconc start`. Safe to overwrite; re-run to refresh._\n\n")
	if ts, ok := d["generated_at"].(string); ok {
		b.WriteString("Generated: " + ts + "\n\n")
	}

	b.WriteString("## Repo state\n\n")
	if root, ok := d["repo_root"].(string); ok {
		b.WriteString("- **Root:** `" + root + "`\n")
	}
	if status, ok := d["lockfile_status"].(string); ok {
		b.WriteString("- **Lockfile:** " + status + "\n")
	}
	if rc, ok := d["rule_count"].(int); ok {
		sc, _ := d["source_count"].(int)
		b.WriteString(fmt.Sprintf("- **Rules:** %d active across %d source(s)\n", rc, sc))
	}
	if cnt, ok := d["conflicts"].(int); ok && cnt > 0 {
		b.WriteString(fmt.Sprintf("- **Conflicts:** %d (run `reconc compile` to inspect)\n", cnt))
	} else {
		b.WriteString("- **Conflicts:** none\n")
	}

	b.WriteString("\n## Recent activity\n\n")
	if enabled, _ := d["audit_enabled"].(bool); enabled {
		total, _ := d["audit_total"].(int)
		hour, _ := d["audit_last_hour"].(int)
		blocking, _ := d["audit_blocking_24h"].(int)
		b.WriteString(fmt.Sprintf("- Audit log: %d entries (%d in the last hour, %d blocking)\n",
			total, hour, blocking))
		if top, ok := d["audit_top_rule"].(string); ok && top != "" {
			cnt, _ := d["audit_top_rule_count"].(int)
			b.WriteString(fmt.Sprintf("- Top firing rule: `%s` (%d fires)\n", top, cnt))
		}
		if recent, ok := d["recent_decisions"].([]string); ok && len(recent) > 0 {
			b.WriteString("\nLast 5 decisions:\n")
			for _, line := range recent {
				b.WriteString("- " + line + "\n")
			}
		}
	} else {
		b.WriteString("- Audit log not enabled. Enable with `RECONC_AUDIT=1` to record decisions.\n")
	}

	b.WriteString("\n## Next action\n\n")
	if na, ok := d["next_action"].(string); ok && na != "" {
		b.WriteString(na + "\n")
	} else {
		b.WriteString("None outstanding.\n")
	}

	b.WriteString("\n## Agent orientation\n\n")
	b.WriteString("Run `reconc agent-intro` for the full command + rule-kind reference.\n")
	b.WriteString("Fast-path decisions: `reconc can write <path>` (exit 0/2).\n")
	b.WriteString("Full check: `reconc check . --write <path> --json`.\n")
	return b.String()
}

func renderStartMinimal(d map[string]interface{}) string {
	var b strings.Builder
	root := strOrEmpty(d["repo_root"])
	if root != "" {
		root = filepath.Base(root)
	}
	lockfile := strOrEmpty(d["lockfile_status"])
	if lockfile == "" {
		lockfile = "unknown"
	}
	ruleCount, _ := d["rule_count"].(int)
	sourceCount, _ := d["source_count"].(int)
	conflicts, _ := d["conflicts"].(int)
	fmt.Fprintf(&b, "status: repo=%s lockfile=%s rules=%d sources=%d conflicts=%d\n",
		root, lockfile, ruleCount, sourceCount, conflicts)
	nextAction := "None outstanding."
	if value, ok := d["next_action"].(string); ok && value != "" {
		nextAction = value
	}
	fmt.Fprintf(&b, "next: %s\n", nextAction)
	b.WriteString("more: run `reconc start` for the full guide.\n")
	return b.String()
}

// runPostTaskCheck implements `reconc post-task-check [repo]` (W46).
//
// Pre-done gate for an agent / human to assert before declaring a task
// complete. Reads the audit log for recent blocking activity and
// validates the lockfile is fresh. Exit 1 on any failure so CI /
// hook loops can block task-completion declarations.
//
// Checks (all must pass):
//  1. Repo is discovered and has a compiled lockfile
//  2. Most recent audit entry (if any) is not a blocking decision
//  3. No blocking audit entries in the last N minutes (default 10)
//  4. Optional --require-clean-git: git working tree has no changes
type taskGateCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // OK | FAIL
	Detail string `json:"detail"`
}

type taskGateReport struct {
	RepoRoot string          `json:"repo_root"`
	Checks   []taskGateCheck `json:"checks"`
	OK       bool            `json:"ok"`
}

func buildTaskGateReport(repo string, windowMinutes int, requireCleanGit bool) (taskGateReport, error) {
	abs, err := filepath.Abs(repo)
	if err != nil {
		return taskGateReport{}, err
	}

	report := taskGateReport{
		RepoRoot: abs,
		Checks:   []taskGateCheck{},
		OK:       true,
	}
	addCheck := func(name, status, detail string) {
		report.Checks = append(report.Checks, taskGateCheck{Name: name, Status: status, Detail: detail})
		if status == "FAIL" {
			report.OK = false
		}
	}

	// 1. Lockfile freshness
	discovery, derr := ingest.DiscoverPolicyRepo(abs)
	if derr != nil || !discovery.Discovered {
		addCheck("repo discovered", "FAIL", fmt.Sprintf("%v", derr))
	} else if discovery.LockfilePath == nil {
		addCheck("lockfile fresh", "FAIL", "no lockfile; run `reconc compile`")
	} else {
		validation, verr := validatePolicyReadOnly(discovery.RepoRoot)
		payload, lerr := readLockfileSummary(discovery.RepoRoot)
		if verr != nil {
			addCheck("lockfile fresh", "FAIL", verr.Error())
		} else if lerr != nil {
			addCheck("lockfile fresh", "FAIL", lerr.Error())
		} else if err := validateLockfileRepoRoot(discovery.RepoRoot, payload); err != nil {
			addCheck("lockfile fresh", "FAIL", err.Error())
		} else if storedDigest, _ := payload["source_digest"].(string); storedDigest != validation.sourceDigest {
			addCheck("lockfile fresh", "FAIL", "stale lockfile; run `reconc compile`")
		} else {
			addCheck("lockfile fresh", "OK", *discovery.LockfilePath)
		}
	}

	// 2 + 3. Audit log
	recentBlockCount := 0
	lastDecision := ""
	if discovery.Discovered {
		since := time.Now().Add(-time.Duration(windowMinutes) * time.Minute).UTC().Format(time.RFC3339Nano)
		entries, err := audit.Tail(discovery.RepoRoot, audit.TailOptions{Since: since, Decision: "block"})
		if err == nil {
			recentBlockCount = len(entries)
		}
		all, _ := audit.Tail(discovery.RepoRoot, audit.TailOptions{N: 1})
		if len(all) > 0 {
			lastDecision = all[0].Decision
		}
	}
	if recentBlockCount > 0 {
		addCheck(fmt.Sprintf("no blocks in last %dm", windowMinutes), "FAIL",
			fmt.Sprintf("%d blocking audit entries", recentBlockCount))
	} else {
		addCheck(fmt.Sprintf("no blocks in last %dm", windowMinutes), "OK", "")
	}
	if lastDecision == "block" {
		addCheck("last decision not block", "FAIL", "latest audit entry is a block")
	} else {
		addCheck("last decision not block", "OK", lastDecision)
	}

	// 4. Optional: clean git tree
	if requireCleanGit {
		clean, detail := gitIsClean(abs)
		if clean {
			addCheck("git tree clean", "OK", "")
		} else {
			addCheck("git tree clean", "FAIL", detail)
		}
	}
	return report, nil
}

func runPostTaskCheck(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	requireCleanGit := false
	windowMinutes := 10
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--require-clean-git":
			requireCleanGit = true
		case "--window":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc post-task-check: --window requires minutes"}
			}
			n, err := atoi(args[i+1])
			if err != nil || n <= 0 {
				return &CLIError{ExitCode: 1, Message: "reconc post-task-check: --window must be a positive integer"}
			}
			windowMinutes = n
			i++
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc post-task-check [repo] [--window N] [--require-clean-git] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Pre-done gate: fresh lockfile + no blocking decisions in the last N")
			fmt.Fprintln(stdout, "minutes (default 10). Exit 1 on any failure.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc post-task-check: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	report, err := buildTaskGateReport(repo, windowMinutes, requireCleanGit)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc post-task-check: " + err.Error()}
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		if !report.OK {
			return &CLIError{ExitCode: 1, Message: ""}
		}
		return nil
	}
	for _, c := range report.Checks {
		fmt.Fprintf(stdout, "[%-4s] %-35s %s\n", c.Status, c.Name, c.Detail)
	}
	if !report.OK {
		return &CLIError{ExitCode: 1, Message: "post-task-check failed"}
	}
	fmt.Fprintln(stdout, "All checks passed.")
	return nil
}

func runDone(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	requireCleanGit := false
	windowMinutes := 10
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--require-clean-git":
			requireCleanGit = true
		case "--window":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc done: --window requires minutes"}
			}
			n, err := atoi(val)
			if err != nil || n <= 0 {
				return &CLIError{ExitCode: 1, Message: "reconc done: --window must be a positive integer"}
			}
			windowMinutes = n
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc done [repo] [--window N] [--require-clean-git] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Task-finish gate. Prints `done` when the repo is ready, otherwise")
			fmt.Fprintln(stdout, "`blocked: <next action>`. Exit 0 = done, 2 = blocked, 1 = input error.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc done: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	report, err := buildTaskGateReport(repo, windowMinutes, requireCleanGit)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc done: " + err.Error()}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		if !report.OK {
			return &CLIError{ExitCode: 2, Message: ""}
		}
		return nil
	}
	if report.OK {
		fmt.Fprintln(stdout, "done")
		return nil
	}
	fmt.Fprintf(stdout, "blocked: %s\n", firstFailDetail(report.Checks))
	return &CLIError{ExitCode: 2, Message: ""}
}

func firstFailDetail(checks []taskGateCheck) string {
	for _, c := range checks {
		if c.Status == "FAIL" {
			if c.Detail != "" {
				return c.Detail
			}
			return c.Name
		}
	}
	return "no failed checks"
}

// gitIsClean runs `git status --porcelain` and returns (clean, detail).
// Non-git repos return (true, "not a git repo") so the check is a
// no-op there, keeping the gate useful before a repository is initialized.
func gitIsClean(repoRoot string) (bool, string) {
	gitDir := filepath.Join(repoRoot, ".git")
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		return true, "not a git repo"
	}
	out, err := runGitPorcelain(repoRoot)
	if err != nil {
		return false, "git status failed: " + err.Error()
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return true, ""
	}
	lines := strings.Count(trimmed, "\n") + 1
	return false, fmt.Sprintf("%d unstaged/untracked change(s)", lines)
}

// runGitPorcelain is factored out so tests can replace it if needed.
// Kept minimal: no env scrubbing (git porcelain is read-only) but we
// explicitly set Dir so CI environment doesn't leak.
func runGitPorcelain(repoRoot string) (string, error) {
	cmd := osExecCommand("git", "status", "--porcelain")
	cmd.Dir = repoRoot
	b, err := cmd.Output()
	return string(b), err
}

// osExecCommand is an indirection so the post-task-check test suite
// can stub git calls without actually invoking git binaries in CI.
// Defaults to exec.Command.
var osExecCommand = exec.Command

// runDelta implements `reconc delta [repo] [--since RFC3339] [--json]` (W47).
//
// Shows audit activity since a reference point (default: 1 hour ago).
// Useful at session start to understand "what happened since I
// logged off" without reading the full audit log.
func runDelta(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	since := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--since":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc delta: --since requires an RFC3339 timestamp"}
			}
			since = args[i+1]
			i++
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc delta [repo] [--since RFC3339] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Show audit activity since a reference point (default: 1 hour ago).")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc delta: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc delta: " + err.Error()}
	}
	entries, err := audit.Tail(abs, audit.TailOptions{Since: since})
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc delta: " + err.Error()}
	}

	byDecision := map[string]int{}
	byEvent := map[string]int{}
	for _, e := range entries {
		byDecision[e.Decision]++
		byEvent[e.Event]++
	}

	payload := map[string]interface{}{
		"repo_root":   abs,
		"since":       since,
		"total":       len(entries),
		"by_decision": byDecision,
		"by_event":    byEvent,
		"entries":     entries,
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}
	fmt.Fprintf(stdout, "Delta since %s: %d audit entries\n", since, len(entries))
	if len(byDecision) > 0 {
		fmt.Fprintln(stdout, "  by decision:")
		for _, k := range sortedKeys(byDecision) {
			fmt.Fprintf(stdout, "    %-7s %d\n", k+":", byDecision[k])
		}
	}
	if len(byEvent) > 0 {
		fmt.Fprintln(stdout, "  by event:")
		for _, k := range sortedKeys(byEvent) {
			fmt.Fprintf(stdout, "    %-10s %d\n", k+":", byEvent[k])
		}
	}
	return nil
}

// runSpec implements `reconc spec check [repo] [--file PATH] [--max-age-days N]` (W49).
//
// Verifies that a project-convention specification file exists and is
// reasonably fresh. Default target is docs/spec.md but any path works
// via --file. Exit 1 on missing file or staleness breach.
func runSpec(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc spec: missing subcommand (check)"}
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc spec check [repo] [--file PATH] [--max-age-days N] [--json]")
			fmt.Fprintln(stdout, "Defaults: --file docs/spec.md (no max-age).")
			return nil
		}
	}
	sub := args[0]
	if sub != "check" {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc spec: unknown subcommand %q (expected 'check')", sub)}
	}

	repo := "."
	file := "docs/spec.md"
	maxAgeDays := 0
	jsonOut := false
	rest := args[1:]
	i := 0
	for i < len(rest) {
		a := rest[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--file":
			if i+1 >= len(rest) {
				return &CLIError{ExitCode: 1, Message: "reconc spec check: --file requires a path"}
			}
			file = rest[i+1]
			i++
		case "--max-age-days":
			if i+1 >= len(rest) {
				return &CLIError{ExitCode: 1, Message: "reconc spec check: --max-age-days requires an integer"}
			}
			n, err := atoi(rest[i+1])
			if err != nil || n <= 0 {
				return &CLIError{ExitCode: 1, Message: "reconc spec check: --max-age-days must be a positive integer"}
			}
			maxAgeDays = n
			i++
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc spec check: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc spec check: " + err.Error()}
	}
	target := filepath.Join(abs, file)
	info, err := os.Stat(target)
	result := map[string]interface{}{
		"repo_root": abs,
		"file":      file,
		"exists":    false,
		"stale":     false,
	}
	ok := true
	var reason string
	if err != nil {
		reason = "file not found"
		ok = false
	} else {
		result["exists"] = true
		result["size_bytes"] = info.Size()
		result["modified"] = info.ModTime().UTC().Format(time.RFC3339)
		if maxAgeDays > 0 {
			ageDays := int(time.Since(info.ModTime()).Hours() / 24)
			result["age_days"] = ageDays
			if ageDays > maxAgeDays {
				result["stale"] = true
				reason = fmt.Sprintf("last modified %d days ago (max %d)", ageDays, maxAgeDays)
				ok = false
			}
		}
	}
	result["ok"] = ok
	if reason != "" {
		result["reason"] = reason
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
		if !ok {
			return &CLIError{ExitCode: 1, Message: ""}
		}
		return nil
	}
	if ok {
		fmt.Fprintf(stdout, "[OK  ] %s present\n", file)
		if m, ok2 := result["modified"].(string); ok2 {
			fmt.Fprintf(stdout, "       modified %s\n", m)
		}
		return nil
	}
	fmt.Fprintf(stdout, "[FAIL] %s: %s\n", file, reason)
	return &CLIError{ExitCode: 1, Message: ""}
}

// runCoverage implements `reconc coverage check [repo] [--file PATH] [--min-pct N]` (W50).
//
// Reads a simple coverage file, extracts the first numeric percentage,
// and compares it to --min-pct (default 80). Supports the most common
// artefact formats: "XX.X%" text files, single-line number, `go test
// -cover` "ok  pkg  1.2s  coverage: 87.5% of statements" output.
// Exit 1 on missing file / below minimum.
func runCoverage(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc coverage: missing subcommand (check)"}
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc coverage check [repo] [--file PATH] [--min-pct N] [--json]")
			fmt.Fprintln(stdout, "Defaults: --file coverage.txt, --min-pct 80.")
			fmt.Fprintln(stdout, "Supports text with XX.X%, bare number, or `go test -cover` output.")
			return nil
		}
	}
	sub := args[0]
	if sub != "check" {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc coverage: unknown subcommand %q (expected 'check')", sub)}
	}

	repo := "."
	file := "coverage.txt"
	minPct := 80.0
	jsonOut := false
	rest := args[1:]
	i := 0
	for i < len(rest) {
		a := rest[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--file":
			if i+1 >= len(rest) {
				return &CLIError{ExitCode: 1, Message: "reconc coverage check: --file requires a path"}
			}
			file = rest[i+1]
			i++
		case "--min-pct":
			if i+1 >= len(rest) {
				return &CLIError{ExitCode: 1, Message: "reconc coverage check: --min-pct requires a value"}
			}
			v, err := parseFloatPct(rest[i+1])
			if err != nil {
				return &CLIError{ExitCode: 1, Message: "reconc coverage check: --min-pct must be a number between 0 and 100"}
			}
			minPct = v
			i++
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc coverage check: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc coverage check: " + err.Error()}
	}
	target := filepath.Join(abs, file)
	data, err := os.ReadFile(target)
	if err != nil {
		if jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(map[string]interface{}{
				"repo_root": abs, "file": file, "ok": false,
				"reason": "file not found",
			})
		} else {
			fmt.Fprintf(stdout, "[FAIL] %s: file not found\n", file)
		}
		return &CLIError{ExitCode: 1, Message: ""}
	}
	pct, ok := firstPercent(string(data))
	if !ok {
		if jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(map[string]interface{}{
				"repo_root": abs, "file": file, "ok": false,
				"reason": "no percentage value found in file",
			})
		} else {
			fmt.Fprintf(stdout, "[FAIL] %s: no percentage value found\n", file)
		}
		return &CLIError{ExitCode: 1, Message: ""}
	}

	passed := pct >= minPct
	result := map[string]interface{}{
		"repo_root": abs,
		"file":      file,
		"min_pct":   minPct,
		"found_pct": pct,
		"ok":        passed,
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
		if !passed {
			return &CLIError{ExitCode: 1, Message: ""}
		}
		return nil
	}
	status := "OK  "
	if !passed {
		status = "FAIL"
	}
	fmt.Fprintf(stdout, "[%s] coverage %.1f%% (min %.1f%%)\n", status, pct, minPct)
	if !passed {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

// firstPercent extracts the first float-like number from s. Accepts:
//   - "87.5%"       -> 87.5
//   - "coverage: 87.5% of statements"  -> 87.5
//   - "87.5"        -> 87.5
//
// Returns (pct, true) on success, (0, false) otherwise.
func firstPercent(s string) (float64, bool) {
	// Scan char-by-char looking for a digit run that may contain a
	// decimal point. Stops at first complete number.
	var buf []byte
	seenDigit := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			buf = append(buf, c)
			seenDigit = true
		case c == '.' && seenDigit:
			buf = append(buf, c)
		default:
			if seenDigit {
				return parseFloatSimple(string(buf))
			}
			buf = nil
		}
	}
	if seenDigit {
		return parseFloatSimple(string(buf))
	}
	return 0, false
}

// parseFloatSimple parses a numeric string like "87.5" into a float.
// Uses basic char-walk to avoid a strconv import here (keeping this
// file's dep surface minimal).
func parseFloatSimple(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	var whole, frac int64
	fracDigits := 0
	seenDot := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' {
			if seenDot {
				return 0, false
			}
			seenDot = true
			continue
		}
		if c < '0' || c > '9' {
			return 0, false
		}
		if seenDot {
			frac = frac*10 + int64(c-'0')
			fracDigits++
		} else {
			whole = whole*10 + int64(c-'0')
		}
	}
	result := float64(whole)
	if fracDigits > 0 {
		div := 1.0
		for i := 0; i < fracDigits; i++ {
			div *= 10
		}
		result += float64(frac) / div
	}
	return result, true
}

// parseFloatPct validates a --min-pct argument (0-100, float).
func parseFloatPct(s string) (float64, error) {
	v, ok := parseFloatSimple(s)
	if !ok || v < 0 || v > 100 {
		return 0, fmt.Errorf("invalid percentage %q", s)
	}
	return v, nil
}

// runExtract implements `reconc extract [repo] [--from PATH] [--yaml|--json]` (W20).
//
// Heuristic scan of AGENTS.md / CLAUDE.md prose for concrete rule
// hints (deny-write phrases, generated-file declarations, "run X
// before committing" patterns, secret mentions, CI gating). Emits
// suggestions in the same format as `reconc adopt` so the two
// commands feed the same downstream apply path.
//
// Deterministic. Pure heuristic. No LLM. False negatives by design:
// when in doubt, skip.
func runExtract(args []string, stdout, stderr io.Writer) error {
	repo := "."
	from := ""
	yamlOut := false
	jsonOut := false
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--yaml":
			yamlOut = true
		case "--from":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc extract: --from requires a path"}
			}
			from = args[i+1]
			i++
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc extract [repo] [--from PATH] [--yaml|--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Heuristic scan of AGENTS.md / CLAUDE.md prose for rule hints.")
			fmt.Fprintln(stdout, "Defaults to AGENTS.md; use --from to pick a specific file.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc extract: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}
	if yamlOut && jsonOut {
		return &CLIError{ExitCode: 1, Message: "reconc extract: --yaml and --json are mutually exclusive"}
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc extract: " + err.Error()}
	}

	var contents []byte
	var sourcePath string
	if from != "" {
		sourcePath = from
		contents, err = os.ReadFile(filepath.Join(abs, from))
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc extract: read " + from + ": " + err.Error()}
		}
	} else {
		// Try AGENTS.md then CLAUDE.md.
		for _, candidate := range []string{"AGENTS.md", "CLAUDE.md"} {
			path := filepath.Join(abs, candidate)
			if b, rerr := os.ReadFile(path); rerr == nil {
				contents = b
				sourcePath = candidate
				break
			}
		}
		if sourcePath == "" {
			return &CLIError{ExitCode: 1, Message: "reconc extract: no AGENTS.md or CLAUDE.md found (use --from PATH)"}
		}
	}

	suggestions := extractor.Extract(string(contents))

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]interface{}{
			"repo_root":   abs,
			"source":      sourcePath,
			"suggestions": suggestions,
		})
	}
	if yamlOut {
		fmt.Fprint(stdout, adopt.RenderYAML(adopt.Report{
			RepoRoot:    abs,
			Suggestions: suggestions,
		}))
		return nil
	}

	if len(suggestions) == 0 {
		fmt.Fprintf(stdout, "reconc extract: no rule hints detected in %s\n", sourcePath)
		return nil
	}
	fmt.Fprintf(stdout, "Extracted %d rule hint(s) from %s:\n\n", len(suggestions), sourcePath)
	for i, s := range suggestions {
		fmt.Fprintf(stdout, "%d. %s (%s)\n     %s\n", i+1, s.ID, s.Kind, s.Reason)
		if len(s.Paths) > 0 {
			fmt.Fprintf(stdout, "     -> paths: %s\n", strings.Join(s.Paths, ", "))
		}
		if len(s.Commands) > 0 {
			fmt.Fprintf(stdout, "     -> commands: %s\n", strings.Join(s.Commands, ", "))
		}
		if len(s.Claims) > 0 {
			fmt.Fprintf(stdout, "     -> claims: %s\n", strings.Join(s.Claims, ", "))
		}
		if len(s.Evidence) > 0 {
			fmt.Fprintf(stdout, "     cite:   %s\n", s.Evidence[0])
		}
	}
	fmt.Fprintln(stdout, "\nNext:")
	fmt.Fprintln(stdout, "  - Review each suggestion against the source.")
	fmt.Fprintf(stdout, "  - Preview YAML:   reconc extract %s --yaml\n", abs)
	fmt.Fprintf(stdout, "  - JSON for agent: reconc extract %s --json\n", abs)
	return nil
}

// runDiff implements `reconc diff <lockA> <lockB> [--json]` (W5).
//
// Structural JSON-level comparison of two lockfiles. Matches rules by
// id and reports added / removed / changed, plus default-mode drift
// and source-digest shift. Intended for PR reviews: "what did this
// commit change in the compiled policy?"
func runDiff(args []string, stdout, stderr io.Writer) error {
	jsonOut := false
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc diff <lockfile-a> <lockfile-b> [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Compare two compiled lockfiles. Reports added / removed / changed")
			fmt.Fprintln(stdout, "rules, default-mode drift, source-digest shift.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc diff: unknown flag %q", a)}
			}
			positional = append(positional, a)
		}
	}
	if len(positional) != 2 {
		return &CLIError{ExitCode: 1, Message: "reconc diff: usage: reconc diff <lockfile-a> <lockfile-b>"}
	}
	report, err := lockdiff.Diff(positional[0], positional[1])
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc diff: " + err.Error()}
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Fprintf(stdout, "Diff %s -> %s\n", report.PathA, report.PathB)
	if report.IsEmpty() && !report.DefaultModeDiff && report.DigestA == report.DigestB {
		fmt.Fprintln(stdout, "No changes.")
		return nil
	}
	if report.DefaultModeDiff {
		fmt.Fprintf(stdout, "  default_mode: %s -> %s\n", report.DefaultModeA, report.DefaultModeB)
	}
	if report.DigestA != report.DigestB {
		fmt.Fprintf(stdout, "  source_digest: %s -> %s\n", short12(report.DigestA), short12(report.DigestB))
	}
	if len(report.Added) > 0 {
		fmt.Fprintf(stdout, "\nAdded (%d):\n", len(report.Added))
		for _, r := range report.Added {
			fmt.Fprintf(stdout, "  + %s (%s, %s)\n", r.ID, r.Kind, r.Mode)
		}
	}
	if len(report.Removed) > 0 {
		fmt.Fprintf(stdout, "\nRemoved (%d):\n", len(report.Removed))
		for _, r := range report.Removed {
			fmt.Fprintf(stdout, "  - %s (%s, %s)\n", r.ID, r.Kind, r.Mode)
		}
	}
	if len(report.Changed) > 0 {
		fmt.Fprintf(stdout, "\nChanged (%d):\n", len(report.Changed))
		for _, c := range report.Changed {
			fmt.Fprintf(stdout, "  ~ %s (%s) -- %s\n", c.ID, c.Kind, strings.Join(c.FieldsChanged, ", "))
		}
	}
	if report.Unchanged > 0 {
		fmt.Fprintf(stdout, "\nUnchanged: %d rules\n", report.Unchanged)
	}
	return nil
}

// short12 returns the first 12 chars of a string (typically a hex
// digest) or the whole string if shorter. Keeps diff output tidy.
func short12(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12] + "..."
}

// runWatch implements `reconc watch [repo] [--interval-ms N]` (W6).
//
// Poll-based source watcher: every --interval-ms (default 800) the
// watcher re-scans the policy sources and recompiles if any mtime
// shifted. Purposely poll-based rather than fsnotify-based so we
// don't add a new dep for a dev-convenience command.
//
// Runs forever; exit on Ctrl-C. First recompile happens on startup
// so the first output confirms the watcher is live.
func runWatch(args []string, stdout, stderr io.Writer) error {
	repo := "."
	intervalMS := 800
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--interval-ms":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc watch: --interval-ms requires an integer"}
			}
			n, err := atoi(args[i+1])
			if err != nil || n < 100 {
				return &CLIError{ExitCode: 1, Message: "reconc watch: --interval-ms must be >= 100"}
			}
			intervalMS = n
			i++
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc watch [repo] [--interval-ms N]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Poll policy sources every N ms and recompile when any mtime changes.")
			fmt.Fprintln(stdout, "Exit with Ctrl-C. Default interval: 800 ms.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc watch: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc watch: " + err.Error()}
	}
	discovery, derr := ingest.DiscoverPolicyRepo(abs)
	if derr != nil || !discovery.Discovered {
		return &CLIError{ExitCode: 1, Message: "reconc watch: no reconc config found under " + abs}
	}

	fmt.Fprintf(stdout, "reconc watch: watching %s (poll every %dms, Ctrl-C to exit)\n",
		discovery.RepoRoot, intervalMS)

	// Initial compile so the user gets immediate feedback.
	compileOnce(stdout, stderr, discovery.RepoRoot, "0.1.0-watch")

	lastSig := sourceMTimeSignature(discovery.RepoRoot)
	for {
		time.Sleep(time.Duration(intervalMS) * time.Millisecond)
		sig := sourceMTimeSignature(discovery.RepoRoot)
		if sig == lastSig {
			continue
		}
		lastSig = sig
		compileOnce(stdout, stderr, discovery.RepoRoot, "0.1.0-watch")
	}
}

// compileOnce runs the compiler and prints a tight 1-line status.
// Never returns an error upstream; watch is a best-effort loop.
func compileOnce(stdout, stderr io.Writer, repoRoot, version string) {
	start := time.Now()
	compiled, err := compiler.CompileRepoPolicy(repoRoot, version)
	dur := time.Since(start)
	ts := time.Now().UTC().Format("15:04:05")
	if err != nil {
		fmt.Fprintf(stdout, "[%s] compile failed (%s): %s\n", ts, dur.Round(time.Millisecond), err.Error())
		return
	}
	fmt.Fprintf(stdout, "[%s] compiled %d rules from %d sources in %s\n",
		ts, compiled.RuleCount, compiled.SourceCount, dur.Round(time.Millisecond))
	if len(compiled.Conflicts) > 0 {
		fmt.Fprintf(stdout, "          %d conflict(s): run `reconc compile` for details\n", len(compiled.Conflicts))
	}
}

// sourceMTimeSignature builds a compact deterministic signature of
// every policy-source mtime under repoRoot. When this changes the
// watcher knows to recompile. Cheap: just stat calls on known paths.
func sourceMTimeSignature(repoRoot string) string {
	var b strings.Builder
	candidates := []string{
		"AGENTS.md", "CLAUDE.md", ".reconc.yml",
	}
	policyDir := filepath.Join(repoRoot, "policies")
	entries, _ := os.ReadDir(policyDir)
	for _, e := range entries {
		if !e.IsDir() {
			n := e.Name()
			if strings.HasSuffix(n, ".yml") || strings.HasSuffix(n, ".yaml") {
				candidates = append(candidates, filepath.Join("policies", n))
			}
		}
	}
	for _, rel := range candidates {
		full := filepath.Join(repoRoot, rel)
		if info, err := os.Stat(full); err == nil {
			fmt.Fprintf(&b, "%s=%d;", rel, info.ModTime().UnixNano())
		}
	}
	return b.String()
}

// runManpage emits a groff man(1) page for reconc to stdout.
// Content is generated from the same Subcommands table the shell
// completion uses; the page's date + version header reflect the
// current build.
//
// Install on a typical system:
//
//	reconc manpage | sudo tee /usr/local/share/man/man1/reconc.1
//	sudo mandb  # or `man -w reconc` to verify
func runManpage(args []string, version string, stdout io.Writer) error {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc manpage")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Emit a groff man(1) page on stdout.")
			fmt.Fprintln(stdout, "Install with:")
			fmt.Fprintln(stdout, "  reconc manpage | sudo tee /usr/local/share/man/man1/reconc.1")
			return nil
		}
		if len(a) > 0 && a[0] == '-' {
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc manpage: unknown flag %q", a)}
		}
	}
	return manpage.Render(stdout, version)
}

// runVersion prints the build version. Supports --json for agents.
// Also invoked via the `--version` / `-V` shortcuts so the two paths
// share one implementation.
func runVersion(args []string, version string, stdout io.Writer) error {
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc version [--json]")
			return nil
		}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]string{
			"version":    version,
			"binary":     "reconc",
			"go_runtime": runtimeVersion(),
		})
	}
	fmt.Fprintf(stdout, "reconc %s\n", version)
	return nil
}

// runtimeVersion wraps runtime.Version for dependency-injection in
// tests. Defaults to the actual Go runtime version at build time.
var runtimeVersion = defaultRuntimeVersion

// runCompletion implements `reconc completion <shell>`.
//
// Emits a ready-to-source shell completion script on stdout for
// bash / zsh / fish. Script content is generated from the canonical
// Subcommands table so adding a subcommand requires only one table
// update to keep completion in sync.
func runCompletion(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc completion: missing shell (bash | zsh | fish)"}
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc completion <bash|zsh|fish>")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Emits a shell completion script to stdout.")
			fmt.Fprintln(stdout, "Install:")
			fmt.Fprintln(stdout, "  bash:  reconc completion bash > /usr/local/etc/bash_completion.d/reconc")
			fmt.Fprintln(stdout, "  zsh:   reconc completion zsh  > /usr/local/share/zsh/site-functions/_reconc")
			fmt.Fprintln(stdout, "  fish:  reconc completion fish > ~/.config/fish/completions/reconc.fish")
			return nil
		}
	}
	shell := args[0]
	switch shell {
	case "bash":
		return completion.GenerateBash(stdout)
	case "zsh":
		return completion.GenerateZsh(stdout)
	case "fish":
		return completion.GenerateFish(stdout)
	default:
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc completion: unknown shell %q (expected bash | zsh | fish)", shell)}
	}
}

// defaultRuntimeVersion is the Go runtime version this binary was
// compiled with. Exported indirection so tests can stub it.
func defaultRuntimeVersion() string {
	return goRuntimeVersion
}

// detectCIEnvironment reports whether this process is running inside a
// known CI environment. Used by --auto-claim (W7) to silently assert
// ci-green so hosted pipelines don't need a separate `reconc hook
// claim` step.
//
// Detection is conservative: we only return true for environment
// variables that ONLY CI systems set. A local developer with
// CI_GREEN=true or similar will NOT trigger a false positive.
func detectCIEnvironment() bool {
	// "CI=true" is the cross-provider convention (GitHub Actions,
	// GitLab CI, CircleCI, Travis, Drone, Buildkite, Semaphore, ...).
	if v, ok := os.LookupEnv("CI"); ok {
		vv := strings.ToLower(strings.TrimSpace(v))
		if vv == "1" || vv == "true" || vv == "on" || vv == "yes" {
			return true
		}
	}
	// Provider-specific fallback markers (always set by their
	// respective runners, even when CI= isn't).
	for _, key := range []string{
		"GITHUB_ACTIONS", "GITLAB_CI", "CIRCLECI", "TRAVIS",
		"JENKINS_URL", "BUILDKITE", "DRONE", "APPVEYOR",
		"TEAMCITY_VERSION", "BITBUCKET_BUILD_NUMBER",
	} {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			return true
		}
	}
	return false
}

// splitCommaList splits "a,b,c" into ["a","b","c"] trimming whitespace.
// Returns nil for empty input so the caller falls back to defaults.
func splitCommaList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// itoaCLI is a local int->string used in session-briefing messages.
// Avoids pulling strconv into this file for one format site.
func itoaCLI(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

// sortedMapKeys returns a map's keys alphabetically for stable display.
func sortedMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

// sortedKeys returns a map's string keys sorted ascending. Tiny helper
// used by audit stats for deterministic human output.
func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// sort.Strings lives in "sort"; we keep things dep-free by sorting
	// inline rather than importing sort here (stable for small N).
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

// auditEntryFromReport builds an audit.Entry from a finished
// runtime.CheckReport + the original ExecutionInputs. Captures what
// the evaluator saw and what it decided. Called by runCheck / runCI /
// runAssert only when auditing is enabled.
func auditEntryFromReport(event string, report *runtime.CheckReport, reconcVersion string, start time.Time) audit.Entry {
	ruleIDs := make([]string, 0, len(report.Violations))
	for _, v := range report.Violations {
		ruleIDs = append(ruleIDs, v.RuleID)
	}
	return audit.Entry{
		Event:          event,
		Decision:       string(report.Decision),
		OK:             report.OK,
		RuleIDs:        ruleIDs,
		ViolationCount: report.ViolationCount,
		BlockingCount:  report.BlockingViolationCount,
		WritePaths:     report.Inputs.WritePaths,
		ReadPaths:      report.Inputs.ReadPaths,
		Commands:       report.Inputs.Commands,
		Claims:         report.Inputs.Claims,
		RepoRoot:       report.RepoRoot,
		ReconcVersion:  reconcVersion,
		DurationMs:     time.Since(start).Milliseconds(),
	}
}

// maybeAudit appends one entry to the audit log when logging is
// enabled for the given repo. Non-fatal on error: audit is best-effort
// and must never break the check's exit path.
func maybeAudit(event string, report *runtime.CheckReport, start time.Time) {
	if report == nil {
		return
	}
	// configEnabled=false: only RECONC_AUDIT env can enable today.
	// Once .reconc.yml grows an `audit.enabled` key, thread it through.
	if !audit.Enabled(report.RepoRoot, false) {
		return
	}
	entry := auditEntryFromReport(event, report, "0.1.0-dev", start)
	// Log rotation / append failures to stderr so operators notice, but
	// never fail the user's command -- audit is advisory, not blocking.
	if err := audit.Append(report.RepoRoot, entry, 0); err != nil {
		fmt.Fprintf(os.Stderr, "reconc: audit: %s\n", err)
	}
}

// atoi is a tiny stdlib-free integer parser used by flag handling. No
// strconv pull-in for a one-call site.
func atoi(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n := 0
	neg := false
	start := 0
	if s[0] == '-' {
		neg = true
		start = 1
	} else if s[0] == '+' {
		start = 1
	}
	for i := start; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid integer %q", s)
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

// runBootstrap is the one-shot repo setup: init -> compile ->
// (auto-detected) hook install. Produces a "ready to use" repo from
// a fresh directory in one command.
//
// Behavior:
//   - Init: scaffold .reconc.yml + AGENTS.md (extends default by default)
//   - Compile: build .reconc/policy.lock.json
//   - If .git/ present and --skip-git-hook NOT set: install git pre-commit
//   - If .claude/ / .codex/ present and --skip-agent-hooks NOT set:
//     install the matching agent-platform hook config non-destructively
//     (merges with any existing settings)
//   - Idempotent: re-running bootstrap on an already-initialized repo
//     skips with --force when the user wants to overwrite
//
// Flags mirror init + extra ones for hook control:
//
//	--preset NAME (rep)    same as init
//	--force                same as init (overwrite .reconc.yml)
//	--skip-git-hook        do not install .git/hooks/pre-commit
//	--json                 emit structured JSON instead of text
func runBootstrap(args []string, version string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	skipGitHook := false
	skipAgentHooks := false
	opts := scaffold.Options{}

	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--force":
			opts.Force = true
		case "--skip-git-hook":
			skipGitHook = true
		case "--skip-agent-hooks":
			skipAgentHooks = true
		case "--preset":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc bootstrap: --preset requires a value"}
			}
			opts.Presets = append(opts.Presets, val)
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc setup [repo] [--preset NAME ...] [--force]")
			fmt.Fprintln(stdout, "       reconc bootstrap [repo] [--preset NAME ...] [--force]")
			fmt.Fprintln(stdout, "                              [--skip-git-hook] [--skip-agent-hooks] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "One-shot repo setup: init + compile + platform hook install.")
			fmt.Fprintln(stdout, "- git pre-commit is installed when .git/ is present.")
			fmt.Fprintln(stdout, "- Claude Code / Codex hooks are merged when .claude/ or .codex/ exist.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc bootstrap: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	steps := []string{}
	hints := []string{}

	// Step 1: init
	initReport, err := scaffold.Initialize(repo, opts)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc bootstrap (init): " + err.Error()}
	}
	steps = append(steps, fmt.Sprintf("init: presets=%s, created=%v, updated=%v, skipped=%v",
		joinList(initReport.Presets), initReport.Created, initReport.Updated, initReport.Skipped))

	// Step 2: compile
	compiled, err := compiler.CompileRepoPolicy(initReport.RepoRoot, version)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc bootstrap (compile): " + err.Error()}
	}
	steps = append(steps, fmt.Sprintf("compile: %d rules from %d sources -> %s",
		compiled.RuleCount, compiled.SourceCount, compiled.LockfilePath))

	// Step 3: git pre-commit (only when .git is present)
	gitDirPresent := dirExists(filepath.Join(initReport.RepoRoot, ".git"))
	if gitDirPresent && !skipGitHook {
		hookReport, err := hooks.Install(hooks.KindGitPreCommit, initReport.RepoRoot, true)
		if err != nil {
			steps = append(steps, "hook install git-pre-commit: "+err.Error())
		} else {
			steps = append(steps, fmt.Sprintf("hook install git-pre-commit: %s -> %s", hookReport.Action, hookReport.TargetPath))
		}
	} else if !gitDirPresent {
		hints = append(hints, "no .git/ found - run `git init` then `reconc hook install git-pre-commit` to enable commit-time enforcement")
	}

	// Step 4: auto-install agent hooks when the platform's config dir
	// is already present. Opt-out via --skip-agent-hooks (we keep the
	// bootstrap non-invasive for repos that don't yet use those agents).
	claudeDirPresent := dirExists(filepath.Join(initReport.RepoRoot, ".claude"))
	codexDirPresent := dirExists(filepath.Join(initReport.RepoRoot, ".codex"))
	if claudeDirPresent && !skipAgentHooks {
		if rep, err := hooks.Install(hooks.KindClaudeCode, initReport.RepoRoot, false); err != nil {
			steps = append(steps, "hook install claude-code: "+err.Error())
		} else {
			steps = append(steps, fmt.Sprintf("hook install claude-code: %s -> %s", rep.Action, rep.TargetPath))
		}
	} else if !claudeDirPresent {
		hints = append(hints, "Claude Code: create .claude/ then `reconc hook install claude-code` (or `reconc hook generate claude-code` for manual paste)")
	}
	if codexDirPresent && !skipAgentHooks {
		if rep, err := hooks.Install(hooks.KindCodex, initReport.RepoRoot, false); err != nil {
			steps = append(steps, "hook install codex: "+err.Error())
		} else {
			steps = append(steps, fmt.Sprintf("hook install codex: %s -> %s", rep.Action, rep.TargetPath))
		}
	} else if !codexDirPresent {
		hints = append(hints, "Codex: create .codex/ then `reconc hook install codex` (and set codex_hooks=true in config.toml)")
	}

	if jsonOut {
		payload := map[string]interface{}{
			"repo_root":  initReport.RepoRoot,
			"steps":      steps,
			"next_hints": hints,
			"healthy":    true,
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(payload)
		return nil
	}

	fmt.Fprintf(stdout, "Bootstrapped reconc at %s\n\n", initReport.RepoRoot)
	for i, s := range steps {
		fmt.Fprintf(stdout, "  %d. %s\n", i+1, s)
	}
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Next steps:")
	for _, h := range hints {
		fmt.Fprintf(stdout, "  - %s\n", h)
	}
	return nil
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// runHook implements `reconc hook <generate|install> <kind> [repo]
// [--force] [--json]`. Routes to the hooks package.
func runHook(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc hook: missing subcommand (generate | install | runtime | claim)"}
	}
	switch args[0] {
	case "-h", "--help":
		fmt.Fprintln(stdout, "Usage: reconc hook generate <kind> [--json]")
		fmt.Fprintln(stdout, "       reconc hook install  <kind> [repo] [--force] [--json]")
		fmt.Fprintln(stdout, "       reconc hook runtime  <event> <repo>            (reads stdin JSON)")
		fmt.Fprintln(stdout, "       reconc hook claim    <repo> <claim-name> [--session ID] [--json]")
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "Kinds: git-pre-commit, claude-code, codex (all three are installable)")
		fmt.Fprintln(stdout, "Runtime events: claude-{session-start,pre-tool-use,post-tool-use,")
		fmt.Fprintln(stdout, "                post-tool-use-failure,stop,session-end}")
		fmt.Fprintln(stdout, "                codex-{session-start,pre-tool-use,post-tool-use,stop}")
		return nil
	case "generate":
		return runHookGenerate(args[1:], stdout, stderr)
	case "install":
		return runHookInstall(args[1:], stdout, stderr)
	case "runtime":
		return runHookRuntime(args[1:], stdout, stderr)
	case "claim":
		return runHookClaim(args[1:], stdout, stderr)
	}
	return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook: unknown subcommand %q (expected generate | install | runtime | claim)", args[0])}
}

func runHookGenerate(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook generate: missing kind (one of: %v)", hooks.SupportedKinds())}
	}
	kind := args[0]
	jsonOut := false
	outputPath := ""
	i := 1
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc hook generate: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintf(stdout, "Usage: reconc hook generate <kind> [--json] [--output PATH]\nKinds: %v\n", hooks.SupportedKinds())
			return nil
		default:
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook generate: unknown flag %q", a)}
		}
		i++
	}
	a, err := hooks.Generate(kind)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook generate: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook generate: open output file: " + err.Error()}
	}
	defer func() { _ = closeOutput() }()
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(a); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc hook generate: json encode: " + err.Error()}
		}
		return nil
	}
	// Write the raw artifact content to stdout so users can redirect.
	fmt.Fprint(out, a.Content)
	return nil
}

func runHookInstall(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook install: missing kind (one of: %v)", hooks.InstallableKinds())}
	}
	kind := args[0]
	repo := "."
	force := false
	jsonOut := false
	outputPath := ""
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--force":
			force = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc hook install: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintf(stdout, "Usage: reconc hook install <kind> [repo] [--force] [--json] [--output PATH]\nInstallable: %v\n", hooks.InstallableKinds())
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook install: unknown flag %q", a)}
			}
			repo = a
		}
	}
	report, err := hooks.Install(kind, repo, force)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook install: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook install: open output file: " + err.Error()}
	}
	defer func() { _ = closeOutput() }()
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		return nil
	}
	fmt.Fprintf(out, "Installed %s hook (%s)\n", report.Kind, report.Action)
	fmt.Fprintf(out, "Repo:    %s\n", report.RepoRoot)
	fmt.Fprintf(out, "Target:  %s\n", report.TargetPath)
	fmt.Fprintf(out, "Next:    %s\n", report.NextAction)
	// Surface any user-modified reconc entries that got overwritten so
	// operators notice.
	if len(report.DroppedUserEdits) > 0 {
		fmt.Fprintf(stderr, "reconc hook install: replaced %d user-modified reconc entr(y/ies):\n",
			len(report.DroppedUserEdits))
		for _, e := range report.DroppedUserEdits {
			fmt.Fprintf(stderr, "  - %s\n", e)
		}
		fmt.Fprintln(stderr, "  (If this was intentional, redo the edit via a wrapper command)")
	}
	return nil
}

// runHookRuntime dispatches `reconc hook runtime <event> <repo>` to
// the agent-session adapter. Reads a JSON payload from stdin, runs
// the per-event handler, and translates the Result into exit code +
// stdout/stderr.
//
// Design anchor: the threat model in docs/architecture.md#threat-model-hook-runtime
// specifies the behaviour for every failure mode (fail-closed vs
// fail-open per event, max payload size, depth limits, timeout).
// runHookRuntime is the single enforcement point for those contracts.
func runHookRuntime(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc hook runtime: missing <event> <repo>"}
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc hook runtime <event> <repo>   (reads JSON from stdin)")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Events: claude-session-start, claude-pre-tool-use,")
			fmt.Fprintln(stdout, "        claude-post-tool-use, claude-post-tool-use-failure,")
			fmt.Fprintln(stdout, "        claude-stop, claude-session-end,")
			fmt.Fprintln(stdout, "        codex-session-start, codex-pre-tool-use,")
			fmt.Fprintln(stdout, "        codex-post-tool-use, codex-stop")
			return nil
		}
	}
	if len(args) < 2 {
		return &CLIError{ExitCode: 1, Message: "reconc hook runtime: expected <event> <repo>"}
	}
	event := args[0]
	repo := args[1]

	payload, err := agentsession.ReadPayload(os.Stdin)
	if err != nil {
		return &CLIError{ExitCode: 2, Message: "reconc hook runtime: " + err.Error()}
	}

	var result agentsession.Result
	switch event {
	case "claude-session-start", "codex-session-start":
		result = agentsession.RunSessionStart(repo, payload)
	case "claude-pre-tool-use", "codex-pre-tool-use":
		result = agentsession.RunPreToolUse(repo, payload)
	case "claude-post-tool-use", "codex-post-tool-use":
		result = agentsession.RunPostToolUse(repo, payload)
	case "claude-post-tool-use-failure":
		result = agentsession.RunPostToolUseFailure(repo, payload)
	case "claude-stop", "codex-stop":
		result = agentsession.RunStop(repo, payload)
	case "claude-session-end":
		result = agentsession.RunSessionEnd(repo, payload)
	default:
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook runtime: unknown event %q", event)}
	}

	if result.Stdout != "" {
		fmt.Fprintln(stdout, result.Stdout)
	}
	if result.Stderr != "" {
		fmt.Fprintln(stderr, result.Stderr)
	}
	if result.ExitCode != 0 {
		return &CLIError{ExitCode: result.ExitCode, Message: ""}
	}
	return nil
}

// runHookClaim appends one explicit claim to the active session state.
func runHookClaim(args []string, stdout, stderr io.Writer) error {
	repo := ""
	claim := ""
	sessionID := ""
	jsonOut := false
	outputPath := ""
	positional := 0
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc hook claim <repo> <claim-name> [--session ID] [--json] [--output PATH]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Records a claim (e.g. 'ci-green') in the active session state so")
			fmt.Fprintln(stdout, "subsequent require_claim rules see it.")
			return nil
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc hook claim: --output requires a path"}
			}
			outputPath = val
		case "--session":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc hook claim: --session requires a value"}
			}
			sessionID = args[i+1]
			i++
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook claim: unknown flag %q", a)}
			}
			switch positional {
			case 0:
				repo = a
			case 1:
				claim = a
			default:
				return &CLIError{ExitCode: 1, Message: "reconc hook claim: too many positional arguments (expected <repo> <claim-name>)"}
			}
			positional++
		}
		i++
	}
	if repo == "" || claim == "" {
		return &CLIError{ExitCode: 1, Message: "reconc hook claim: usage: reconc hook claim <repo> <claim-name> [--session ID] [--json]"}
	}

	report, err := agentsession.RecordClaim(repo, claim, sessionID)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook claim: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook claim: open output file: " + err.Error()}
	}
	defer func() { _ = closeOutput() }()

	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	fmt.Fprintln(out, agentsession.DescribeClaimReport(report))
	return nil
}

// runPreset implements `reconc preset <list|show> [name] [--json]`.
func runPreset(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc preset: missing subcommand (list | show)"}
	}
	switch args[0] {
	case "-h", "--help":
		fmt.Fprintln(stdout, "Usage: reconc preset list [--json]")
		fmt.Fprintln(stdout, "       reconc preset show <name>")
		return nil
	case "list":
		return runPresetList(args[1:], stdout, stderr)
	case "show":
		return runPresetShow(args[1:], stdout, stderr)
	}
	return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc preset: unknown subcommand %q", args[0])}
}

func runPresetList(args []string, stdout, stderr io.Writer) error {
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
				return &CLIError{ExitCode: 1, Message: "reconc preset list: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc preset list [--json] [--output PATH]")
			return nil
		default:
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc preset list: unknown flag %q", a)}
		}
		i++
	}
	list, err := presets.List()
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc preset list: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc preset list: open output file: " + err.Error()}
	}
	defer func() { _ = closeOutput() }()
	if jsonOut {
		payload := map[string]interface{}{
			"preset_count": len(list),
			"presets":      list,
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(payload)
		return nil
	}
	if len(list) == 0 {
		fmt.Fprintln(out, "No presets available.")
		return nil
	}
	fmt.Fprintln(out, "Bundled and user presets:")
	for _, p := range list {
		fmt.Fprintf(out, "  %s (%s)  %s\n", p.Name, p.Source, p.Path)
	}
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Extend any of these from .reconc.yml:")
	fmt.Fprintln(out, "  extends: [<name>, ...]")
	return nil
}

func runPresetShow(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc preset show: missing preset name"}
	}
	name := args[0]
	jsonOut := false
	outputPath := ""
	i := 1
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc preset show: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc preset show <name> [--json] [--output PATH]")
			return nil
		default:
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc preset show: unknown flag %q", a)}
		}
		i++
	}
	content, err := presets.Load(name)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc preset show: " + err.Error()}
	}
	path, source, err := presets.Path(name)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc preset show: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc preset show: open output file: " + err.Error()}
	}
	defer func() { _ = closeOutput() }()
	if jsonOut {
		payload := map[string]interface{}{
			"name":    name,
			"path":    path,
			"source":  source,
			"content": content,
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(payload)
		return nil
	}
	// Plain content to stdout so users can redirect into a file.
	fmt.Fprint(out, content)
	return nil
}

// runCI implements `reconc ci [repo] (--staged | --base REF [--head REF])
// [--read PATH ...] [--command CMD ...] [--claim NAME ...] [--json]`.
//
// Derives write_paths from a git diff (staged OR base..head range),
// merges with explicit --read/--command/--claim flags, and runs check.
// Exit codes: 0 = pass/warn, 1 = error, 2 = blocking violation.
//
// The most common shapes:
//   - reconc ci --staged                  (used by git pre-commit hook)
//   - reconc ci --base main               (used by PR / CI pipelines)
//   - reconc ci --base main --head HEAD   (explicit range)
func runCI(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	staged := false
	outputPath := ""
	base := ""
	head := ""
	inputs := runtime.Empty()

	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--staged":
			staged = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --output requires a path"}
			}
			outputPath = val
		case "--base":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --base requires a ref"}
			}
			base = val
		case "--head":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --head requires a ref"}
			}
			head = val
		case "--read":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --read requires a value"}
			}
			inputs.ReadPaths = append(inputs.ReadPaths, val)
		case "--command":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --command requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
		case "--command-success":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --command-success requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
			inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
				Command: val, Outcome: runtime.CommandOutcomeSuccess,
			})
		case "--command-failure":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --command-failure requires a value"}
			}
			inputs.Commands = append(inputs.Commands, val)
			inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
				Command: val, Outcome: runtime.CommandOutcomeFailure,
			})
		case "--claim":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc ci: --claim requires a value"}
			}
			inputs.Claims = append(inputs.Claims, val)
		case "--auto-claim":
			if detectCIEnvironment() {
				inputs.Claims = append(inputs.Claims, "ci-green")
			}
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc ci [repo] (--staged | --base REF [--head REF])")
			fmt.Fprintln(stdout, "                 [--read PATH] [--command CMD] [--command-success CMD]")
			fmt.Fprintln(stdout, "                 [--command-failure CMD] [--claim NAME] [--auto-claim] [--json] [--output PATH]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Derive write_paths from git diff and run policy check.")
			fmt.Fprintln(stdout, "  --staged              git diff --cached --name-only (pre-commit)")
			fmt.Fprintln(stdout, "  --base REF [--head REF]  git diff base...head --name-only (PR/CI)")
			fmt.Fprintln(stdout, "Exit codes: 0 = pass/warn, 1 = error, 2 = blocking violation.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc ci: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	// Need to discover the repo to know what dir to run git from.
	discovery, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc ci: " + err.Error()}
	}
	if !discovery.Discovered {
		return &CLIError{ExitCode: 1, Message: "reconc ci: no policy markers found"}
	}

	gitPaths, gitMeta, err := runtime.CollectGitWritePaths(discovery.RepoRoot, staged, base, head)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc ci: " + err.Error()}
	}
	inputs.WritePaths = append(inputs.WritePaths, gitPaths...)

	startCI := time.Now()
	report, err := runtime.CheckRepoPolicy(repo, inputs)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc ci: " + err.Error()}
	}
	maybeAudit("ci", report, startCI)
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc ci: open output file: " + err.Error()}
	}
	defer func() { _ = closeOutput() }()

	if jsonOut {
		// Embed git metadata into the JSON output for auditability.
		payload := map[string]interface{}{
			"report": report,
			"git":    gitMeta,
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(payload); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc ci: json encode: " + err.Error()}
		}
	} else {
		fmt.Fprintf(out, "Git:       %s (%d path(s))\n", gitMeta.GitCommand, gitMeta.WritePathCount)
		renderCheckText(report, out)
	}

	if report.Decision == runtime.DecisionBlock {
		return &CLIError{ExitCode: 2, Message: ""}
	}
	return nil
}

// runInit implements `reconc init [repo] [--preset NAME ...] [--force] [--json]`.
//
// Scaffolds .reconc.yml + AGENTS.md in a fresh repo. Idempotent for
// AGENTS.md (never overwrites). Refuses to overwrite .reconc.yml
// without --force.
func runInit(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	outputPath := ""
	opts := scaffold.Options{}

	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc init: --output requires a path"}
			}
			outputPath = val
		case "--force":
			opts.Force = true
		case "--preset":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc init: --preset requires a value"}
			}
			opts.Presets = append(opts.Presets, val)
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc init [repo] [--preset NAME ...] [--force] [--json] [--output PATH]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Scaffold a minimal .reconc.yml that extends one or more bundled presets.")
			fmt.Fprintln(stdout, "Also writes a stub AGENTS.md when no entry file (CLAUDE.md / AGENTS.md /")
			fmt.Fprintln(stdout, "start.md) is present. Never overwrites AGENTS.md; refuses to overwrite an")
			fmt.Fprintln(stdout, "existing .reconc.yml without --force.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc init: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	report, err := scaffold.Initialize(repo, opts)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc init: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc init: open output file: " + err.Error()}
	}
	defer func() { _ = closeOutput() }()

	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc init: json encode: " + err.Error()}
		}
		return nil
	}

	fmt.Fprintf(out, "Initialized reconc policy at %s\n", report.RepoRoot)
	fmt.Fprintf(out, "Presets: %s\n", joinList(report.Presets))
	if len(report.Created) > 0 {
		fmt.Fprintf(out, "Created: %s\n", joinList(report.Created))
	}
	if len(report.Updated) > 0 {
		fmt.Fprintf(out, "Updated: %s\n", joinList(report.Updated))
	}
	if len(report.Skipped) > 0 {
		fmt.Fprintf(out, "Skipped: %s\n", joinList(report.Skipped))
	}
	fmt.Fprintf(out, "Next:    %s\n", report.NextAction)
	return nil
}

// runStatus implements `reconc status [repo] [--json]`.
//
// One-line policy health summary. Returns exit 0 always (it's a
// diagnostic, not an enforcement command).
func runStatus(args []string, stdout, stderr io.Writer) error {
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
		bundle, err := ingest.LoadPolicySources(discovery.RepoRoot)
		if err != nil {
			issues = append(issues, err.Error())
		} else {
			sourceCount = len(bundle.Sources)
			if discovery.LockfilePath == nil {
				issues = append(issues, "no lockfile (run `reconc compile`)")
			} else {
				payload, err := readLockfileSummary(discovery.RepoRoot)
				if err != nil {
					issues = append(issues, err.Error())
				} else {
					ruleCount = int(jsonNumberAsIntDefault(payload["rule_count"], 0))
					defaultMode, _ = payload["default_mode"].(string)
					storedDigest, _ := payload["source_digest"].(string)
					liveDigest := compiler.ComputeSourceDigest(bundle)
					if err := validateLockfileRepoRoot(discovery.RepoRoot, payload); err != nil {
						issues = append(issues, err.Error())
					} else if storedDigest == liveDigest {
						lockfileFresh = true
						healthy = true
					} else {
						issues = append(issues, "stale lockfile (run `reconc compile`)")
					}
					if storedSourceCount := int(jsonNumberAsIntDefault(payload["source_count"], 0)); storedSourceCount > 0 {
						sourceCount = storedSourceCount
					}
				}
			}
		}
	}

	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc status: open output file: " + err.Error()}
	}
	defer func() { _ = closeOutput() }()

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
func runTUI(args []string, stdout, stderr io.Writer) error {
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
	defer func() { _ = closeOutput() }()

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
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("lockfile is not valid JSON: %w", err)
	}
	if payload == nil {
		return nil, fmt.Errorf("lockfile must contain a JSON object")
	}
	return payload, nil
}

func validateLockfileRepoRoot(repoRoot string, payload map[string]interface{}) error {
	storedRoot, _ := payload["repo_root"].(string)
	if storedRoot == "" {
		return fmt.Errorf("compiled lockfile repo_root is missing; re-run `reconc compile`")
	}
	if canonicalPathForCompare(storedRoot) != canonicalPathForCompare(repoRoot) {
		return fmt.Errorf("compiled lockfile repo_root does not match the discovered repository root; re-run `reconc compile`")
	}
	return nil
}

func canonicalPathForCompare(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(resolved)
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
	return &readOnlyPolicyValidation{
		ruleCount:    len(parsed.Rules),
		sourceCount:  len(bundle.Sources),
		sourceDigest: compiler.ComputeSourceDigest(bundle),
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

// joinList joins string slice with ", " separator.
func joinList(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
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

func printUsage(w io.Writer, version string) {
	fmt.Fprintf(w, `reconc %s -- Repository Control Compiler

Usage:
  reconc [flags] <subcommand> [args...]

Flags:
  --version, -V    Print version and exit
  --help, -h       Print this help and exit

Daily:
  setup            friendly bootstrap alias for new repos
  status           one-line policy health summary
  check            evaluate runtime evidence against compiled policy
  next             show the next remediation
  done             task-finish gate: prints done or blocked

Setup & inspection:
  init             Scaffold .reconc.yml (and stub AGENTS.md) for a fresh repo
  adopt            Scan repo for tooling and suggest matching rules
  extract          Heuristic scan of AGENTS.md/CLAUDE.md prose for rule hints
  doctor           Inspect discovery and validation state
  verify           End-to-end setup health check ($RECONC_HOME, repo, lockfile, hook)

Compile & evaluate:
  compile          Compile policy sources into .reconc/policy.lock.json
  ci               Derive write_paths from git diff and run check
  assert           Evaluate one rule by id with --var key=value substitution
  can              Ultra-terse yes/no for an action (e.g. 'reconc can write src/app.go')
  diff             Compare two compiled lockfiles (added / removed / changed rules)
  watch            Recompile on source-file changes (exits on Ctrl-C)

Explain & remediate:
  explain          Render a check report in text or markdown
  why              Print the full details of one compiled rule

Packs & wiring:
  preset           list / show bundled and user presets
  template         list / show bundled and user rule templates (W18)
  hook             generate / install / claim platform hooks (git / claude-code / codex)

Workflow maintenance:
  changelog        rotate docs/changelog.md / list-archives
  agent-intro      print the embedded reconc agent integration guide
  audit            tail / stats / export the enforcement decision log
  session-briefing token-efficient session-start dump (lockfile + audit)
  context          size check for auto-loaded files vs a token budget
  start            render / write a canonical start.md onboarding doc
  post-task-check  pre-done gate: fresh lockfile + no recent blocks
  delta            show audit + policy changes since a point in time
  spec             check docs/spec.md presence + freshness
  coverage         check a coverage percentage against a minimum
  tui              terminal dashboard for policy / rules / audit / session

Meta:
  version          print the build version
  completion       emit shell completion script (bash / zsh / fish)
  manpage          emit a groff man(1) page for reconc(1)

reconc is the standalone Go implementation in this repository.
`, version)
}
