package agentsession

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

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
	now := time.Now().UTC()
	if _, err := mutateSessionStateResolved(root, parsed.SessionID, func(state SessionState) SessionState {
		return reapPendingToolCalls(state, now)
	}); err != nil {
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
	return runAntigravityPreToolUseResolvedWithEvaluatorAndStopCache(root, payloadBytes, evaluator, nil)
}

func runAntigravityPreToolUseResolvedWithEvaluatorAndStopCache(
	root string,
	payloadBytes []byte,
	evaluator *runtime.Evaluator,
	stopCache *StopDecisionCache,
) Result {
	payload, err := NormalizeAntigravityPayload("antigravity-pre-tool-use", payloadBytes)
	if err != nil {
		return AdaptAntigravityResult("antigravity-pre-tool-use", Result{ExitCode: 2, Stderr: err.Error()})
	}
	if parsed, parseErr := ParsePayload(payload); parseErr == nil {
		if antigravitySendsProcessInput(parsed) {
			return AdaptAntigravityResult("antigravity-pre-tool-use", Result{ExitCode: 2, Stderr: "reconc blocked manage_task send_input: input written into a running process cannot be bound to a command Reconc reviewed; run the command through run_command instead."})
		}
		if reason := antigravityOverwriteBlock(root, parsed.SessionID); reason != "" {
			return AdaptAntigravityResult("antigravity-pre-tool-use", Result{ExitCode: 2, Stderr: reason})
		}
	}
	result := runPreToolUseResolvedWithEvaluatorAndStopCache(root, payload, evaluator, stopCache)
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
		var correlationErr error
		createdAt := time.Now().UTC().UnixNano()
		updated, err = mutateSessionStateResolved(root, parsed.SessionID, func(state SessionState) SessionState {
			state = reapPendingToolCalls(state, time.Unix(0, createdAt))
			key, err := antigravityPendingKey(parsed)
			if err != nil {
				keyErr = err
				return state
			}
			state, correlationErr = putPendingToolCallTransient(state, key, PendingToolCall{
				ToolName:          parsed.ToolName,
				ToolInput:         cloneObject(parsed.ToolInput),
				ToolUseID:         parsed.ToolUseID,
				CreatedAtUnixNano: createdAt,
			})
			return state
		})
		if err != nil {
			return AdaptAntigravityResult("antigravity-pre-tool-use", Result{ExitCode: 2, Stderr: err.Error()})
		}
		if keyErr != nil {
			return AdaptAntigravityResult("antigravity-pre-tool-use", resultWithEncodingError(Result{ExitCode: 2}, keyErr))
		}
		if correlationErr != nil {
			return AdaptAntigravityResult("antigravity-pre-tool-use", Result{ExitCode: 2, Stderr: correlationErr.Error()})
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
	now := time.Now().UTC()
	_, err = mutateSessionStateResolved(root, parsed.SessionID, func(state SessionState) SessionState {
		key, err := antigravityPendingKey(parsed)
		if err != nil {
			keyErr = err
			return state
		}
		state, pending, found = takePendingToolCall(state, key, now)
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

	// A later hook's `overwrite` can change the arguments after Reconc checked
	// them, so the executed call reported by PostToolUse is the evidence.
	toolName, toolInput := pending.ToolName, pending.ToolInput
	overwritten, err := antigravityCallOverwritten(pending, parsed)
	if err != nil {
		return AdaptAntigravityResult("antigravity-post-tool-use", resultWithEncodingError(Result{ExitCode: 2}, err))
	}
	if overwritten {
		toolName, toolInput = parsed.ToolName, parsed.ToolInput
	}
	out := parsed.Raw
	out["tool_name"] = toolName
	out["tool_input"] = toolInput
	out["tool_use_id"] = pending.ToolUseID
	if parsed.Error != "" {
		out["error"] = parsed.Error
	}
	body, err := json.Marshal(out)
	if err != nil {
		return AdaptAntigravityResult("antigravity-post-tool-use", resultWithEncodingError(Result{ExitCode: 2}, fmt.Errorf("marshal Antigravity post-tool payload: %w", err)))
	}
	var result Result
	switch {
	case parsed.Error != "" && toolName == "Bash":
		result = runPostToolUseFailureResolved(root, body)
	case parsed.Error != "":
		result = Result{ExitCode: 0}
	default:
		result = runPostToolUseResolved(root, body)
	}
	if overwritten {
		warning := "reconc hook (Antigravity post, warn): the executed tool arguments differ from the arguments Reconc checked (another hook's overwrite); evidence records the executed call, and this conversation's later calls and Stop stay blocked"
		if _, err := mutateSessionStateResolved(root, parsed.SessionID, func(state SessionState) SessionState {
			state.HostArgumentOverwrite = toolName
			return state
		}); err != nil {
			warning += "; record the overwrite: " + err.Error()
		}
		if result.Stderr != "" {
			warning += "; " + result.Stderr
		}
		result.Stderr = warning
	}
	return AdaptAntigravityResult("antigravity-post-tool-use", result)
}

// antigravityCallOverwritten reports whether PostToolUse names a tool call
// other than the one PreToolUse allowed. Hosts that omit the executed call
// keep the checked call as the evidence.
func antigravityCallOverwritten(pending PendingToolCall, executed *HookPayload) (bool, error) {
	if executed.ToolName == "" && len(executed.ToolInput) == 0 {
		return false, nil
	}
	type call struct {
		Name  string                 `json:"name"`
		Input map[string]interface{} `json:"input"`
	}
	checked, err := json.Marshal(call{Name: pending.ToolName, Input: pending.ToolInput})
	if err != nil {
		return false, fmt.Errorf("marshal checked Antigravity tool call: %w", err)
	}
	ran, err := json.Marshal(call{Name: executed.ToolName, Input: executed.ToolInput})
	if err != nil {
		return false, fmt.Errorf("marshal executed Antigravity tool call: %w", err)
	}
	return string(checked) != string(ran), nil
}

func RunAntigravityPostInvocation(repoRoot string, payloadBytes []byte) Result {
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return antigravityResult(Result{ExitCode: 0, Stderr: err.Error()}, map[string]interface{}{"injectSteps": []interface{}{}, "terminationBehavior": ""})
	}
	return runAntigravityPostInvocationResolved(root.path, payloadBytes)
}

func runAntigravityPostInvocationResolved(root string, payloadBytes []byte) Result {
	payload, err := NormalizeAntigravityPayload("antigravity-post-invocation", payloadBytes)
	if err != nil {
		return antigravityResult(Result{ExitCode: 0, Stderr: err.Error()}, map[string]interface{}{"injectSteps": []interface{}{}, "terminationBehavior": ""})
	}
	parsed, err := ParsePayload(payload)
	if err != nil {
		return antigravityResult(Result{ExitCode: 0, Stderr: err.Error()}, map[string]interface{}{"injectSteps": []interface{}{}, "terminationBehavior": ""})
	}
	if _, err := mutateSessionStateResolved(root, parsed.SessionID, clearPendingToolCalls); err != nil {
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
	if parsed, parseErr := ParsePayload(payload); parseErr == nil {
		if reason := antigravityOverwriteBlock(root, parsed.SessionID); reason != "" {
			return AdaptAntigravityResult("antigravity-stop", Result{ExitCode: 2, Stderr: reason})
		}
	}
	return AdaptAntigravityResult("antigravity-stop", runStopResolvedWithEvaluatorAndCache(root, payload, runtimeName, evaluator, stopCache))
}

// antigravityOverwriteBlock holds a conversation in which another hook's
// `overwrite` changed checked arguments. Antigravity documents neither hook
// order nor how overwrites merge, so no Reconc check can see the arguments
// that run; only removing that hook and starting a new conversation restores
// enforcement. An unreadable state blocks as well.
func antigravityOverwriteBlock(root, sessionID string) string {
	state, err := readSessionStateResolved(root, sessionID)
	if err != nil {
		return "reconc blocked this Antigravity call: read session state: " + err.Error()
	}
	if state.HostArgumentOverwrite == "" {
		return ""
	}
	return fmt.Sprintf("reconc blocked this Antigravity call: another hook rewrote the arguments of an earlier %s call after Reconc checked them (Antigravity `overwrite`). Antigravity does not order hooks, so Reconc cannot check the arguments that run. Remove the hook that returns `overwrite` from .agents/hooks.json, ~/.gemini/config/hooks.json, the CLI settings, or the plugin that ships it, then start a new conversation.", state.HostArgumentOverwrite)
}

func NormalizeAntigravityPayload(event string, payloadBytes []byte) ([]byte, error) {
	if len(strings.TrimSpace(string(payloadBytes))) == 0 {
		return nil, fmt.Errorf("antigravity payload is empty")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("antigravity payload is not valid JSON: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("antigravity payload contains multiple JSON values")
		}
		return nil, fmt.Errorf("antigravity payload has trailing data: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("antigravity payload must be a JSON object")
	}
	out := cloneObject(raw)
	delete(out, "reconc_mcp")
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
				"reason":   resultReason(result, "reconc denied this Antigravity action."),
			})
		}
		return antigravityResult(Result{ExitCode: 0, Stderr: result.Stderr, Err: result.Err}, map[string]interface{}{"decision": "allow"})
	case "antigravity-post-tool-use":
		return antigravityResult(Result{ExitCode: 0, Stderr: result.Stderr, Err: result.Err}, map[string]interface{}{})
	case "antigravity-stop":
		if result.ExitCode != 0 || result.Err != nil {
			return antigravityResult(Result{ExitCode: 0, Err: result.Err}, map[string]interface{}{
				"decision": "continue",
				"reason":   resultReason(result, "reconc denied this Antigravity action."),
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
	input := cloneObject(args)
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
		// Literal write targets resolve against the command's directory.
		if directory := antigravityString(args, "Cwd"); directory != "" {
			input["cwd"] = directory
		}
		return "Bash", input
	default:
		return strings.TrimSpace(name), input
	}
}

// antigravitySendsProcessInput reports manage_task send_input, which types
// into a running run_command process outside the reviewed command line, like
// Devin's write_to_process. list, status, and kill pass.
func antigravitySendsProcessInput(payload *HookPayload) bool {
	if payload == nil || payload.ToolName != "manage_task" {
		return false
	}
	action, _ := payload.ToolInput["Action"].(string)
	return strings.TrimSpace(action) == "send_input"
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
		if sequence, valid := antigravityStepValue(value); valid {
			return "step:" + strconv.FormatUint(sequence, 10)
		}
	}
	if value, ok := raw["step_idx"]; ok {
		if sequence, valid := antigravityStepValue(value); valid {
			return "step:" + strconv.FormatUint(sequence, 10)
		}
	}
	return ""
}

func antigravityStepValue(value interface{}) (uint64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	sequence, err := strconv.ParseUint(string(number), 10, 64)
	return sequence, err == nil
}

func antigravityStopReason(stdout string) string {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &raw); err == nil {
		if decision, _ := raw["decision"].(string); decision == "block" {
			if reason, _ := raw["reason"].(string); strings.TrimSpace(reason) != "" {
				return boundHostReason(reason)
			}
		}
	}
	return boundHostReason(stdout)
}

func antigravityObject(raw map[string]interface{}, keys ...string) map[string]interface{} {
	for _, key := range keys {
		if value, ok := raw[key].(map[string]interface{}); ok {
			return cloneObject(value)
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

func antigravityResult(result Result, payload map[string]interface{}) Result {
	return resultWithHookJSON(result, payload)
}
