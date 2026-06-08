package agentsession

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// NormalizeCursorPayload converts Cursor Desktop hook payloads into the
// internal Claude/Codex-shaped payload contract used by the runtime handlers.
// Unknown fields stay in Raw so future Cursor fields remain diagnosable.
func NormalizeCursorPayload(event string, payloadBytes []byte) ([]byte, error) {
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return nil, fmt.Errorf("cursor payload is empty")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return nil, fmt.Errorf("cursor payload is not valid JSON: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("cursor payload must be a JSON object")
	}

	out := cloneCursorObject(raw)
	out["session_id"] = cursorSessionID(raw)
	out["cursor_event"] = event
	if prompt := cursorFirstString(raw, "prompt", "user_prompt", "userPrompt", "message", "text", "input"); prompt != "" {
		out["prompt"] = prompt
	}
	if v, ok := cursorFirstBool(raw, "stop_hook_active", "stopHookActive", "isStopHookActive"); ok {
		out["stop_hook_active"] = v
	}
	if v, ok := cursorFirstBool(raw, "is_interrupt", "isInterrupt", "interrupted", "aborted"); ok {
		out["is_interrupt"] = v
	}

	switch event {
	case "cursor-pre-tool-use", "cursor-post-tool-use":
		toolName := normalizeCursorToolName(cursorToolName(raw))
		if toolName != "" {
			out["tool_name"] = toolName
		}
		out["tool_input"] = cursorToolInput(raw)
		if response := cursorToolResponse(raw); len(response) > 0 {
			out["tool_response"] = response
		}
	case "cursor-before-shell-execution", "cursor-after-shell-execution":
		out["tool_name"] = "Bash"
		out["tool_input"] = cursorShellInput(raw)
		if event == "cursor-after-shell-execution" {
			out["tool_response"] = cursorShellResponse(raw)
		}
	case "cursor-before-read-file", "cursor-before-tab-file-read":
		out["tool_name"] = "Read"
		out["tool_input"] = cursorPathInput(raw)
	case "cursor-after-file-edit", "cursor-after-tab-file-edit":
		out["tool_name"] = "Write"
		out["tool_input"] = cursorPathInput(raw)
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("cursor payload normalize: %w", err)
	}
	return body, nil
}

// PayloadLooksLikeCursor reports whether a raw hook payload came from Cursor
// Desktop before event-specific normalization. Cursor can execute compatible
// Claude project hooks in the same workspace; those duplicate hooks must not
// mutate Reconc session/degenmode state for the Cursor run.
func PayloadLooksLikeCursor(payloadBytes []byte) bool {
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return false
	}
	if !payloadHasCursorDetectionKey(payloadBytes) {
		return false
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return false
	}
	return cursorObjectLooksLikeCursor(raw)
}

func payloadHasCursorDetectionKey(payloadBytes []byte) bool {
	if bytes.Contains(payloadBytes, []byte(`"cursor_version"`)) ||
		bytes.Contains(payloadBytes, []byte(`"cursorVersion"`)) ||
		bytes.Contains(payloadBytes, []byte(`"cursor_event"`)) {
		return true
	}
	if bytes.Contains(payloadBytes, []byte(`"hook_event_name"`)) &&
		bytes.Contains(payloadBytes, []byte(`"workspace_roots"`)) {
		return true
	}
	return bytes.Contains(payloadBytes, []byte(`"conversation_id"`)) &&
		bytes.Contains(payloadBytes, []byte(`"generation_id"`))
}

func cursorObjectLooksLikeCursor(raw map[string]interface{}) bool {
	if raw == nil {
		return false
	}
	if _, ok := raw["cursor_version"]; ok {
		return true
	}
	if _, ok := raw["cursorVersion"]; ok {
		return true
	}
	if _, ok := raw["cursor_event"]; ok {
		return true
	}
	_, hasHookEvent := raw["hook_event_name"]
	_, hasWorkspaceRoots := raw["workspace_roots"]
	if hasHookEvent && hasWorkspaceRoots {
		return true
	}
	_, hasConversation := raw["conversation_id"]
	_, hasGeneration := raw["generation_id"]
	return hasConversation && hasGeneration
}

