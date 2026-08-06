package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/pathidentity"
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

// preCommandBlockKinds are the policy rules whose effect must happen before a
// shell command executes. Evidence-requiring command rules stay at Stop; a
// forbid_command rule is a prevention control and therefore belongs here.
var preCommandBlockKinds = map[policy.Kind]struct{}{
	policy.KindForbidCommand: {},
	policy.KindAllOf:         {},
	policy.KindAnyOf:         {},
	policy.KindNot:           {},
}

// RunSessionStart initialises fresh session state. The handler reports errors;
// the central route registry converts them to success for fail-open
// SessionStart integrations so a state failure cannot wedge the host session.
func RunSessionStart(repoRoot string, payloadBytes []byte) Result {
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook: session root: %s", err)}
	}
	return runSessionStartResolved(root.path, payloadBytes, "")
}

func runSessionStartResolved(root string, payloadBytes []byte, runtimeName string) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook: %s", err)}
	}
	if runtimeName == "" {
		runtimeName = runtimeFromPayload(payload)
	}
	if _, err := initializeSessionStateResolved(root, payload.SessionID, runtimeName); err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook: session init: %s", err)}
	}
	warnings := []string{}
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
	if value == "copilot" || value == "github-copilot" || strings.HasPrefix(value, "copilot-") || strings.HasPrefix(value, "github-copilot-") {
		return "github-copilot"
	}
	for _, prefix := range []string{"cursor-", "codex-", "claude-", "opencode-", "devin-", "antigravity-", "kilo-", "grok-", "omp-", "pi-", "zcode-"} {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimSuffix(prefix, "-")
		}
	}
	return value
}

// RunPassiveEvent validates a lifecycle payload and ensures its session exists
// without treating passive host events as policy evidence. Grok exposes a
// richer lifecycle than Reconc needs for policy evaluation; liveness is
// recorded by the central dispatcher after this handler succeeds.
func RunPassiveEvent(repoRoot string, payloadBytes []byte) Result {
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (passive, warn): %s", err)}
	}
	return runPassiveEventResolved(root.path, payloadBytes)
}

func runPassiveEventResolved(root string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (passive, warn): %s", err)}
	}
	if err := observeSessionStateResolved(root, payload.SessionID); err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (passive, warn): %s", err)}
	}
	return Result{ExitCode: 0}
}

func logRunStopDecision(repoRoot, branch string, payload *HookPayload, runtime string, before, after repositoryRunState, policyBlocked bool, violationCount int) {
	_ = appendRunStopDecision(repoRoot, branch, payload, runtime, before, after, policyBlocked, violationCount)
}

func appendRunStopDecision(repoRoot, branch string, payload *HookPayload, runtime string, before, after repositoryRunState, policyBlocked bool, violationCount int) error {
	return appendRunDecisionResolved(repoRoot, RunDecision{
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

func logRunContinuationDecision(repoRoot, branch string, payload *HookPayload, runtime string, before, after repositoryRunState, nudges int, strict bool) error {
	return appendRunDecisionResolved(repoRoot, RunDecision{
		Event: "stop", Branch: branch, Runtime: strings.TrimSpace(runtime),
		SessionID:     sessionIDFromPayload(payload),
		EnabledBefore: before.Enabled, EnabledAfter: after.Enabled,
		DisabledReasonBefore: before.DisabledReason.String(), DisabledReasonAfter: after.DisabledReason.String(),
		AwaitingContinuationBefore: before.AwaitingContinuation, AwaitingContinuationAfter: after.AwaitingContinuation,
		StopHookActive:   payload != nil && payload.StopHookActive,
		NoProgressNudges: nudges, StrictContinuation: strict,
	})
}

// RunPreToolUse evaluates whether the tool-use about to happen would
// violate command safety or deny_write / blocking require_read rules.
// If it would, returns exit 2 with a human-readable explanation on
// stderr so Claude Code surfaces it to the agent.
func RunPreToolUse(repoRoot string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): %s", err)}
	}
	if !payload.IsCommandTool() && !payload.IsWriteTool() {
		return Result{ExitCode: 0}
	}
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): %s", err)}
	}
	return runPreToolUseResolved(root.path, payloadBytes)
}

func runPreToolUseResolved(root string, payloadBytes []byte) Result {
	return runPreToolUseResolvedWithEvaluator(root, payloadBytes, runtime.NewEvaluator())
}

