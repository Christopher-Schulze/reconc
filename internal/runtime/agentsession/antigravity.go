package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"reconc.dev/reconc/internal/retention"
	"reconc.dev/reconc/internal/runtime"
)

func RunAntigravityPreInvocation(repoRoot string, payloadBytes []byte) Result {
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return antigravityResult(Result{ExitCode: 0, Stderr: err.Error()}, map[string]interface{}{"injectSteps": []interface{}{}})
	}
	return runAntigravityPreInvocationResolved(root.path, payloadBytes)
}

func runAntigravityPreInvocationResolved(root string, payloadBytes []byte) Result {
	payload, err := NormalizeAntigravityPayload("antigravity-pre-invocation", payloadBytes)
	if err != nil {
		return antigravityResult(Result{ExitCode: 0, Stderr: err.Error()}, map[string]interface{}{"injectSteps": []interface{}{}})
	}
	parsed, err := ParsePayload(payload)
	if err != nil {
		return antigravityResult(Result{ExitCode: 0, Stderr: err.Error()}, map[string]interface{}{"injectSteps": []interface{}{}})
	}
	if _, err := ensureSessionStateResolved(root, parsed.SessionID); err != nil {
		return antigravityResult(Result{ExitCode: 0, Stderr: err.Error()}, map[string]interface{}{"injectSteps": []interface{}{}})
	}
	retentionStderr := retentionWarning(retention.RunIfDue(retention.Options{RepoRoot: root, StateRoot: stateRoot(), ActiveSession: parsed.SessionID}))
	return antigravityResult(Result{ExitCode: 0, Stderr: retentionStderr}, map[string]interface{}{"injectSteps": []interface{}{}})
}

func RunAntigravityPreToolUse(repoRoot string, payloadBytes []byte) Result {
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return AdaptAntigravityResult("antigravity-pre-tool-use", Result{ExitCode: 2, Stderr: err.Error()})
	}
	return runAntigravityPreToolUseResolved(root.path, payloadBytes)
}

func runAntigravityPreToolUseResolved(root string, payloadBytes []byte) Result {
	return runAntigravityPreToolUseResolvedWithEvaluator(root, payloadBytes, runtime.NewEvaluator())
}

func runAntigravityPreToolUseResolvedWithEvaluator(root string, payloadBytes []byte, evaluator *runtime.Evaluator) Result {
	payload, err := NormalizeAntigravityPayload("antigravity-pre-tool-use", payloadBytes)
	if err != nil {
		return AdaptAntigravityResult("antigravity-pre-tool-use", Result{ExitCode: 2, Stderr: err.Error()})
	}
	result := runPreToolUseResolvedWithEvaluator(root, payload, evaluator)
	if result.ExitCode != 0 || result.Err != nil {
		return AdaptAntigravityResult("antigravity-pre-tool-use", result)
	}
	parsed, err := ParsePayload(payload)
	if err != nil {
		return AdaptAntigravityResult("antigravity-pre-tool-use", Result{ExitCode: 2, Stderr: err.Error()})
	}
	if parsed.IsReadTool() || parsed.IsWriteTool() || parsed.IsCommandTool() {
		var updated SessionState
		var keyErr error
		updated, err = mutateSessionStateResolved(root, parsed.SessionID, func(state SessionState) SessionState {
			key, err := antigravityPendingKey(parsed)
			if err != nil {
				keyErr = err
				return state
			}
			return PutPendingToolCall(state, key, PendingToolCall{
				ToolName:  parsed.ToolName,
				ToolInput: cloneAntigravityObject(parsed.ToolInput),
				ToolUseID: parsed.ToolUseID,
			})
		})
		if err != nil {
			return AdaptAntigravityResult("antigravity-pre-tool-use", Result{ExitCode: 2, Stderr: err.Error()})
		}
		if keyErr != nil {
			return AdaptAntigravityResult("antigravity-pre-tool-use", resultWithEncodingError(Result{ExitCode: 2}, keyErr))
		}
		if updated.EvidenceOverflow {
			return AdaptAntigravityResult("antigravity-pre-tool-use", Result{ExitCode: 2, Stderr: evidenceOverflowMessage(updated)})
		}
	}
	return AdaptAntigravityResult("antigravity-pre-tool-use", result)
}

