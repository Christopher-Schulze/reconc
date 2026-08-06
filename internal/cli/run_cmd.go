package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	policyruntime "reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
	"reconc.dev/reconc/internal/tasklifecycle"
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
		return &CLIError{ExitCode: 1, Message: "reconc run: missing subcommand (on | off | reset | status | log)"}
	}
	switch args[0] {
	case "on":
		return runRunSwitch(args[1:], true, stdout)
	case "off":
		return runRunSwitch(args[1:], false, stdout)
	case "reset":
		return runRunReset(args[1:], stdout)
	case "status":
		return runRunStatus(args[1:], stdout, stderr)
	case "log":
		return runRunLog(args[1:], stdout, stderr)
	default:
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc run: unknown subcommand %q (expected on, off, reset, status, or log)", args[0])}
	}
}

func printRunControlHelp(stdout io.Writer) {
	fmt.Fprintln(stdout, "Usage:")
	fmt.Fprintln(stdout, "  reconc run on [repo] [--force] [--json]")
	fmt.Fprintln(stdout, "  reconc run off [repo] [--json]")
	fmt.Fprintln(stdout, "  reconc run reset [repo] [--json]")
	fmt.Fprintln(stdout, "  reconc run status [repo] [--verbose | --json]")
	fmt.Fprintln(stdout, "  reconc run log [repo] [-n N] [--branch B] [--session S] [--follow] [--json]")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "AI-operated repository run control. On keeps every supported agent runtime")
	fmt.Fprintln(stdout, "working while the typed TASK plane has executable work; off releases Stop.")
}

func runRunSwitch(args []string, enabled bool, stdout io.Writer) error {
	repo := "."
	jsonOut := false
	force := false
	repoSeen := false
	for _, arg := range args {
		switch {
		case arg == "--json":
			jsonOut = true
		case arg == "--force":
			force = true
		case strings.HasPrefix(arg, "-"):
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc run: unknown flag %q", arg)}
		case repoSeen:
			return &CLIError{ExitCode: 1, Message: "reconc run: expected at most one repo path"}
		default:
			repo = arg
			repoSeen = true
		}
	}
	if force && !enabled {
		return &CLIError{ExitCode: 1, Message: "reconc run off: --force is only valid with `reconc run on`"}
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc run: " + err.Error()}
	}
	if enabled && !force {
		if err := preflightRepositoryRun(abs); err != nil {
			return &CLIError{ExitCode: 2, Message: "reconc run on: " + err.Error() + "; use --force only when intentionally overriding this preflight"}
		}
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

func preflightRepositoryRun(repoRoot string) error {
	quotedRoot := strconv.Quote(repoRoot)
	if _, err := validatePolicyReadOnly(repoRoot); err != nil {
		return fmt.Errorf("policy sources are not ready: %w; run `reconc doctor %s --deep` and apply its remediation", err, quotedRoot)
	}
	if err := policyruntime.ValidatePolicyLockfile(repoRoot); err != nil {
		return fmt.Errorf("compiled policy is not ready: %w; run `reconc refresh %s`", err, quotedRoot)
	}
	state, err := tasklifecycle.InspectRunState(repoRoot)
	if err != nil {
		return fmt.Errorf("TASK plane is invalid: %w; run `reconc task validate %s`", err, quotedRoot)
	}
	if state.Disposition != tasklifecycle.RunContinue && state.Disposition != tasklifecycle.RunClaim {
		return fmt.Errorf("TASK disposition %s has no executable work; run `reconc task status %s` and make one TASK executable", state.Disposition, quotedRoot)
	}
	return nil
}

func runRunReset(args []string, stdout io.Writer) error {
	repo := "."
	jsonOut := false
	repoSeen := false
	for _, arg := range args {
		switch {
		case arg == "--json":
			jsonOut = true
		case strings.HasPrefix(arg, "-"):
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc run reset: unknown flag %q", arg)}
		case repoSeen:
			return &CLIError{ExitCode: 1, Message: "reconc run reset: expected at most one repo path"}
		default:
			repo = arg
			repoSeen = true
		}
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc run reset: " + err.Error()}
	}
	info, err := agentsession.ResetRepositoryRun(abs)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc run reset: " + err.Error()}
	}
	if jsonOut {
		body, err := json.Marshal(info)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc run reset: " + err.Error()}
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
	verbose := false
	repoSeen := false
	for _, a := range args {
		switch {
		case a == "--json":
			jsonOut = true
		case a == "--verbose":
			verbose = true
		case len(a) > 0 && a[0] == '-':
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc run status: unknown flag %q", a)}
		case repoSeen:
			return &CLIError{ExitCode: 1, Message: "reconc run status: expected at most one repo path"}
		default:
			repo = a
			repoSeen = true
		}
	}
	if jsonOut && verbose {
		return &CLIError{ExitCode: 1, Message: "reconc run status: --verbose and --json are mutually exclusive"}
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
	if !verbose {
		fmt.Fprintln(stdout, formatRunStatus(info))
		return nil
	}
	decisions, err := agentsession.ReadRunDecisions(abs, 1)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc run status: " + err.Error()}
	}
	fmt.Fprintln(stdout, formatRunStatusVerbose(info, decisions))
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

	readLimit := 0
	if !follow && branch == "" && session == "" && n > 0 {
		readLimit = n
	}
	decisions, err := agentsession.ReadRunDecisions(abs, readLimit)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc run log: " + err.Error()}
	}
	cursor := lastRunDecisionCursor(decisions)
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
	return followRunLogAfter(ctx, abs, branch, session, jsonOut, cursor, 500*time.Millisecond, stdout)
}

// followRunLog baselines the complete bounded ring and renders only later
// records. It is kept separate from the CLI snapshot path for deterministic
// tests and other internal callers.
func followRunLog(ctx context.Context, repoRoot, branch, session string, jsonOut bool, pollInterval time.Duration, ready chan<- struct{}, stdout io.Writer) error {
	decisions, err := agentsession.ReadRunDecisions(repoRoot, 0)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc run log: " + err.Error()}
	}
	if ready != nil {
		close(ready)
	}
	return followRunLogAfter(ctx, repoRoot, branch, session, jsonOut, lastRunDecisionCursor(decisions), pollInterval, stdout)
}

