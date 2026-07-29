package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"reconc.dev/reconc/internal/agentguide"
	"reconc.dev/reconc/internal/audit"
	"reconc.dev/reconc/internal/retention"
	"reconc.dev/reconc/internal/runtime/agentsession"
	"strings"
)

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

// runPrune performs the same bounded cleanup that SessionStart/SessionEnd
// invoke on a six-hour interval. The explicit CLI always runs immediately.
func runPrune(args []string, stdout io.Writer) error {
	repo := "."
	repoSet := false
	dryRun := false
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--dry-run":
			dryRun = true
		case "--json":
			jsonOut = true
		case "--force":
			// Compatibility with the old harness utility. Explicit prune is
			// already immediate, so --force has no additional effect.
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc prune [repo] [--dry-run] [--json] [--force]")
			fmt.Fprintln(stdout, "Bound project state, sessions, reports, locks, audit/run JSONL, generated audit binaries, and owned temp residue.")
			fmt.Fprintln(stdout, "--force is accepted only as a compatibility no-op; explicit prune already runs immediately.")
			return nil
		default:
			if strings.HasPrefix(arg, "-") {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc prune: unknown flag %q", arg)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc prune: expected at most one repo path"}
			}
			repo = arg
			repoSet = true
		}
	}
	root, err := agentsession.ResolveRepoRoot(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc prune: " + err.Error()}
	}
	active, err := agentsession.ResolveActiveSessionID(root)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc prune: resolve active session: " + err.Error()}
	}
	report := retention.Run(retention.Options{
		RepoRoot:      root,
		StateRoot:     retention.ResolveStateRoot(),
		ActiveSession: active,
		DryRun:        dryRun,
	})
	if jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc prune: encode report: " + err.Error()}
		}
	} else {
		verb := "pruned"
		if dryRun {
			verb = "would prune"
		}
		for _, class := range report.Classes {
			fmt.Fprintf(stdout, "%s %s: deleted=%d freed=%dB kept=%d after=%dB\n", verb, class.Name, class.FilesDeleted, class.BytesFreed, class.FilesKept, class.BytesAfter)
		}
		fmt.Fprintf(stdout, "budgets: projects=%d/%dB state=%d/%dB repo=%d/%dB temp=%d/%dB\n", report.ProjectStateBytes, report.ProjectStateBudget, report.StateBytesAfter, report.StateByteBudget, report.RepoBytesAfter, report.RepoByteBudget, report.OwnedTempBytes, report.OwnedTempBudget)
	}
	if len(report.Errors) > 0 {
		return &CLIError{ExitCode: 1, Message: "reconc prune: " + strings.Join(report.Errors, "; ")}
	}
	return nil
}

// runAudit implements `reconc audit <tail|stats|export|verify>` (W29).
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
		return &CLIError{ExitCode: 1, Message: "reconc audit: missing subcommand (tail | stats | export | verify)"}
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage:")
			fmt.Fprintln(stdout, "  reconc audit tail [repo] [-n N] [--rule ID] [--since RFC3339]")
			fmt.Fprintln(stdout, "                     [--decision pass|warn|block] [--json] [--compact]")
			fmt.Fprintln(stdout, "  reconc audit stats [repo] [--json]")
			fmt.Fprintln(stdout, "  reconc audit export [repo]   # JSONL to stdout")
			fmt.Fprintln(stdout, "  reconc audit verify [repo] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Enable logging: RECONC_AUDIT=1 reconc check ...")
			fmt.Fprintln(stdout, "Log location:   .reconc/audit.jsonl (repo-local, 2 MiB live + two archives)")
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
	case "verify":
		return runAuditVerify(rest, stdout)
	default:
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc audit: unknown subcommand %q (expected tail, stats, export, or verify)", sub)}
	}
}

func runAuditTail(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	compact := false
	opts := audit.TailOptions{N: 20}
	i := 0
	repoSeen := false
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
			if repoSeen {
				return &CLIError{ExitCode: 1, Message: "reconc audit tail: expected at most one repo path"}
			}
			repo = a
			repoSeen = true
		}
		i++
	}
	if jsonOut && compact {
		return &CLIError{ExitCode: 1, Message: "reconc audit tail: --json and --compact are mutually exclusive"}
	}
	if opts.Decision != "" && opts.Decision != "pass" && opts.Decision != "warn" && opts.Decision != "block" {
		return &CLIError{ExitCode: 1, Message: "reconc audit tail: --decision must be pass, warn, or block"}
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc audit tail: " + err.Error()}
	}
	entries, err := audit.Tail(abs, opts)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc audit tail: " + err.Error()}
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
	repoSeen := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc audit stats: unknown flag %q", a)}
			}
			if repoSeen {
				return &CLIError{ExitCode: 1, Message: "reconc audit stats: expected at most one repo path"}
			}
			repo = a
			repoSeen = true
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
	repoSeen := false
	for _, a := range args {
		if len(a) > 0 && a[0] == '-' {
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc audit export: unknown flag %q", a)}
		}
		if repoSeen {
			return &CLIError{ExitCode: 1, Message: "reconc audit export: expected at most one repo path"}
		}
		repo = a
		repoSeen = true
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

func runAuditVerify(args []string, stdout io.Writer) error {
	repo := "."
	jsonOut := false
	repoSeen := false
	for _, arg := range args {
		switch {
		case arg == "--json":
			jsonOut = true
		case strings.HasPrefix(arg, "-"):
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc audit verify: unknown flag %q", arg)}
		case repoSeen:
			return &CLIError{ExitCode: 1, Message: "reconc audit verify: expected at most one repo path"}
		default:
			repo = arg
			repoSeen = true
		}
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc audit verify: " + err.Error()}
	}
	report, err := audit.Verify(abs)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc audit verify: " + err.Error()}
	}
	if jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc audit verify: encode report: " + err.Error()}
		}
		return nil
	}
	if report.Entries == 0 {
		fmt.Fprintln(stdout, "audit: valid, no retained entries")
		return nil
	}
	fmt.Fprintf(stdout, "audit: valid entries=%d sequence=%d..%d digest=%s\n",
		report.Entries, report.FirstSequence, report.LastSequence, report.LastDigest)
	return nil
}