func runPreToolUseResolvedWithEvaluator(root string, payloadBytes []byte, evaluator *runtime.Evaluator) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		// Fail-closed per threat model.
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): %s", err)}
	}
	if payload.IsCommandTool() {
		if reason := forbiddenShellCommandReasonInRepo(root, payload.Command()); reason != "" {
			return Result{ExitCode: 2, Stderr: reason}
		}
		state, err := ensureSessionStateResolved(root, payload.SessionID)
		if err != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): %s", err)}
		}
		if state.EvidenceOverflow {
			return Result{ExitCode: 2, Stderr: evidenceOverflowMessage(state)}
		}
		state, err = loadCompleteSessionEvidence(root, state)
		if err != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): load evidence chain: %s", err)}
		}
		report, err := runPreCommandPolicyCheckWithEvaluator(evaluator, root, state, payload.Command())
		if err != nil {
			if isLockfileError(err) {
				// A stale lockfile blocks every gated command, including the
				// refresh that repairs it. Admitting the repair is what keeps
				// this fail-closed instead of sealed shut.
				if isLockfileRepairCommand(payload.Command()) {
					return Result{ExitCode: 0}
				}
				return Result{ExitCode: 2, Stderr: lockfileBlockMessage("pre", err)}
			}
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): command check failed: %s", err)}
		}
		violations := blockingViolationsForKinds(report, preCommandBlockKinds)
		if len(violations) > 0 {
			return Result{ExitCode: 2, Stderr: firstLinesForViolations(violations, "reconc blocked this command before execution.")}
		}
	}
	if !payload.IsWriteTool() {
		return Result{ExitCode: 0}
	}
	pendingWrites := payload.FilePaths()
	if len(pendingWrites) == 0 {
		// An apply_patch call whose patch body parses to zero file
		// operations means the envelope format drifted; passing it
		// through would silently ungate every Codex write. Fail closed
		// like any other malformed pre-write payload.
		if payload.ToolName == "apply_patch" && payload.Command() != "" {
			return Result{ExitCode: 2, Stderr: "reconc hook (pre): apply_patch payload contains no parseable file operations; refusing to pass an unparseable write through the gate"}
		}
		return Result{ExitCode: 0}
	}
	// Agent persistent-memory writes (~/.claude/projects/<p>/memory/**) are
	// harness runtime state, not repository writes: they are excluded from the
	// repo write policy instead of being denied as boundary escapes.
	pendingWrites = withoutAgentMemoryPaths(root, pendingWrites)
	if len(pendingWrites) == 0 {
		return Result{ExitCode: 0}
	}
	state, err := ensureSessionStateResolved(root, payload.SessionID)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): %s", err)}
	}
	if state.EvidenceOverflow {
		return Result{ExitCode: 2, Stderr: evidenceOverflowMessage(state)}
	}
	state, err = loadCompleteSessionEvidence(root, state)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): load evidence chain: %s", err)}
	}
	trialWrites := append([]string{}, state.WritePaths...)
	trialWrites = append(trialWrites, pendingWrites...)
	report, err := runPreWritePolicyCheckWithEvaluator(evaluator, root, state.ReadPaths, trialWrites,
		state.WriteEpochs, state.Commands, state.CommandResults, state.Claims)
	if err != nil {
		if isLockfileError(err) {
			// Writes stay blocked: a write authorized by an unreadable policy
			// is exactly what this gate exists to prevent. The message now
			// names a reachable repair instead of one the gate refuses.
			return Result{ExitCode: 2, Stderr: lockfileBlockMessage("pre", err)}
		}
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
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return Result{ExitCode: 0, Stdout: permissionRequestDenyJSONOutput(fmt.Sprintf("reconc hook (pre): %s", err))}
	}
	return runPermissionRequestResolved(root.path, payloadBytes)
}

func runPermissionRequestResolved(root string, payloadBytes []byte) Result {
	pre := runPreToolUseResolved(root, payloadBytes)
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
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post, warn): %s", err)}
	}
	return runPostToolUseResolved(root.path, payloadBytes)
}

func runPostToolUseResolved(root string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		// Fail-open on parse errors for observation-only events.
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post, warn): %s", err)}
	}
	updated, err := mutateSessionStateResolved(root, payload.SessionID, func(state SessionState) SessionState {
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
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-fail, warn): %s", err)}
	}
	return runPostToolUseFailureResolved(root.path, payloadBytes)
}

