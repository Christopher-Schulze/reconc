package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"reconc.dev/reconc/internal/agentguide"
	"reconc.dev/reconc/internal/audit"
	"reconc.dev/reconc/internal/changelog"
	"reconc.dev/reconc/internal/retention"
	"reconc.dev/reconc/internal/runtime/agentsession"
	"strings"
	"time"
)

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

// runPrune performs the same bounded cleanup that SessionStart/SessionEnd
// invoke on a six-hour interval. The explicit CLI always runs immediately.
func runPrune(args []string, stdout io.Writer) error {
	repo := "."
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
			fmt.Fprintln(stdout, "Usage: reconc prune [repo] [--dry-run] [--json]")
			fmt.Fprintln(stdout, "Bound sessions, reports, locks, audit/run JSONL, generated audit binaries, and owned temp residue.")
			return nil
		default:
			if strings.HasPrefix(arg, "-") {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc prune: unknown flag %q", arg)}
			}
			repo = arg
		}
	}
	root, err := agentsession.ResolveRepoRoot(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc prune: " + err.Error()}
	}
	active, _ := agentsession.ResolveActiveSessionID(root)
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
		fmt.Fprintf(stdout, "budgets: state=%d/%dB repo=%d/%dB temp=%d/%dB\n", report.StateBytesAfter, report.StateByteBudget, report.RepoBytesAfter, report.RepoByteBudget, report.OwnedTempBytes, report.OwnedTempBudget)
	}
	if len(report.Errors) > 0 {
		return &CLIError{ExitCode: 1, Message: "reconc prune: " + strings.Join(report.Errors, "; ")}
	}
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

// runDelta implements `reconc delta [repo] [--since RFC3339] [--json]`.
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
