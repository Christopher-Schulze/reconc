package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/retention"
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
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook: session root: %s", err)}
	}
	if _, err := initializeSessionState(root, payload.SessionID, runtimeFromPayload(payload)); err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook: session init: %s", err)}
	}
	warnings := []string{}
	if err := RecordHookLiveness(root, runtimeFromPayload(payload), "session_start"); err != nil {
		warnings = append(warnings, "reconc hook liveness (warn): "+err.Error())
	}
	if warning := retentionWarning(retention.RunIfDue(retention.Options{RepoRoot: root, StateRoot: stateRoot(), ActiveSession: payload.SessionID})); warning != "" {
		warnings = append(warnings, warning)
	}
	return Result{ExitCode: 0, Stderr: strings.Join(warnings, "; ")}
}

func runtimeFromPayload(payload *HookPayload) string {
	if payload != nil && payload.Raw != nil {
		for _, key := range []string{"reconc_runtime", "runtime", "agent_runtime"} {
			if value, ok := payload.Raw[key].(string); ok && strings.TrimSpace(value) != "" {
				return normalizeRuntimeName(value)
			}
		}
	}
	return normalizeRuntimeName(os.Getenv(reconcHookRuntimeEnv))
}

func normalizeRuntimeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"cursor-", "codex-", "claude-", "opencode-", "devin-", "antigravity-", "copilot-", "kilo-"} {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimSuffix(prefix, "-")
		}
	}
	return value
}

func logRunStopDecision(repoRoot, branch string, payload *HookPayload, runtime string, before, after repositoryRunState, policyBlocked bool, violationCount int) {
	_ = appendRunDecisionResolved(repoRoot, RunDecision{
		Event:                      "stop",
		Branch:                     branch,
		Runtime:                    strings.TrimSpace(runtime),
		SessionID:                  sessionIDFromPayload(payload),
		EnabledBefore:              before.Enabled,
		EnabledAfter:               after.Enabled,
		DisabledReasonBefore:       before.DisabledReason.String(),
		DisabledReasonAfter:        after.DisabledReason.String(),
		AwaitingContinuationBefore: before.AwaitingContinuation,
		AwaitingContinuationAfter:  after.AwaitingContinuation,
		StopHookActive:             payload != nil && payload.StopHookActive,
		PolicyBlocked:              policyBlocked,
		ViolationCount:             violationCount,
	})
}

// RunPreToolUse evaluates whether the tool-use about to happen would
// violate command safety or deny_write / blocking require_read rules.
// If it would, returns exit 2 with a human-readable explanation on
// stderr so Claude Code surfaces it to the agent.
func RunPreToolUse(repoRoot string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		// Fail-closed per threat model.
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): %s", err)}
	}
	if payload.IsCommandTool() {
		if reason := forbiddenShellCommandReason(payload.Command()); reason != "" {
			return Result{ExitCode: 2, Stderr: reason}
		}
	}
	if !payload.IsWriteTool() {
		return Result{ExitCode: 0}
	}
	pendingWrites := payload.FilePaths()
	if len(pendingWrites) == 0 {
		return Result{ExitCode: 0}
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): %s", err)}
	}
	state, err := EnsureSessionState(root, payload.SessionID)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): %s", err)}
	}
	if state.EvidenceOverflow {
		return Result{ExitCode: 2, Stderr: evidenceOverflowMessage(state)}
	}
	trialWrites := append([]string{}, state.WritePaths...)
	trialWrites = append(trialWrites, pendingWrites...)
	report, err := runPreWritePolicyCheck(root, state.ReadPaths, trialWrites,
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

// RunPermissionRequest denies approval prompts that would violate the
// same pre-execution write policy as PreToolUse. It intentionally never
// auto-allows requests; no decision leaves the platform's normal prompt
// flow intact.
func RunPermissionRequest(repoRoot string, payloadBytes []byte) Result {
	pre := RunPreToolUse(repoRoot, payloadBytes)
	if pre.ExitCode == 0 {
		return Result{ExitCode: 0}
	}
	reason := strings.TrimSpace(pre.Stderr)
	if reason == "" {
		reason = "reconc denied this permission request before execution."
	}
	return Result{ExitCode: 0, Stdout: permissionRequestDenyJSONOutput(reason)}
}

// RunPostToolUse records the tool-use as evidence.
// Always exit 0 -- blocking here would be disruptive without
// preventing damage. Stop is the hard policy-enforcement point; running the
// full Stop policy after every write/shell event turns multi-edit agent runs
// into repeated repo-wide audits.
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
	updated, err := MutateSessionState(root, payload.SessionID, func(state SessionState) SessionState {
		return recordToolUse(state, payload)
	})
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post, warn): %s", err)}
	}
	if updated.EvidenceOverflow {
		return Result{ExitCode: 0, Stderr: evidenceOverflowMessage(updated)}
	}
	return Result{ExitCode: 0}
}

