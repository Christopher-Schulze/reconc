package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"reconc.dev/reconc/internal/runtime"
)

// Devin's native tool_use_id and prompt_id bind a pre/post pair. Persist only
// a digest of the allowed input, never the prompt, command body, or file text.
func devinToolDigest(payload *HookPayload) (string, error) {
	body, err := json.Marshal(struct {
		Name  string                 `json:"name"`
		Input map[string]interface{} `json:"input"`
	}{Name: payload.ToolName, Input: payload.ToolInput})
	if err != nil {
		return "", fmt.Errorf("marshal Devin tool identity: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func runDevinPreToolUseResolved(root string, body []byte, evaluator *runtime.Evaluator, stopCache *StopDecisionCache) Result {
	result := runPreDecisionResolvedWithEvaluatorAndStopCache(root, body, false, evaluator, stopCache)
	payload, err := ParsePayload(body)
	if err != nil || payload.ToolUseID == "" {
		return Result{ExitCode: 2, Stderr: "reconc hook (Devin pre): missing bound tool call"}
	}
	if !payload.IsReadTool() && !payload.IsWriteTool() && !payload.IsCommandTool() {
		return result
	}
	key := "devin:" + payload.ToolUseID
	if result.ExitCode != 0 || result.Err != nil {
		_, _ = mutateSessionStateResolved(root, payload.SessionID, func(state SessionState) SessionState {
			return removeDevinPending(state, key)
		})
		return result
	}
	digest, err := devinToolDigest(payload)
	if err != nil {
		return resultWithEncodingError(Result{ExitCode: 2}, err)
	}
	createdAt := time.Now().UTC().UnixNano()
	state, err := mutateSessionStateResolved(root, payload.SessionID, func(state SessionState) SessionState {
		state = pruneDevinPending(state, time.Unix(0, createdAt))
		state = removeDevinPending(state, key)
		return PutPendingToolCall(state, key, PendingToolCall{
			ToolName:          payload.ToolName,
			ToolInput:         map[string]interface{}{"sha256": digest},
			ToolUseID:         payload.ToolUseID,
			CreatedAtUnixNano: createdAt,
		})
	})
	if err != nil {
		return Result{ExitCode: 2, Stderr: "reconc hook (Devin pre): persist tool correlation: " + err.Error()}
	}
	if state.EvidenceOverflow {
		return Result{ExitCode: 2, Stderr: "reconc hook (Devin pre): tool correlation capacity unavailable"}
	}
	return result
}

func consumeDevinToolCorrelation(root string, payload *HookPayload) (bool, error) {
	if payload.ToolUseID == "" {
		return false, nil
	}
	key := "devin:" + payload.ToolUseID
	var pending PendingToolCall
	var found bool
	_, err := mutateSessionStateResolved(root, payload.SessionID, func(state SessionState) SessionState {
		pending, found = state.PendingToolCalls[key]
		return removeDevinPending(state, key)
	})
	if err != nil || !found || pendingToolCallExpired(pending, time.Now().UTC()) {
		return false, err
	}
	digest, err := devinToolDigest(payload)
	if err != nil {
		return false, err
	}
	stored, _ := pending.ToolInput["sha256"].(string)
	return pending.ToolName == payload.ToolName && pending.ToolUseID == payload.ToolUseID && stored == digest, nil
}

func pruneDevinPending(state SessionState, now time.Time) SessionState {
	for key, call := range state.PendingToolCalls {
		if strings.HasPrefix(key, "devin:") && pendingToolCallExpired(call, now) {
			state = removeDevinPending(state, key)
		}
	}
	return state
}

func removeDevinPending(state SessionState, key string) SessionState {
	if _, found := state.PendingToolCalls[key]; !found {
		return state
	}
	pending := make(map[string]PendingToolCall, len(state.PendingToolCalls)-1)
	for current, call := range state.PendingToolCalls {
		if current != key {
			pending[current] = call
		}
	}
	state.PendingToolCalls = pending
	if len(pending) == 0 {
		state.PendingToolCalls = nil
	}
	return state
}
