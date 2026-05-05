package agentsession

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
)

// Result is what one handler returns to the CLI wrapper: an exit
// code, optional stdout (JSON control response to the agent),
// optional stderr (human-readable explanation).
//
// Per the threat model:
//   - PreToolUse / Stop use Stdout = JSON control-response payload
//     so Claude Code renders the block reason in its UI.
//   - Post* handlers use Stdout for additionalContext warnings.
//   - Stderr is for human log consumption; not parsed by the agent.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// BlockingModes are the Mode values that cause a pre-/stop-hook to
// actually refuse the action.
var blockingModes = map[policy.Mode]struct{}{
	policy.ModeBlock: {},
	policy.ModeFix:   {},
}

// preWriteBlockKinds are the subset of rule kinds that are meaningful
// to enforce at PreToolUse time (before a file is actually written).
// Other kinds (require_command, require_claim, ...) are Stop-time
// gates because their evidence only accrues after the agent runs.
var preWriteBlockKinds = map[policy.Kind]struct{}{
	policy.KindDenyWrite:   {},
	policy.KindRequireRead: {},
}

// RunSessionStart initialises fresh session state. Always exit 0;
// any failure is turned into stderr text because blocking SessionStart
// would wedge the whole agent session (the payload is mostly session
// id + initial transcript path -- little to validate).
func RunSessionStart(repoRoot string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook: %s", err)}
	}
	if _, err := InitializeSessionState(repoRoot, payload.SessionID); err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook: session init: %s", err)}
	}
	return Result{ExitCode: 0}
}

// RunPreToolUse evaluates whether the tool-use about to happen (a
// write, since non-write tools short-circuit) would violate a
// deny_write / blocking require_read rule. If it would, returns exit
// 2 with a human-readable explanation on stderr so Claude Code
// surfaces it to the agent.
func RunPreToolUse(repoRoot string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		// Fail-closed per threat model.
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): %s", err)}
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): %s", err)}
	}
	state, err := EnsureSessionState(root, payload.SessionID)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): %s", err)}
	}
	if !payload.IsWriteTool() {
		return Result{ExitCode: 0}
	}
	pendingWrite := payload.FilePath()
	if pendingWrite == "" {
		return Result{ExitCode: 0}
	}
	// Run check with state + the pending write appended.
	trialWrites := append([]string{}, state.WritePaths...)
	trialWrites = append(trialWrites, pendingWrite)
	report, err := runCheckAndSave(root, state.SessionID, state.ReadPaths, trialWrites,
		state.Commands, state.CommandResults, state.Claims)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): check failed: %s", err)}
	}
	violations := preWriteBlockingViolations(report)
	if len(violations) == 0 {
		return Result{ExitCode: 0}
	}
	return Result{
		ExitCode: 2,
		Stderr: firstLinesForViolations(violations,
			"reconc blocked this file modification before execution."),
	}
}

// RunPostToolUse records the tool-use as evidence and optionally
// surfaces a warn-level JSON hook feedback to Claude Code.
// Always exit 0 -- blocking here would be disruptive without
// preventing damage.
func RunPostToolUse(repoRoot string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		// Fail-open on parse errors for observation-only events.
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post, warn): %s", err)}
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post, warn): %s", err)}
	}
	state, err := EnsureSessionState(root, payload.SessionID)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post, warn): %s", err)}
	}
	updated := recordToolUse(state, payload)
	if err := SaveSessionState(updated); err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post, warn): %s", err)}
	}
	// Only run check for known tool kinds to keep cold-path cheap.
	if !payload.IsReadTool() && !payload.IsWriteTool() && !payload.IsCommandTool() {
		return Result{ExitCode: 0}
	}
	report, err := runCheckAndSave(root, updated.SessionID, updated.ReadPaths,
		updated.WritePaths, updated.Commands, updated.CommandResults, updated.Claims)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post, warn): %s", err)}
	}
	// Reads don't need additionalContext; writes + commands do.
	if payload.IsReadTool() {
		return Result{ExitCode: 0}
	}
	return Result{ExitCode: 0, Stdout: postToolJSONOutput(report)}
}

