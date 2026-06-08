package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

// runDegenmode implements `reconc degenmode <status|log>` - a read-only view
// over the append-only degenmode state + decision log. It never writes and is
// a separate process from the hooks, so it can never slow down or block the
// degenmode/reconc control flow.
func runDegenmode(args []string, stdout, stderr io.Writer) error {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage:")
			fmt.Fprintln(stdout, "  reconc degenmode status [repo] [--json]")
			fmt.Fprintln(stdout, "  reconc degenmode log [repo] [-n N] [--branch B] [--session S] [--follow] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Read-only view of degenmode state and the decision log")
			fmt.Fprintln(stdout, "(.reconc/degenmode/state.json + decisions.jsonl). --follow tails live.")
			return nil
		}
	}
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc degenmode: missing subcommand (status | log)"}
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "status":
		return runDegenmodeStatus(rest, stdout, stderr)
	case "log":
		return runDegenmodeLog(rest, stdout, stderr)
	default:
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc degenmode: unknown subcommand %q (expected status or log)", sub)}
	}
}

func runDegenmodeStatus(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	for _, a := range args {
		switch {
		case a == "--json":
			jsonOut = true
		case len(a) > 0 && a[0] == '-':
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc degenmode status: unknown flag %q", a)}
		default:
			repo = a
		}
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc degenmode status: " + err.Error()}
	}
	info, err := agentsession.ReadDegenModeStatus(abs)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc degenmode status: " + err.Error()}
	}
	if jsonOut {
		body, err := json.Marshal(info)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc degenmode status: " + err.Error()}
		}
		fmt.Fprintln(stdout, string(body))
		return nil
	}
	fmt.Fprintln(stdout, formatDegenModeStatus(info))
	return nil
}

func runDegenmodeLog(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	follow := false
	branch := ""
	session := ""
	n := 20
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--follow", "-f":
			follow = true
		case "-n":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc degenmode log: -n requires an integer"}
			}
			v, err := atoi(args[i+1])
			if err != nil || v < 0 {
				return &CLIError{ExitCode: 1, Message: "reconc degenmode log: -n must be a non-negative integer"}
			}
			n = v
			i++
		case "--branch":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc degenmode log: --branch requires a value"}
			}
			branch = args[i+1]
			i++
		case "--session":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc degenmode log: --session requires a value"}
			}
			session = args[i+1]
			i++
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc degenmode log: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc degenmode log: " + err.Error()}
	}

	decisions, err := agentsession.ReadDegenModeDecisions(abs, 0)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc degenmode log: " + err.Error()}
	}
	filtered := filterDegenModeDecisions(decisions, branch, session)
	if n > 0 && len(filtered) > n {
		filtered = filtered[len(filtered)-n:]
	}
	for _, d := range filtered {
		writeDegenModeDecision(stdout, d, jsonOut)
	}
	if !follow {
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return followDegenModeLog(ctx, abs, branch, session, jsonOut, 500*time.Millisecond, stdout)
}

// followDegenModeLog tails decisions.jsonl by byte offset and renders new
// records as they are appended, until ctx is cancelled (Ctrl-C / SIGTERM).
// pollInterval is injectable so tests can drive the live tail deterministically.
func followDegenModeLog(ctx context.Context, repoRoot, branch, session string, jsonOut bool, pollInterval time.Duration, stdout io.Writer) error {
	path, err := agentsession.DegenModeDecisionLogPath(repoRoot)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc degenmode log: " + err.Error()}
	}
	var offset int64
	if fi, statErr := os.Stat(path); statErr == nil {
		offset = fi.Size()
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		fi, statErr := os.Stat(path)
		if statErr != nil {
			continue
		}
		size := fi.Size()
		if size < offset {
			offset = 0 // file rotated/truncated; re-read from start
		}
		if size == offset {
			continue
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			continue
		}
		if _, seekErr := file.Seek(offset, io.SeekStart); seekErr != nil {
			file.Close()
			continue
		}
		data, readErr := io.ReadAll(file)
		file.Close()
		if readErr != nil {
			continue
		}
		offset = size
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var d agentsession.DegenModeDecision
			if json.Unmarshal([]byte(line), &d) != nil {
				continue
			}
			if !decisionMatches(d, branch, session) {
				continue
			}
			writeDegenModeDecision(stdout, d, jsonOut)
		}
	}
}

func writeDegenModeDecision(stdout io.Writer, d agentsession.DegenModeDecision, jsonOut bool) {
	if jsonOut {
		if body, err := json.Marshal(d); err == nil {
			fmt.Fprintln(stdout, string(body))
		}
		return
	}
	fmt.Fprintln(stdout, formatDegenModeDecision(d))
}

func filterDegenModeDecisions(in []agentsession.DegenModeDecision, branch, session string) []agentsession.DegenModeDecision {
	if branch == "" && session == "" {
		return in
	}
	out := make([]agentsession.DegenModeDecision, 0, len(in))
	for _, d := range in {
		if decisionMatches(d, branch, session) {
			out = append(out, d)
		}
	}
	return out
}

func decisionMatches(d agentsession.DegenModeDecision, branch, session string) bool {
	if branch != "" && !strings.Contains(d.Branch, branch) {
		return false
	}
	if session != "" && !strings.Contains(d.SessionID, session) && !strings.Contains(d.ActiveRunID, session) && !strings.Contains(d.StateSessionID, session) {
		return false
	}
	return true
}

func formatDegenModeStatus(info agentsession.DegenModeStatusInfo) string {
	reason := info.DisabledReason
	if reason == "" {
		reason = "-"
	}
	return fmt.Sprintf("degenmode: enabled=%v runtime=%s active_run=%s awaiting=%v nudges=%d stopfile=%v reason=%s",
		info.Enabled, dash(info.Runtime), shortID(info.ActiveRunID), info.AwaitingContinuation, info.NoProgressNudges, info.StopFilePresent, reason)
}

func formatDegenModeDecision(d agentsession.DegenModeDecision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s/%s  rt=%s  en=%v->%v  await=%v->%v  reason=%s  sess=%s",
		dash(d.Timestamp), dash(d.Event), dash(d.Branch), dash(d.Runtime),
		d.EnabledBefore, d.EnabledAfter, d.AwaitingContinuationBefore, d.AwaitingContinuationAfter,
		dash(d.DisabledReasonAfter), shortID(firstNonEmpty(d.SessionID, d.ActiveRunID, d.StateSessionID)))
	if d.PolicyBlocked {
		fmt.Fprintf(&b, "  [policy_block viol=%d]", d.ViolationCount)
	}
	if d.StopFileApplies {
		b.WriteString("  [stop_file]")
	}
	if d.StopHookActive {
		b.WriteString("  [stop_hook_active]")
	}
	if d.OpenCodeContinuationDriver {
		b.WriteString("  [opencode_driver]")
	}
	if d.RuntimeInternalPrompt {
		b.WriteString("  [internal_prompt]")
	}
	if d.Intent == "enable" {
		b.WriteString("  [intent=enable]")
	}
	return b.String()
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func shortID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
