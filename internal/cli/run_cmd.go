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

// runRunControl implements the canonical AI-operated repository run switch.
func runRunControl(args []string, stdout, stderr io.Writer) error {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			printRunControlHelp(stdout)
			return nil
		}
	}
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc run: missing subcommand (on | off | status | log)"}
	}
	switch args[0] {
	case "on":
		return runRunSwitch(args[1:], true, stdout)
	case "off":
		return runRunSwitch(args[1:], false, stdout)
	case "status":
		return runRunStatus(args[1:], stdout, stderr)
	case "log":
		return runRunLog(args[1:], stdout, stderr)
	default:
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc run: unknown subcommand %q (expected on, off, status, or log)", args[0])}
	}
}

func printRunControlHelp(stdout io.Writer) {
	fmt.Fprintln(stdout, "Usage:")
	fmt.Fprintln(stdout, "  reconc run on [repo] [--json]")
	fmt.Fprintln(stdout, "  reconc run off [repo] [--json]")
	fmt.Fprintln(stdout, "  reconc run status [repo] [--json]")
	fmt.Fprintln(stdout, "  reconc run log [repo] [-n N] [--branch B] [--session S] [--follow] [--json]")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "AI-operated repository run control. On keeps every supported agent runtime")
	fmt.Fprintln(stdout, "working while the typed TASK plane has executable work; off releases Stop.")
}

func runRunSwitch(args []string, enabled bool, stdout io.Writer) error {
	repo := "."
	jsonOut := false
	repoSeen := false
	for _, arg := range args {
		switch {
		case arg == "--json":
			jsonOut = true
		case strings.HasPrefix(arg, "-"):
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc run: unknown flag %q", arg)}
		case repoSeen:
			return &CLIError{ExitCode: 1, Message: "reconc run: expected at most one repo path"}
		default:
			repo = arg
			repoSeen = true
		}
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc run: " + err.Error()}
	}
	info, err := agentsession.SetRepositoryRun(abs, enabled)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc run: " + err.Error()}
	}
	if jsonOut {
		body, err := json.Marshal(info)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc run: " + err.Error()}
		}
		fmt.Fprintln(stdout, string(body))
		return nil
	}
	fmt.Fprintln(stdout, formatRunStatus(info))
	return nil
}

func runRunStatus(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	repoSeen := false
	for _, a := range args {
		switch {
		case a == "--json":
			jsonOut = true
		case len(a) > 0 && a[0] == '-':
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc run status: unknown flag %q", a)}
		case repoSeen:
			return &CLIError{ExitCode: 1, Message: "reconc run status: expected at most one repo path"}
		default:
			repo = a
			repoSeen = true
		}
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc run status: " + err.Error()}
	}
	info, err := agentsession.ReadRepositoryRunStatus(abs)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc run status: " + err.Error()}
	}
	if jsonOut {
		body, err := json.Marshal(info)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc run status: " + err.Error()}
		}
		fmt.Fprintln(stdout, string(body))
		return nil
	}
	fmt.Fprintln(stdout, formatRunStatus(info))
	return nil
}

func runRunLog(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	follow := false
	branch := ""
	session := ""
	n := 20
	i := 0
	repoSeen := false
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--follow", "-f":
			follow = true
		case "-n":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc run log: -n requires an integer"}
			}
			v, err := atoi(args[i+1])
			if err != nil || v < 0 {
				return &CLIError{ExitCode: 1, Message: "reconc run log: -n must be a non-negative integer"}
			}
			n = v
			i++
		case "--branch":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc run log: --branch requires a value"}
			}
			branch = args[i+1]
			i++
		case "--session":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc run log: --session requires a value"}
			}
			session = args[i+1]
			i++
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc run log: unknown flag %q", a)}
			}
			if repoSeen {
				return &CLIError{ExitCode: 1, Message: "reconc run log: expected at most one repo path"}
			}
			repo = a
			repoSeen = true
		}
		i++
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc run log: " + err.Error()}
	}

	decisions, err := agentsession.ReadRunDecisions(abs, 0)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc run log: " + err.Error()}
	}
	filtered := filterRunDecisions(decisions, branch, session)
	if n > 0 && len(filtered) > n {
		filtered = filtered[len(filtered)-n:]
	}
	for _, d := range filtered {
		writeRunDecision(stdout, d, jsonOut)
	}
	if !follow {
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return followRunLog(ctx, abs, branch, session, jsonOut, 500*time.Millisecond, stdout)
}

// followRunLog tails decisions.jsonl by byte offset and renders new
// records as they are appended, until ctx is cancelled (Ctrl-C / SIGTERM).
// pollInterval is injectable so tests can drive the live tail deterministically.
func followRunLog(ctx context.Context, repoRoot, branch, session string, jsonOut bool, pollInterval time.Duration, stdout io.Writer) error {
	path, err := agentsession.RunDecisionLogPath(repoRoot)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc run log: " + err.Error()}
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
			var d agentsession.RunDecision
			if json.Unmarshal([]byte(line), &d) != nil {
				continue
			}
			if !decisionMatches(d, branch, session) {
				continue
			}
			writeRunDecision(stdout, d, jsonOut)
		}
	}
}

func writeRunDecision(stdout io.Writer, d agentsession.RunDecision, jsonOut bool) {
	if jsonOut {
		if body, err := json.Marshal(d); err == nil {
			fmt.Fprintln(stdout, string(body))
		}
		return
	}
	fmt.Fprintln(stdout, formatRunDecision(d))
}

func filterRunDecisions(in []agentsession.RunDecision, branch, session string) []agentsession.RunDecision {
	if branch == "" && session == "" {
		return in
	}
	out := make([]agentsession.RunDecision, 0, len(in))
	for _, d := range in {
		if decisionMatches(d, branch, session) {
			out = append(out, d)
		}
	}
	return out
}

func decisionMatches(d agentsession.RunDecision, branch, session string) bool {
	if branch != "" && !strings.Contains(d.Branch, branch) {
		return false
	}
	if session != "" && !strings.Contains(d.SessionID, session) {
		return false
	}
	return true
}

func formatRunStatus(info agentsession.RepositoryRunStatus) string {
	reason := info.DisabledReason
	if reason == "" {
		reason = "-"
	}
	status := fmt.Sprintf("run: enabled=%v task=%s/%s open=%d awaiting=%v nudges=%d reason=%s",
		info.Enabled, dash(info.TaskDisposition), dash(info.TaskID), info.OpenTasks,
		info.AwaitingContinuation, info.NoProgressNudges, reason)
	if info.Blocker != "" {
		status += fmt.Sprintf(" blocker=%q", info.Blocker)
	}
	return status
}

func formatRunDecision(d agentsession.RunDecision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s/%s  rt=%s  en=%v->%v  await=%v->%v  reason=%s  sess=%s",
		dash(d.Timestamp), dash(d.Event), dash(d.Branch), dash(d.Runtime),
		d.EnabledBefore, d.EnabledAfter, d.AwaitingContinuationBefore, d.AwaitingContinuationAfter,
		dash(d.DisabledReasonAfter), shortID(d.SessionID))
	if d.PolicyBlocked {
		fmt.Fprintf(&b, "  [policy_block viol=%d]", d.ViolationCount)
	}
	if d.StopHookActive {
		b.WriteString("  [stop_hook_active]")
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