// RunPostToolUseFailure records a failed command outcome and keeps
// the saved report fresh. Always exit 0.
func RunPostToolUseFailure(repoRoot string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-fail, warn): %s", err)}
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-fail, warn): %s", err)}
	}
	state, err := EnsureSessionState(root, payload.SessionID)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-fail, warn): %s", err)}
	}
	updated := recordToolFailure(state, payload)
	if err := SaveSessionState(updated); err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-fail, warn): %s", err)}
	}
	if _, err := runCheckAndSave(root, updated.SessionID, updated.ReadPaths,
		updated.WritePaths, updated.Commands, updated.CommandResults, updated.Claims); err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-fail, warn): %s", err)}
	}
	return Result{ExitCode: 0, Stdout: postToolFailureJSONOutput(updated)}
}

// RunStop checks whether any blocking invariant is still unmet at
// session end. If so, emits a JSON control-response with decision=
// block so Claude Code refuses to stop (prompting the agent to fix
// the remaining violations). Exit code stays 0 -- the block is in
// the JSON payload.
func RunStop(repoRoot string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): %s", err)}
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): %s", err)}
	}
	state, err := EnsureSessionState(root, payload.SessionID)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): %s", err)}
	}
	report, err := runCheckAndSave(root, state.SessionID, state.ReadPaths,
		state.WritePaths, state.Commands, state.CommandResults, state.Claims)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): check failed: %s", err)}
	}
	if len(blockingViolations(report)) == 0 {
		return Result{ExitCode: 0}
	}
	// Avoid endless loops when Claude is already continuing because
	// of this hook.
	if payload.StopHookActive {
		return Result{ExitCode: 0}
	}
	return Result{ExitCode: 0, Stdout: stopBlockJSONOutput(report)}
}

// RunSessionEnd cleans up the mutable session state; saved reports
// survive so post-session diagnostics remain available.
func RunSessionEnd(repoRoot string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (end, warn): %s", err)}
	}
	if err := CleanupSessionState(repoRoot, payload.SessionID); err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (end, warn): %s", err)}
	}
	return Result{ExitCode: 0}
}

// --- shared internals ----------------------------------------------

// recordToolUse inspects the payload's tool_name and appends the
// matching evidence (read path, write path, command) to the state.
// Returns a new SessionState; caller must save.
func recordToolUse(state SessionState, payload *HookPayload) SessionState {
	switch {
	case payload.IsReadTool():
		return AppendReadPath(state, payload.FilePath())
	case payload.IsWriteTool():
		return AppendWritePath(state, payload.FilePath())
	case payload.IsCommandTool():
		cmd := payload.Command()
		if cmd == "" {
			return state
		}
		state = AppendCommand(state, cmd)
		state = AppendCommandResult(state, commandResultFromPayload(payload, "success"))
		return state
	}
	return state
}

// recordToolFailure appends a command-result with outcome "failure"
// if the payload describes a Bash tool failure. Non-Bash failures are
// ignored (reads / writes don't have a success/failure binary).
func recordToolFailure(state SessionState, payload *HookPayload) SessionState {
	if !payload.IsCommandTool() {
		return state
	}
	cmd := payload.Command()
	if cmd == "" {
		return state
	}
	return AppendCommandResult(state, commandResultFromPayload(payload, "failure"))
}

// commandResultFromPayload extracts the normalised CommandResult from
// a Bash tool-use payload.
func commandResultFromPayload(payload *HookPayload, outcome string) CommandResult {
	return CommandResult{
		Command:     payload.Command(),
		Outcome:     outcome,
		ToolUseID:   payload.ToolUseID,
		ExitCode:    payload.ExitCode(),
		Error:       payload.Error,
		IsInterrupt: payload.IsInterrupt,
	}
}

// runCheckAndSave runs the evaluator and also writes the resulting
// CheckReport to the session's reports/ file so later inspection
// (`reconc why`, `reconc fix`, agent tooling) finds the latest view.
func runCheckAndSave(
	repoRoot, sessionID string,
	readPaths, writePaths, commands []string,
	cmdResults []CommandResult,
	claims []string,
) (*runtime.CheckReport, error) {
	// Convert our CommandResult shape to the evaluator's.
	evalResults := make([]runtime.CommandResult, len(cmdResults))
	for i, r := range cmdResults {
		evalResults[i] = runtime.CommandResult{
			Command: r.Command,
			Outcome: r.Outcome,
		}
	}
	inputs := runtime.ExecutionInputs{
		ReadPaths:      readPaths,
		WritePaths:     writePaths,
		Commands:       commands,
		Claims:         claims,
		CommandResults: evalResults,
	}
	report, err := runtime.CheckRepoPolicy(repoRoot, inputs)
	if err != nil {
		return nil, err
	}
	if err := writeLatestReport(repoRoot, sessionID, report); err != nil {
		return nil, err
	}
	return report, nil
}