// RunPostToolUseFailure records a failed command outcome. Always exit 0.
func RunPostToolUseFailure(repoRoot string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-fail, warn): %s", err)}
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-fail, warn): %s", err)}
	}
	updated, err := MutateSessionState(root, payload.SessionID, func(state SessionState) SessionState {
		return recordToolFailure(state, payload)
	})
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-fail, warn): %s", err)}
	}
	return Result{ExitCode: 0, Stdout: postToolFailureJSONOutput(updated)}
}

// RunPostToolUseComplete records a completed command as success or failure
// based on the payload's exit code. Runtimes like Cursor provide one
// after-shell event instead of separate PostToolUse/PostToolUseFailure events.
func RunPostToolUseComplete(repoRoot string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-complete, warn): %s", err)}
	}
	if payload.IsCommandTool() {
		if payload.Error != "" {
			return RunPostToolUseFailure(repoRoot, payloadBytes)
		}
		if exitCode := payload.ExitCode(); exitCode != nil && *exitCode != 0 {
			return RunPostToolUseFailure(repoRoot, payloadBytes)
		}
	}
	return RunPostToolUse(repoRoot, payloadBytes)
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
	if root, err := ResolveRepoRoot(repoRoot); err == nil {
		if warning := retentionWarning(retention.RunIfDue(retention.Options{RepoRoot: root, StateRoot: stateRoot()})); warning != "" {
			return Result{ExitCode: 0, Stderr: warning}
		}
	}
	return Result{ExitCode: 0}
}

func retentionWarning(report retention.Report) string {
	if len(report.Errors) == 0 {
		return ""
	}
	return "reconc retention (warn): " + strings.Join(report.Errors, "; ")
}

// --- shared internals ----------------------------------------------

// recordToolUse inspects the payload's tool_name and appends the
// matching evidence (read path, write path, command) to the state.
// Returns a new SessionState; caller must save.
func recordToolUse(state SessionState, payload *HookPayload) SessionState {
	switch {
	case payload.IsReadTool():
		path := payload.FilePath()
		if !isRepoScopedReadEvidence(state.RepoRoot, path) {
			return state
		}
		return AppendReadPath(state, path)
	case payload.IsWriteTool():
		seen := false
		for _, path := range payload.FilePaths() {
			state = AppendWritePath(state, path)
			seen = true
		}
		if seen {
			state = RecordMaterialEvent(state, materialEventSignature(payload, "success"))
		}
		return state
	case payload.IsCommandTool():
		cmd := payload.Command()
		if cmd == "" {
			return state
		}
		state = AppendCommand(state, cmd)
		state = AppendCommandResult(state, commandResultFromPayload(payload, "success"))
		return RecordMaterialEvent(state, materialEventSignature(payload, "success"))
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
	state = AppendCommandResult(state, commandResultFromPayload(payload, "failure"))
	return RecordMaterialEvent(state, materialEventSignature(payload, "failure"))
}

func materialEventSignature(payload *HookPayload, outcome string) string {
	if payload == nil {
		return ""
	}
	body, err := json.Marshal(struct {
		ToolName  string                 `json:"tool_name"`
		ToolInput map[string]interface{} `json:"tool_input"`
		Outcome   string                 `json:"outcome"`
	}{ToolName: payload.ToolName, ToolInput: payload.ToolInput, Outcome: outcome})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
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
	inputs := executionInputs(filterRepoScopedReadPaths(repoRoot, readPaths), writePaths, commands, cmdResults, claims)
	report, err := runtime.CheckRepoPolicy(repoRoot, inputs)
	if err != nil {
		return nil, err
	}
	if err := writeLatestReport(repoRoot, sessionID, report); err != nil {
		return nil, err
	}
	return report, nil
}

func runPreWritePolicyCheck(
	repoRoot string,
	readPaths, writePaths, commands []string,
	cmdResults []CommandResult,
	claims []string,
) (*runtime.CheckReport, error) {
	inputs := executionInputs(filterRepoScopedReadPaths(repoRoot, readPaths), writePaths, commands, cmdResults, claims)
	return runtime.CheckRepoPolicyForKinds(repoRoot, inputs, preWriteBlockKinds)
}

func filterRepoScopedReadPaths(repoRoot string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if isRepoScopedReadEvidence(repoRoot, path) {
			out = append(out, path)
		}
	}
	return out
}