func RunAntigravityPostToolUse(repoRoot string, payloadBytes []byte) Result {
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return AdaptAntigravityResult("antigravity-post-tool-use", Result{ExitCode: 0, Stderr: err.Error()})
	}
	return runAntigravityPostToolUseResolved(root.path, payloadBytes)
}

func runAntigravityPostToolUseResolved(root string, payloadBytes []byte) Result {
	payload, err := NormalizeAntigravityPayload("antigravity-post-tool-use", payloadBytes)
	if err != nil {
		return AdaptAntigravityResult("antigravity-post-tool-use", Result{ExitCode: 0, Stderr: err.Error()})
	}
	parsed, err := ParsePayload(payload)
	if err != nil {
		return AdaptAntigravityResult("antigravity-post-tool-use", Result{ExitCode: 0, Stderr: err.Error()})
	}

	var pending PendingToolCall
	var found bool
	var keyErr error
	_, err = mutateSessionStateResolved(root, parsed.SessionID, func(state SessionState) SessionState {
		key, err := antigravityPendingKey(parsed)
		if err != nil {
			keyErr = err
			return state
		}
		if state.PendingToolCalls != nil {
			pending, found = state.PendingToolCalls[key]
			delete(state.PendingToolCalls, key)
			if len(state.PendingToolCalls) == 0 {
				state.PendingToolCalls = nil
			}
		}
		return state
	})
	if err != nil {
		return AdaptAntigravityResult("antigravity-post-tool-use", Result{ExitCode: 0, Stderr: err.Error()})
	}
	if keyErr != nil {
		return AdaptAntigravityResult("antigravity-post-tool-use", resultWithEncodingError(Result{ExitCode: 2}, keyErr))
	}
	if !found {
		return AdaptAntigravityResult("antigravity-post-tool-use", Result{ExitCode: 0})
	}

	out := parsed.Raw
	out["tool_name"] = pending.ToolName
	out["tool_input"] = pending.ToolInput
	out["tool_use_id"] = pending.ToolUseID
	if parsed.Error != "" {
		out["error"] = parsed.Error
	}
	body, err := json.Marshal(out)
	if err != nil {
		return AdaptAntigravityResult("antigravity-post-tool-use", resultWithEncodingError(Result{ExitCode: 2}, fmt.Errorf("marshal Antigravity post-tool payload: %w", err)))
	}
	if parsed.Error != "" && pending.ToolName == "Bash" {
		return AdaptAntigravityResult("antigravity-post-tool-use", runPostToolUseFailureResolved(root, body))
	}
	if parsed.Error != "" {
		return AdaptAntigravityResult("antigravity-post-tool-use", Result{ExitCode: 0})
	}
	return AdaptAntigravityResult("antigravity-post-tool-use", runPostToolUseResolved(root, body))
}

func RunAntigravityPostInvocation(repoRoot string, payloadBytes []byte) Result {
	if _, err := ResolveRepoRootRef(repoRoot); err != nil {
		return antigravityResult(Result{ExitCode: 0, Stderr: err.Error()}, map[string]interface{}{"injectSteps": []interface{}{}, "terminationBehavior": ""})
	}
	return runAntigravityPostInvocationResolved(payloadBytes)
}

func runAntigravityPostInvocationResolved(payloadBytes []byte) Result {
	payload, err := NormalizeAntigravityPayload("antigravity-post-invocation", payloadBytes)
	if err != nil {
		return antigravityResult(Result{ExitCode: 0, Stderr: err.Error()}, map[string]interface{}{"injectSteps": []interface{}{}, "terminationBehavior": ""})
	}
	if _, err := ParsePayload(payload); err != nil {
		return antigravityResult(Result{ExitCode: 0, Stderr: err.Error()}, map[string]interface{}{"injectSteps": []interface{}{}, "terminationBehavior": ""})
	}
	return antigravityResult(Result{ExitCode: 0}, map[string]interface{}{"injectSteps": []interface{}{}, "terminationBehavior": ""})
}