// AdaptCursorResult maps internal hook results to Cursor hook response JSON.
// Cursor uses permission/user_message for pre hooks and followup_message for
// stop continuations, while Reconc's internal stop result uses decision/reason.
func AdaptCursorResult(event string, result Result) Result {
	if event == "cursor-stop" {
		if result.ExitCode != 0 {
			reason := cursorResultReason(result)
			return Result{
				ExitCode: 0,
				Stdout: cursorJSON(map[string]interface{}{
					"continue":      false,
					"user_message":  reason,
					"agent_message": reason,
				}),
			}
		}
		if strings.TrimSpace(result.Stdout) == "" {
			return cursorAllowResult(result)
		}
		reason := cursorStopReason(result.Stdout)
		if reason == "" {
			return cursorAllowResult(result)
		}
		return Result{
			ExitCode: 0,
			Stdout:   cursorJSON(map[string]interface{}{"followup_message": reason}),
		}
	}
	if event == "cursor-user-prompt-submit" {
		if result.ExitCode == 0 {
			if strings.TrimSpace(result.Stdout) == "" {
				return cursorAllowResult(result)
			}
			return result
		}
		reason := cursorResultReason(result)
		return Result{
			ExitCode: 0,
			Stdout:   cursorJSON(map[string]interface{}{"continue": false, "user_message": reason}),
		}
	}
	if isCursorPreDecisionEvent(event) && result.ExitCode != 0 {
		reason := cursorResultReason(result)
		return Result{
			ExitCode: 0,
			Stdout: cursorJSON(map[string]interface{}{
				"permission":    "deny",
				"user_message":  reason,
				"agent_message": reason,
			}),
		}
	}
	if isCursorPreDecisionEvent(event) && result.ExitCode == 0 && strings.TrimSpace(result.Stdout) == "" {
		return cursorAllowResult(result)
	}
	if isCursorObservationEvent(event) && result.ExitCode == 0 {
		return cursorAllowResult(result)
	}
	if strings.HasPrefix(event, "cursor-") && result.ExitCode == 0 && strings.TrimSpace(result.Stdout) == "" {
		return cursorAllowResult(result)
	}
	return result
}

func isCursorPreDecisionEvent(event string) bool {
	switch event {
	case "cursor-pre-tool-use",
		"cursor-before-shell-execution",
		"cursor-before-mcp-execution",
		"cursor-before-read-file",
		"cursor-before-tab-file-read":
		return true
	default:
		return false
	}
}

func isCursorObservationEvent(event string) bool {
	switch event {
	case "cursor-post-tool-use",
		"cursor-after-shell-execution",
		"cursor-after-mcp-execution",
		"cursor-after-file-edit",
		"cursor-after-tab-file-edit",
		"cursor-session-end":
		return true
	default:
		return false
	}
}

func cursorResultReason(result Result) string {
	for _, candidate := range []string{result.Stderr, result.Stdout, "reconc denied this Cursor action."} {
		if reason := strings.TrimSpace(candidate); reason != "" {
			return reason
		}
	}
	return "reconc denied this Cursor action."
}

func cursorStopReason(stdout string) string {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &raw); err == nil {
		for _, key := range []string{"reason", "followup_message", "message"} {
			if reason, _ := raw[key].(string); strings.TrimSpace(reason) != "" {
				return strings.TrimSpace(reason)
			}
		}
	}
	return strings.TrimSpace(stdout)
}

func cursorJSON(payload map[string]interface{}) string {
	body, _ := json.Marshal(payload)
	return string(body)
}

func cursorAllowResult(result Result) Result {
	return Result{
		ExitCode: 0,
		Stdout: cursorJSON(map[string]interface{}{
			"continue":   true,
			"permission": "allow",
		}),
		Stderr: result.Stderr,
	}
}