func runPostToolUseFailureResolved(root string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-fail, warn): %s", err)}
	}
	updated, err := mutateSessionStateResolved(root, payload.SessionID, func(state SessionState) SessionState {
		return recordToolFailure(state, payload)
	})
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-fail, warn): %s", err)}
	}
	return Result{ExitCode: 0, Stdout: postToolFailureJSONOutput(updated)}
}

// RunPostToolUseComplete records only successful tool evidence and routes an
// explicit failure through the failure observer. Runtimes like Devin provide
// one post-tool event instead of separate success and failure events.
func RunPostToolUseComplete(repoRoot string, payloadBytes []byte) Result {
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-complete, warn): %s", err)}
	}
	return runPostToolUseCompleteResolved(root.path, payloadBytes)
}

func runPostToolUseCompleteResolved(root string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-complete, warn): %s", err)}
	}
	if toolResponseFailed(payload) {
		return runPostToolUseFailureResolved(root, payloadBytes)
	}
	return runPostToolUseResolved(root, payloadBytes)
}

// RunPostToolUseCompleteStrict requires an explicit, internally consistent
// shell outcome. OpenCode, Kilo, OMP, and Pi emit post-tool events even when a child
// process exits unsuccessfully, so completion of the host callback alone is
// not evidence that the command succeeded.
func RunPostToolUseCompleteStrict(repoRoot string, payloadBytes []byte) Result {
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-complete, warn): %s", err)}
	}
	return runPostToolUseCompleteStrictResolved(root.path, payloadBytes)
}

func runPostToolUseCompleteStrictResolved(root string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (post-complete, warn): %s", err)}
	}
	if !payload.IsCommandTool() {
		return runPostToolUseCompleteResolved(root, payloadBytes)
	}
	success, diagnostic := strictCommandOutcome(payload)
	if success {
		return runPostToolUseResolved(root, payloadBytes)
	}
	if strings.TrimSpace(payload.Error) == "" && diagnostic != "" {
		payload.Raw["error"] = diagnostic
		toolResponse := payload.ToolResponse
		if toolResponse == nil {
			toolResponse = map[string]interface{}{}
		}
		toolResponse["success"] = false
		payload.Raw["tool_response"] = toolResponse
		if normalized, marshalErr := json.Marshal(payload.Raw); marshalErr == nil {
			payloadBytes = normalized
		}
	}
	return runPostToolUseFailureResolved(root, payloadBytes)
}

func strictCommandOutcome(payload *HookPayload) (bool, string) {
	if payload == nil {
		return false, "missing authoritative shell outcome"
	}
	if strings.TrimSpace(payload.Error) != "" {
		return false, payload.Error
	}
	if errorValue, present := payload.ToolResponse["error"]; present && errorValue != nil {
		if errorText, ok := errorValue.(string); !ok || strings.TrimSpace(errorText) != "" {
			return false, "host reported shell execution failure"
		}
	}
	success, hasSuccess := payload.ToolResponse["success"].(bool)
	exitCode, hasExit, validExit := strictExitCode(payload.ToolResponse)
	if hasExit && !validExit {
		return false, "invalid authoritative shell exit status"
	}
	if !hasExit {
		return false, "missing authoritative shell exit status"
	}
	exitSuccess := exitCode == 0
	if hasSuccess && success != exitSuccess {
		return false, "conflicting authoritative shell outcome"
	}
	if !exitSuccess {
		return false, fmt.Sprintf("shell command exited with status %d", exitCode)
	}
	return true, ""
}

func strictExitCode(response map[string]interface{}) (int, bool, bool) {
	var value int
	found := false
	for _, key := range []string{"exit_code", "exitCode", "status_code", "statusCode"} {
		raw, present := response[key]
		if !present {
			continue
		}
		current, valid := strictInteger(raw)
		if !valid {
			return 0, true, false
		}
		if found && current != value {
			return 0, true, false
		}
		value = current
		found = true
	}
	return value, found, true
}

func strictInteger(value interface{}) (int, bool) {
	switch number := value.(type) {
	case json.Number:
		integer, err := number.Int64()
		if err != nil || int64(int(integer)) != integer {
			return 0, false
		}
		return int(integer), true
	case float64:
		integer := int(number)
		if float64(integer) != number {
			return 0, false
		}
		return integer, true
	default:
		return 0, false
	}
}