func isRepoScopedReadEvidence(repoRoot, raw string) bool {
	path := strings.TrimSpace(raw)
	if path == "" {
		return false
	}
	path = strings.ReplaceAll(path, "\\", "/")
	if !filepath.IsAbs(path) {
		return true
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	cleaned := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = resolved
	}
	rel, err := filepath.Rel(root, cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func executionInputs(
	readPaths, writePaths, commands []string,
	cmdResults []CommandResult,
	claims []string,
) runtime.ExecutionInputs {
	evalResults := make([]runtime.CommandResult, len(cmdResults))
	for i, r := range cmdResults {
		evalResults[i] = runtime.CommandResult{
			Command: r.Command,
			Outcome: r.Outcome,
		}
	}
	return runtime.ExecutionInputs{
		ReadPaths:      readPaths,
		WritePaths:     writePaths,
		Commands:       commands,
		Claims:         claims,
		CommandResults: evalResults,
	}
}

func evidenceOverflowMessage(state SessionState) string {
	field := strings.TrimSpace(state.EvidenceOverflowReason)
	if field == "" {
		field = "unknown"
	}
	return fmt.Sprintf("reconc blocked because bounded session evidence overflowed at %s. Start a fresh session or reduce the tool-event scope; omitted evidence cannot be evaluated safely.", field)
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
	if _, err := atomicfile.WriteIfChanged(path, body, 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
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

func permissionRequestDenyJSONOutput(reason string) string {
	payload := map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName": "PermissionRequest",
			"decision": map[string]string{
				"behavior": "deny",
				"message":  reason,
			},
		},
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
// Claude/Codex sees decision=block + reason and refuses to finalise
// the session, prompting the agent to resolve the remaining
// blocking violations.
func stopBlockJSONOutput(repoRoot, sessionID string, report *runtime.CheckReport, violations []runtime.Violation) string {
	repeated, feedbackID := recordStopBlockAndRepeated(repoRoot, sessionID, violations)
	reason := stopReasonForViolations(violations, reportPathForStop(repoRoot, sessionID), feedbackID, repeated, repositoryRunStatusLine(repoRoot))
	payload := map[string]string{
		"decision": "block",
		"reason":   reason,
	}
	body, _ := json.Marshal(payload)
	return string(body)
}

func stopReasonForViolations(violations []runtime.Violation, reportPath, feedbackID string, repeated bool, repositoryRunStatus string) string {
	if repeated {
		var rules []string
		for _, v := range violations {
			rules = append(rules, v.RuleID)
		}
		var b strings.Builder
		b.WriteString("reconc: same blocking workflow report still prevents this session from stopping.")
		if feedbackID != "" {
			b.WriteString("\nFeedback: ")
			b.WriteString(feedbackID)
		}
		if reportPath != "" {
			b.WriteString("\nReport: ")
			b.WriteString(reportPath)
		}
		if repositoryRunStatus != "" {
			b.WriteString("\n")
			b.WriteString(repositoryRunStatus)
		}
		if len(rules) > 0 {
			b.WriteString("\nRules: ")
			b.WriteString(strings.Join(rules, ", "))
		}
		b.WriteString("\nResolve the report or re-run the failing audit command before finishing.")
		return b.String()
	}
	reason := firstLinesForViolationsWithReport(violations,
		"reconc: blocking workflow requirements still remain before this session can stop.",
		reportPath)
	if feedbackID != "" {
		reason += "\nFeedback: " + feedbackID
	}
	if repositoryRunStatus != "" {
		reason += "\n" + repositoryRunStatus
	}
	return reason
}

func repositoryRunStatusLine(repoRoot string) string {
	state, err := loadRepositoryRunStateResolved(repoRoot)
	if err != nil {
		return "Repository run: unknown"
	}
	if repositoryRunEnabled(state) {
		return "Repository run: enabled, blocked_by_policy"
	}
	if state.DisabledReason != repositoryRunDisabledNone {
		return fmt.Sprintf("Repository run: disabled, reason=%s", state.DisabledReason.String())
	}
	return "Repository run: disabled"
}

func firstLinesForViolationsWithReport(violations []runtime.Violation, title, reportPath string) string {
	var b strings.Builder
	b.WriteString(title)
	if reportPath != "" {
		b.WriteString("\nReport: ")
		b.WriteString(reportPath)
	}
	n := len(violations)
	upto := n
	if upto > 3 {
		upto = 3
	}
	for i := 0; i < upto; i++ {
		v := violations[i]
		action := v.RecommendedAction
		if strings.Contains(action, "Inspect the script output above") {
			if detail := firstDiagnosticLine(v.Explanation); detail != "" {
				action = detail
			}
		}
		if action == "" {
			action = v.Message
		}
		b.WriteString("\n- [")
		b.WriteString(v.RuleID)
		b.WriteString("] ")
		b.WriteString(action)
	}
	if n > 3 {
		b.WriteString(fmt.Sprintf("\n- %d more violation(s) remain.", n-3))
	}
	return b.String()
}

func firstDiagnosticLine(explanation string) string {
	explanation = strings.TrimSpace(explanation)
	for _, marker := range []string{" blocked: ", " error: "} {
		if idx := strings.Index(explanation, marker); idx >= 0 {
			explanation = strings.TrimSpace(explanation[idx+len(marker):])
			break
		}
	}
	if explanation == "" {
		return ""
	}
	for _, sep := range []string{"\n", ";"} {
		if idx := strings.Index(explanation, sep); idx >= 0 {
			explanation = strings.TrimSpace(explanation[:idx])
		}
	}
	if len([]rune(explanation)) > 220 {
		runes := []rune(explanation)
		explanation = string(runes[:220]) + "..."
	}
	return explanation
}

// repositoryRunBlockJSON returns the Stop-hook block control-response
// that carries the repository-run continuation prompt as the reason so the
// agent auto-continues without a separate JS plugin.
func repositoryRunBlockJSON(prompt string) string {
	payload := map[string]string{
		"decision": "block",
		"reason":   prompt,
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
	return strings.TrimSpace(buf.String())
}