// writeLatestReport persists the CheckReport JSON to the session's
// reports/<id>.json path. Atomic via tmp-rename.
func writeLatestReport(repoRoot, sessionID string, report *runtime.CheckReport) error {
	path := sessionReportPath(repoRoot, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir reports dir: %w", err)
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	body = append(body, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("write report tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename report: %w", err)
	}
	return nil
}

// --- violation helpers ---------------------------------------------

func preWriteBlockingViolations(report *runtime.CheckReport) []runtime.Violation {
	out := []runtime.Violation{}
	for _, v := range report.Violations {
		if _, blocking := blockingModes[v.Mode]; !blocking {
			continue
		}
		if _, ok := preWriteBlockKinds[v.Kind]; !ok {
			continue
		}
		out = append(out, v)
	}
	return out
}

func blockingViolations(report *runtime.CheckReport) []runtime.Violation {
	out := []runtime.Violation{}
	for _, v := range report.Violations {
		if _, blocking := blockingModes[v.Mode]; blocking {
			out = append(out, v)
		}
	}
	return out
}

// firstLinesForViolations produces a compact human-readable summary
// of up to 3 violations plus an "N more" tail.
func firstLinesForViolations(violations []runtime.Violation, title string) string {
	var b strings.Builder
	b.WriteString(title)
	n := len(violations)
	upto := n
	if upto > 3 {
		upto = 3
	}
	for i := 0; i < upto; i++ {
		v := violations[i]
		b.WriteString("\n- [")
		b.WriteString(v.RuleID)
		b.WriteString("] ")
		b.WriteString(v.RecommendedAction)
	}
	if n > 3 {
		b.WriteString(fmt.Sprintf("\n- %d more violation(s) remain.", n-3))
	}
	return b.String()
}

// --- JSON control-response builders --------------------------------

// postToolJSONOutput returns the Claude-Code hookSpecificOutput JSON
// for a PostToolUse event, or "" if there's nothing worth saying
// (pass-decision report).
func postToolJSONOutput(report *runtime.CheckReport) string {
	if report.Decision == runtime.DecisionPass {
		return ""
	}
	title := "reconc: workflow warnings remain after this tool call."
	if report.Decision == runtime.DecisionBlock {
		title = "reconc: blocking workflow requirements still remain before Claude can finish."
	}
	msg := firstLinesForViolations(report.Violations, title)
	payload := map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":     "PostToolUse",
			"additionalContext": msg,
		},
	}
	if report.Decision == runtime.DecisionBlock {
		payload["decision"] = "block"
		payload["reason"] = msg
	}
	body, _ := json.Marshal(payload)
	return string(body)
}

// postToolFailureJSONOutput returns the hookSpecificOutput for a
// PostToolUseFailure event, or "" if the last command result isn't a
// failure (shouldn't happen but defensive).
func postToolFailureJSONOutput(state SessionState) string {
	if len(state.CommandResults) == 0 {
		return ""
	}
	latest := state.CommandResults[len(state.CommandResults)-1]
	if latest.Outcome != "failure" {
		return ""
	}
	var b strings.Builder
	b.WriteString("reconc recorded failed command `")
	b.WriteString(latest.Command)
	b.WriteString("`.")
	if latest.Error != "" {
		b.WriteString("\n")
		b.WriteString(latest.Error)
	}
	b.WriteString("\nOnly successful commands satisfy `require_command_success` rules.")
	payload := map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":     "PostToolUseFailure",
			"additionalContext": b.String(),
		},
	}
	body, _ := json.Marshal(payload)
	return string(body)
}

// stopBlockJSONOutput returns the Stop-hook block control-response.
// Claude Code sees decision=block + reason and refuses to finalise
// the session, prompting the agent to resolve the remaining
// blocking violations.
func stopBlockJSONOutput(report *runtime.CheckReport) string {
	violations := blockingViolations(report)
	reason := firstLinesForViolations(violations,
		"reconc: blocking workflow requirements still remain before this session can stop.")
	payload := map[string]string{
		"decision": "block",
		"reason":   reason,
	}
	body, _ := json.Marshal(payload)
	return string(body)
}