// followRunLogAfter polls complete lock-consistent ring snapshots and advances
// by record identity. This survives append races and rotation without dropping
// partial records or printing an already-rendered record twice.
func followRunLogAfter(ctx context.Context, repoRoot, branch, session string, jsonOut bool, cursor string, pollInterval time.Duration, stdout io.Writer) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		decisions, err := agentsession.ReadRunDecisions(repoRoot, 0)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc run log: " + err.Error()}
		}
		next, err := runDecisionsAfter(decisions, cursor)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc run log: " + err.Error()}
		}
		for _, d := range next {
			if !decisionMatches(d, branch, session) {
				continue
			}
			writeRunDecision(stdout, d, jsonOut)
		}
		if len(decisions) > 0 {
			cursor = runDecisionCursor(decisions[len(decisions)-1])
		}
	}
}

func runDecisionsAfter(decisions []agentsession.RunDecision, cursor string) ([]agentsession.RunDecision, error) {
	if cursor == "" {
		return decisions, nil
	}
	for index := len(decisions) - 1; index >= 0; index-- {
		if runDecisionCursor(decisions[index]) == cursor {
			return decisions[index+1:], nil
		}
	}
	return nil, fmt.Errorf("follow cursor left the bounded decision-log window; restart `reconc run log --follow`")
}

func lastRunDecisionCursor(decisions []agentsession.RunDecision) string {
	if len(decisions) == 0 {
		return ""
	}
	return runDecisionCursor(decisions[len(decisions)-1])
}

func runDecisionCursor(decision agentsession.RunDecision) string {
	body, err := json.Marshal(decision)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(body)
	return fmt.Sprintf("%x", digest[:])
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
	if branch != "" && d.Branch != branch {
		return false
	}
	if session != "" && d.SessionID != session {
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

func formatRunStatusVerbose(info agentsession.RepositoryRunStatus, decisions []agentsession.RunDecision) string {
	var out strings.Builder
	out.WriteString(formatRunStatus(info))
	fmt.Fprintf(&out, "\n  task: disposition=%s id=%s sub-task=%q open=%d", dash(info.TaskDisposition), dash(info.TaskID), info.CurrentSubTask, info.OpenTasks)
	if info.Blocker != "" {
		fmt.Fprintf(&out, "\n  blocker: %s", info.Blocker)
	}
	if len(decisions) == 0 {
		out.WriteString("\n  last decision: none")
		return out.String()
	}
	last := decisions[len(decisions)-1]
	fmt.Fprintf(&out, "\n  last decision: %s", formatRunDecision(last))
	return out.String()
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
	if d.Branch == "run_followup" || d.Branch == "repo_no_progress_release" {
		if d.StrictContinuation {
			b.WriteString("  [strict continuation; delivered-interjection bound=32]")
		} else {
			fmt.Fprintf(&b, "  [no_progress=%d/6]", d.NoProgressNudges)
		}
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