func toolResponseFailed(payload *HookPayload) bool {
	if payload.Error != "" {
		return true
	}
	if exitCode := payload.ExitCode(); exitCode != nil && *exitCode != 0 {
		return true
	}
	if success, ok := payload.ToolResponse["success"].(bool); ok && !success {
		return true
	}
	errorValue, present := payload.ToolResponse["error"]
	if !present || errorValue == nil {
		return false
	}
	if errorText, ok := errorValue.(string); ok {
		return strings.TrimSpace(errorText) != ""
	}
	return true
}

// RunSessionEnd cleans up the mutable session state; saved reports
// survive so post-session diagnostics remain available.
func RunSessionEnd(repoRoot string, payloadBytes []byte) Result {
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (end, warn): %s", err)}
	}
	return runSessionEndResolved(root.path, payloadBytes)
}

func runSessionEndResolved(root string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (end, warn): %s", err)}
	}
	if err := cleanupSessionStateResolved(root, payload.SessionID); err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (end, warn): %s", err)}
	}
	if warning := retentionWarning(retention.RunIfDue(retention.Options{RepoRoot: root, StateRoot: stateRoot()})); warning != "" {
		return Result{ExitCode: 0, Stderr: warning}
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
	if state.EvidenceOverflow {
		return state
	}
	switch {
	case payload.IsReadTool():
		path := payload.FilePath()
		if !isRepoScopedReadEvidence(state.RepoRoot, path) {
			return state
		}
		return AppendReadPath(state, path)
	case payload.IsWriteTool():
		// Agent memory writes are runtime state, never repo write evidence.
		paths := withoutAgentMemoryPaths(state.RepoRoot, payload.FilePaths())
		if len(paths) > 0 {
			signature := materialEventSignature(payload, "success")
			if signature != "" && signature == state.LastMaterialSignature {
				return state
			}
			state = RecordWriteEvent(state, paths)
			state = RecordMaterialEvent(state, signature)
		}
		return state
	case payload.IsCommandTool():
		cmd := payload.Command()
		if cmd == "" {
			return state
		}
		signature := materialEventSignature(payload, "success")
		if signature != "" && signature == state.LastMaterialSignature {
			return state
		}
		state = AppendCommand(state, cmd)
		state = AppendCommandResult(state, commandResultFromPayload(state, payload, "success"))
		return RecordMaterialEvent(state, signature)
	}
	return state
}

// recordToolFailure appends a command-result with outcome "failure"
// if the payload describes a Bash tool failure. Non-Bash failures are
// ignored (reads / writes don't have a success/failure binary).
func recordToolFailure(state SessionState, payload *HookPayload) SessionState {
	if state.EvidenceOverflow {
		return state
	}
	if !payload.IsCommandTool() {
		return state
	}
	cmd := payload.Command()
	if cmd == "" {
		return state
	}
	signature := materialEventSignature(payload, "failure")
	if signature != "" && signature == state.LastMaterialSignature {
		return state
	}
	state = AppendCommandResult(state, commandResultFromPayload(state, payload, "failure"))
	return RecordMaterialEvent(state, signature)
}