func cursorSessionID(raw map[string]interface{}) string {
	if sessionID := cursorFirstString(raw,
		"session_id", "sessionId", "conversation_id", "conversationId",
		"generation_id", "generationId", "request_id", "requestId",
		"workspace_id", "workspaceId", "project_id", "projectId",
	); sessionID != "" {
		return sessionID
	}
	return "cursor-workspace"
}

func cursorToolName(raw map[string]interface{}) string {
	if toolName := cursorFirstString(raw, "tool_name", "toolName", "name"); toolName != "" {
		return toolName
	}
	if nested, ok := raw["tool"].(map[string]interface{}); ok {
		return cursorFirstString(nested, "name", "tool_name", "toolName")
	}
	return ""
}

func normalizeCursorToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	cleaned := strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(trimmed))
	switch cleaned {
	case "shell", "bash", "terminal":
		return "Bash"
	case "read", "tabread":
		return "Read"
	case "write", "edit", "multiedit", "tabwrite", "strreplace", "delete", "fileedit":
		return "Write"
	default:
		return trimmed
	}
}

func cursorToolInput(raw map[string]interface{}) map[string]interface{} {
	input := cursorFirstObject(raw, "tool_input", "toolInput", "input", "args")
	cursorAddPath(raw, input)
	if command := cursorFirstString(raw, "command", "cmd", "script"); command != "" && strings.TrimSpace(cursorString(input, "command")) == "" {
		input["command"] = command
	}
	return input
}

func cursorToolResponse(raw map[string]interface{}) map[string]interface{} {
	response := cursorFirstObject(raw, "tool_response", "toolResponse", "response", "result", "output")
	for _, key := range []string{"exit_code", "exitCode", "status_code", "statusCode", "stdout", "stderr", "error"} {
		if value, ok := raw[key]; ok {
			response[key] = value
		}
	}
	return response
}

func cursorShellInput(raw map[string]interface{}) map[string]interface{} {
	input := cursorToolInput(raw)
	if strings.TrimSpace(cursorString(input, "command")) == "" {
		if command := cursorFirstString(raw, "command", "cmd", "script"); command != "" {
			input["command"] = command
		}
	}
	return input
}

func cursorShellResponse(raw map[string]interface{}) map[string]interface{} {
	response := cursorToolResponse(raw)
	for _, key := range []string{"exit_code", "exitCode", "status_code", "statusCode"} {
		if _, ok := response[key]; ok {
			return response
		}
	}
	if code, ok := raw["code"]; ok {
		response["exit_code"] = code
	}
	return response
}

func cursorPathInput(raw map[string]interface{}) map[string]interface{} {
	input := cursorToolInput(raw)
	cursorAddPath(raw, input)
	return input
}

func cursorAddPath(raw, input map[string]interface{}) {
	if cursorFirstString(input, "file_path", "filePath", "path", "file", "target", "absolute_path", "absolutePath", "relative_path", "relativePath", "target_file", "targetFile") != "" {
		return
	}
	if path := cursorFirstString(raw, "file_path", "filePath", "path", "file", "target", "absolute_path", "absolutePath", "relative_path", "relativePath", "target_file", "targetFile"); path != "" {
		input["file_path"] = path
	}
}

func cloneCursorObject(raw map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(raw))
	for key, value := range raw {
		out[key] = value
	}
	return out
}

func cursorFirstObject(raw map[string]interface{}, keys ...string) map[string]interface{} {
	for _, key := range keys {
		if value, ok := raw[key].(map[string]interface{}); ok {
			return cloneCursorObject(value)
		}
	}
	return map[string]interface{}{}
}

func cursorFirstString(raw map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value := cursorString(raw, key); value != "" {
			return value
		}
	}
	return ""
}

func cursorString(raw map[string]interface{}, key string) string {
	value, _ := raw[key].(string)
	return strings.TrimSpace(value)
}

func cursorFirstBool(raw map[string]interface{}, keys ...string) (bool, bool) {
	for _, key := range keys {
		if value, ok := raw[key].(bool); ok {
			return value, true
		}
	}
	return false, false
}
