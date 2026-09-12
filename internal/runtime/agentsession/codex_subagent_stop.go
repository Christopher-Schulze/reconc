package agentsession

import (
	jsonv2 "encoding/json/v2"
	"fmt"
	"reflect"
	"strings"

	"reconc.dev/reconc/internal/runtime"
)

// A child turn checks policy using its own evidence. It must never inherit the
// repository TASK continuation or disable the parent's durable run mode. Codex
// does not emit SessionEnd for children; a turn stop must retain their evidence.
func runCodexSubagentStopResolved(root string, body []byte, evaluator *runtime.Evaluator, cache *StopDecisionCache) Result {
	payload, err := parseCodexSubagentStop(body)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc subagent stop: %s", err)}
	}
	if isUserStopInterrupt(payload) {
		return finishCodexSubagentTurn(root, payload.SessionID, true, "Subagent turn interrupted; completion remains uncertified.")
	}
	state, err := ensureSessionStateResolved(root, payload.SessionID)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc subagent stop: %s", err)}
	}
	if state.EvidenceOverflow {
		return finishCodexSubagentTurn(root, state.SessionID, true,
			evidenceOverflowMessage(state)+" Subagent turn released as uncertified.")
	}
	checked, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(root, state, evaluator, cache, nil)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc subagent stop: policy check failed: %s", err)}
	}
	return codexSubagentPolicyDecision(root, payload, state, checked)
}

func codexSubagentPolicyDecision(root string, payload *HookPayload, state SessionState, checked stopPolicyCheckResult) Result {
	violations := blockingViolations(checked.Report)
	if len(violations) == 0 {
		currentTask, err := captureStopTaskSnapshot(root)
		if err != nil || !reflect.DeepEqual(checked.TaskSnapshot, currentTask) || checked.GitSnapshot != stopPolicyGitSnapshotFor(root) {
			return Result{ExitCode: 2, Stderr: "reconc subagent stop: repository state changed or could not be verified after policy evaluation"}
		}
		return finishCodexSubagentTurn(root, state.SessionID, false, "")
	}
	// Codex owns this recursion flag. Release the repeated child turn explicitly
	// as uncertified, even if a compatibility caller requests strict continuation.
	if payload.StopHookActive {
		return finishCodexSubagentTurn(root, state.SessionID, true,
			firstLinesForViolations(violations, "Subagent turn released as uncertified after its policy remediation attempt."))
	}
	output, err := stopBlockJSONOutput(root, state.SessionID, checked.Report, violations)
	if err != nil && output == "" {
		return resultWithEncodingError(Result{ExitCode: 2}, err)
	}
	return Result{Stdout: output, Stderr: stopBlockStateDiagnostic(err)}
}

func parseCodexSubagentStop(body []byte) (*HookPayload, error) {
	var identity struct {
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
		Event     string `json:"hook_event_name"`
	}
	if err := jsonv2.Unmarshal(body, &identity); err != nil {
		return nil, fmt.Errorf("decode child identity: %w", err)
	}
	if identity.SessionID == "" || strings.TrimSpace(identity.SessionID) != identity.SessionID {
		return nil, fmt.Errorf("missing or malformed child session identity")
	}
	// Older adapter callers omit native metadata. Native input reaches this
	// handler after NormalizeCodexPayload replaces the shared session identity
	// with agent_id; an unnormalized request must never use the parent's evidence.
	if identity.AgentID != "" && identity.AgentID != identity.SessionID {
		return nil, fmt.Errorf("agent_id differs from child session_id")
	}
	if identity.Event != "" && identity.Event != "SubagentStop" {
		return nil, fmt.Errorf("unexpected child lifecycle event %q", identity.Event)
	}
	return ParsePayload(body)
}

func finishCodexSubagentTurn(root, sessionID string, uncertified bool, diagnostic string) Result {
	_, err := mutateSessionStateResolved(root, sessionID, func(state SessionState) SessionState {
		state.UncertifiedTermination = uncertified
		if !uncertified {
			state.LastStopBlockViolationHash = ""
		}
		return state
	})
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc subagent stop: persist turn outcome: %s", err)}
	}
	return Result{Stderr: diagnostic}
}