func materialEventSignature(payload *HookPayload, outcome string) string {
	if payload == nil {
		return ""
	}
	toolName := strings.ToLower(strings.TrimSpace(payload.ToolName))
	input := payload.ToolInput
	if payload.IsWriteTool() {
		toolName = "write"
		paths := append([]string(nil), payload.FilePaths()...)
		sort.Strings(paths)
		input = map[string]interface{}{"paths": paths}
	} else if payload.IsCommandTool() {
		toolName = "command"
		input = map[string]interface{}{"command": payload.Command()}
	}
	body, err := json.Marshal(struct {
		ToolName  string                 `json:"tool_name"`
		ToolInput map[string]interface{} `json:"tool_input"`
		Outcome   string                 `json:"outcome"`
	}{ToolName: toolName, ToolInput: input, Outcome: outcome})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// commandResultFromPayload extracts the normalised CommandResult from
// a Bash tool-use payload.
func commandResultFromPayload(state SessionState, payload *HookPayload, outcome string) CommandResult {
	return CommandResult{
		Command:       payload.Command(),
		Outcome:       outcome,
		EvidenceEpoch: state.EvidenceEpoch,
		ToolUseID:     payload.ToolUseID,
		ExitCode:      payload.ExitCode(),
		Error:         payload.Error,
		IsInterrupt:   payload.IsInterrupt,
	}
}

// runCheckAndSave runs the evaluator and also writes the resulting
// CheckReport to the session's reports/ file so later inspection
// (`reconc why`, `reconc fix`, agent tooling) finds the latest view.
func runCheckAndSave(
	repoRoot, sessionID string,
	readPaths, writePaths []string,
	writeEpochs map[string]uint64,
	commands []string,
	cmdResults []CommandResult,
	claims []string,
) (*runtime.CheckReport, error) {
	return runCheckAndSaveWithEvaluator(runtime.NewEvaluator(), repoRoot, sessionID, readPaths, writePaths, writeEpochs, commands, cmdResults, claims)
}

func runCheckAndSaveWithEvaluator(
	evaluator *runtime.Evaluator,
	repoRoot, sessionID string,
	readPaths, writePaths []string,
	writeEpochs map[string]uint64,
	commands []string,
	cmdResults []CommandResult,
	claims []string,
) (*runtime.CheckReport, error) {
	inputs := executionInputs(filterRepoScopedReadPaths(repoRoot, readPaths), writePaths, writeEpochs, commands, cmdResults, claims)
	report, err := evaluator.CheckRepoPolicy(repoRoot, inputs)
	if err != nil {
		return nil, err
	}
	if err := writeLatestReport(repoRoot, sessionID, report); err != nil {
		return nil, err
	}
	return report, nil
}

func runPreWritePolicyCheckWithEvaluator(
	evaluator *runtime.Evaluator,
	repoRoot string,
	readPaths, writePaths []string,
	writeEpochs map[string]uint64,
	commands []string,
	cmdResults []CommandResult,
	claims []string,
) (*runtime.CheckReport, error) {
	inputs := executionInputs(filterRepoScopedReadPaths(repoRoot, readPaths), writePaths, writeEpochs, commands, cmdResults, claims)
	return evaluator.CheckRepoPolicyForKinds(repoRoot, inputs, preWriteBlockKinds)
}

func runPreCommandPolicyCheckWithEvaluator(evaluator *runtime.Evaluator, repoRoot string, state SessionState, command string) (*runtime.CheckReport, error) {
	inputs := executionInputs(filterRepoScopedReadPaths(repoRoot, state.ReadPaths), state.WritePaths, state.WriteEpochs, []string{command}, state.CommandResults, state.Claims)
	return evaluator.CheckRepoPolicyForPreCommand(repoRoot, inputs)
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
	path := raw
	if path == "" {
		return false
	}
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) {
		return true
	}
	root, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return false
	}
	cleaned, err := pathidentity.ResolveProspective(filepath.Clean(path))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func executionInputs(
	readPaths, writePaths []string,
	writeEpochs map[string]uint64,
	commands []string,
	cmdResults []CommandResult,
	claims []string,
) runtime.ExecutionInputs {
	evalResults := make([]runtime.CommandResult, len(cmdResults))
	for i, r := range cmdResults {
		evalResults[i] = runtime.CommandResult{
			Command:       r.Command,
			Outcome:       r.Outcome,
			EvidenceEpoch: r.EvidenceEpoch,
		}
	}
	return runtime.ExecutionInputs{
		ReadPaths:      readPaths,
		WritePaths:     writePaths,
		WriteEpochs:    writeEpochs,
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
	limit := strings.TrimSpace(state.EvidenceOverflowLimit)
	if limit == "" {
		limit = "unknown_limit"
	}
	return fmt.Sprintf("reconc evidence is uncertified because %s exceeded %s. Material tools, claims, policy passes, and completion remain blocked across sessions; explicit user interrupt can still terminate the host session.", field, limit)
}

// writeLatestReport persists the CheckReport JSON to the session's
// reports/<id>.json path. Atomic via tmp-rename.
func writeLatestReport(repoRoot, sessionID string, report *runtime.CheckReport) error {
	path := sessionReportPath(repoRoot, sessionID)
	if err := ensurePrivateStateDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("mkdir reports dir: %w", err)
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxStopReportBytes {
		return fmt.Errorf("report exceeds %d bytes", maxStopReportBytes)
	}
	if _, err := atomicfile.WriteIfChanged(path, body, 0o600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// --- violation helpers ---------------------------------------------

func preWriteBlockingViolations(report *runtime.CheckReport) []runtime.Violation {
	return blockingViolationsForKinds(report, preWriteBlockKinds)
}

func blockingViolationsForKinds(report *runtime.CheckReport, kinds map[policy.Kind]struct{}) []runtime.Violation {
	out := []runtime.Violation{}
	for _, v := range report.Violations {
		if _, blocking := blockingModes[v.Mode]; !blocking {
			continue
		}
		if _, ok := kinds[v.Kind]; !ok {
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