func RunAntigravityStop(repoRoot string, payloadBytes []byte) Result {
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return AdaptAntigravityResult("antigravity-stop", Result{ExitCode: 2, Stderr: err.Error()})
	}
	return runAntigravityStopResolved(root.path, payloadBytes, "")
}

func runAntigravityStopResolved(root string, payloadBytes []byte, runtimeName string) Result {
	return runAntigravityStopResolvedWithEvaluator(root, payloadBytes, runtimeName, runtime.NewEvaluator())
}

func runAntigravityStopResolvedWithEvaluator(root string, payloadBytes []byte, runtimeName string, evaluator *runtime.Evaluator) Result {
	return runAntigravityStopResolvedWithEvaluatorAndCache(root, payloadBytes, runtimeName, evaluator, nil)
}

func runAntigravityStopResolvedWithEvaluatorAndCache(
	root string,
	payloadBytes []byte,
	runtimeName string,
	evaluator *runtime.Evaluator,
	stopCache *StopDecisionCache,
) Result {
	payload, err := NormalizeAntigravityPayload("antigravity-stop", payloadBytes)
	if err != nil {
		return AdaptAntigravityResult("antigravity-stop", Result{ExitCode: 2, Stderr: err.Error()})
	}
	return AdaptAntigravityResult("antigravity-stop", runStopResolvedWithEvaluatorAndCache(root, payload, runtimeName, evaluator, stopCache))
}

func NormalizeAntigravityPayload(event string, payloadBytes []byte) ([]byte, error) {
	if len(strings.TrimSpace(string(payloadBytes))) == 0 {
		return nil, fmt.Errorf("antigravity payload is empty")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return nil, fmt.Errorf("antigravity payload is not valid JSON: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("antigravity payload must be a JSON object")
	}
	out := cloneAntigravityObject(raw)
	out["session_id"] = antigravitySessionID(raw)
	out["antigravity_event"] = event
	if key := antigravityStepKey(raw); key != "" {
		out["tool_use_id"] = key
	}
	if errString := antigravityString(raw, "error"); errString != "" {
		out["error"] = errString
	}
	if reason := strings.ToLower(antigravityString(raw, "terminationReason", "termination_reason")); strings.Contains(reason, "interrupt") || strings.Contains(reason, "abort") {
		out["is_interrupt"] = true
	}
	if toolCall := antigravityObject(raw, "toolCall", "tool_call"); len(toolCall) > 0 {
		toolName := antigravityString(toolCall, "name")
		args := antigravityObject(toolCall, "args", "arguments")
		mappedName, input := normalizeAntigravityTool(toolName, args)
		if mappedName != "" {
			out["tool_name"] = mappedName
		}
		out["tool_input"] = input
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("antigravity payload normalize: %w", err)
	}
	return body, nil
}

func AdaptAntigravityResult(event string, result Result) Result {
	switch event {
	case "antigravity-pre-tool-use":
		if result.ExitCode != 0 || result.Err != nil {
			return antigravityResult(Result{ExitCode: 0, Err: result.Err}, map[string]interface{}{
				"decision": "deny",
				"reason":   antigravityResultReason(result),
			})
		}
		return antigravityResult(Result{ExitCode: 0, Stderr: result.Stderr, Err: result.Err}, map[string]interface{}{"decision": "allow"})
	case "antigravity-post-tool-use":
		return antigravityResult(Result{ExitCode: 0, Stderr: result.Stderr, Err: result.Err}, map[string]interface{}{})
	case "antigravity-stop":
		if result.ExitCode != 0 || result.Err != nil {
			return antigravityResult(Result{ExitCode: 0, Err: result.Err}, map[string]interface{}{
				"decision": "continue",
				"reason":   antigravityResultReason(result),
			})
		}
		reason := antigravityStopReason(result.Stdout)
		if reason == "" {
			return antigravityResult(Result{ExitCode: 0, Stderr: result.Stderr, Err: result.Err}, map[string]interface{}{"decision": "stop"})
		}
		return antigravityResult(Result{ExitCode: 0, Stderr: result.Stderr, Err: result.Err}, map[string]interface{}{
			"decision": "continue",
			"reason":   reason,
		})
	default:
		if result.Stdout == "" {
			return antigravityResult(Result{ExitCode: 0, Stderr: result.Stderr, Err: result.Err}, map[string]interface{}{})
		}
		return result
	}
}

func normalizeAntigravityTool(name string, args map[string]interface{}) (string, map[string]interface{}) {
	input := cloneAntigravityObject(args)
	switch strings.TrimSpace(name) {
	case "view_file":
		copyAntigravityPath(input, args, "AbsolutePath")
		return "Read", input
	case "write_to_file", "replace_file_content", "multi_replace_file_content":
		copyAntigravityPath(input, args, "TargetFile")
		return "Write", input
	case "list_dir":
		copyAntigravityPath(input, args, "DirectoryPath")
		return "Read", input
	case "find_by_name":
		copyAntigravityPath(input, args, "SearchDirectory")
		return "Read", input
	case "grep_search":
		copyAntigravityPath(input, args, "SearchPath")
		return "Read", input
	case "run_command":
		if command := antigravityString(args, "CommandLine", "command", "cmd"); command != "" {
			input["command"] = command
		}
		return "Bash", input
	default:
		return strings.TrimSpace(name), input
	}
}

func copyAntigravityPath(input, args map[string]interface{}, key string) {
	if path := antigravityString(args, key, "file_path", "path"); path != "" {
		input["file_path"] = path
	}
}

func antigravityPendingKey(payload *HookPayload) (string, error) {
	if payload == nil {
		return "step:unknown", nil
	}
	if payload.ToolUseID != "" {
		return payload.ToolUseID, nil
	}
	body, err := json.Marshal(struct {
		ToolName  string                 `json:"tool_name"`
		ToolInput map[string]interface{} `json:"tool_input"`
	}{
		ToolName:  payload.ToolName,
		ToolInput: payload.ToolInput,
	})
	if err != nil {
		return "", fmt.Errorf("marshal Antigravity pending-tool identity: %w", err)
	}
	sum := sha256.Sum256(body)
	return "tool:" + hex.EncodeToString(sum[:]), nil
}

func antigravitySessionID(raw map[string]interface{}) string {
	if sessionID := antigravityString(raw, "conversationId", "conversation_id", "sessionId", "session_id"); sessionID != "" {
		return sessionID
	}
	return "antigravity-workspace"
}

func antigravityStepKey(raw map[string]interface{}) string {
	if value, ok := raw["stepIdx"]; ok {
		return "step:" + fmt.Sprint(value)
	}
	if value, ok := raw["step_idx"]; ok {
		return "step:" + fmt.Sprint(value)
	}
	return ""
}

func antigravityStopReason(stdout string) string {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &raw); err == nil {
		if decision, _ := raw["decision"].(string); decision == "block" {
			if reason, _ := raw["reason"].(string); strings.TrimSpace(reason) != "" {
				return strings.TrimSpace(reason)
			}
		}
	}
	return strings.TrimSpace(stdout)
}

func antigravityResultReason(result Result) string {
	for _, candidate := range []string{result.Stderr, result.Stdout, "reconc denied this Antigravity action."} {
		if reason := strings.TrimSpace(candidate); reason != "" {
			return reason
		}
	}
	return "reconc denied this Antigravity action."
}

func antigravityObject(raw map[string]interface{}, keys ...string) map[string]interface{} {
	for _, key := range keys {
		if value, ok := raw[key].(map[string]interface{}); ok {
			return cloneAntigravityObject(value)
		}
	}
	return map[string]interface{}{}
}

func antigravityString(raw map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := raw[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneAntigravityObject(raw map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(raw))
	for key, value := range raw {
		out[key] = value
	}
	return out
}

func antigravityResult(result Result, payload map[string]interface{}) Result {
	return resultWithHookJSON(result, payload)
}
