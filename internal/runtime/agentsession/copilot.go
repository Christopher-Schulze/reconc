package agentsession

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// NormalizeCopilotPayload accepts Copilot's PascalCase event payloads, which
// already use the VS Code-compatible snake_case shape, and normalizes the few
// result fields that differ from Reconc's internal contract.
func NormalizeCopilotPayload(event string, payloadBytes []byte) ([]byte, error) {
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return nil, fmt.Errorf("copilot payload is empty")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return nil, fmt.Errorf("copilot payload is not valid JSON: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("copilot payload must be a JSON object")
	}
	out := cloneCursorObject(raw)
	out["reconc_runtime"] = "copilot"
	out["copilot_event"] = event
	if result := cursorFirstObject(raw, "tool_response", "toolResponse", "tool_result", "toolResult"); len(result) > 0 {
		out["tool_response"] = result
	}
	if toolInput := cursorFirstObject(raw, "tool_input", "toolInput", "tool_args", "toolArgs"); len(toolInput) > 0 {
		out["tool_input"] = toolInput
	}
	if toolName := cursorFirstString(raw, "tool_name", "toolName"); toolName != "" {
		out["tool_name"] = toolName
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("copilot payload normalize: %w", err)
	}
	return body, nil
}

// AdaptCopilotResult maps Reconc's internal Claude-compatible decisions onto
// Copilot's command-hook response schema.
func AdaptCopilotResult(event string, result Result) Result {
	switch event {
	case "copilot-pre-tool-use":
		if result.ExitCode != 0 {
			return Result{Stdout: copilotJSON(map[string]interface{}{
				"permissionDecision":       "deny",
				"permissionDecisionReason": resultReason(result, "reconc denied this Copilot tool call."),
			})}
		}
		return Result{Stdout: copilotJSON(map[string]interface{}{}), Stderr: result.Stderr}
	case "copilot-permission-request":
		if result.ExitCode != 0 || copilotInternalPermissionDenied(result.Stdout) {
			return Result{Stdout: copilotJSON(map[string]interface{}{
				"behavior": "deny",
				"message":  resultReason(result, "reconc denied this Copilot permission request."),
			})}
		}
		return Result{Stdout: copilotJSON(map[string]interface{}{}), Stderr: result.Stderr}
	case "copilot-post-tool-use", "copilot-post-tool-use-failure":
		context := internalAdditionalContext(result.Stdout)
		if context == "" {
			return Result{Stdout: copilotJSON(map[string]interface{}{}), Stderr: result.Stderr}
		}
		return Result{Stdout: copilotJSON(map[string]interface{}{"additionalContext": context}), Stderr: result.Stderr}
	case "copilot-post-compaction":
		return Result{Stdout: copilotJSON(map[string]interface{}{}), Stderr: result.Stderr}
	default:
		return result
	}
}

func copilotInternalPermissionDenied(stdout string) bool {
	var raw map[string]interface{}
	if json.Unmarshal([]byte(stdout), &raw) != nil {
		return false
	}
	hookOutput, _ := raw["hookSpecificOutput"].(map[string]interface{})
	decision, _ := hookOutput["decision"].(map[string]interface{})
	behavior, _ := decision["behavior"].(string)
	return behavior == "deny"
}

func internalAdditionalContext(stdout string) string {
	var raw map[string]interface{}
	if json.Unmarshal([]byte(stdout), &raw) != nil {
		return ""
	}
	if context, _ := raw["additionalContext"].(string); strings.TrimSpace(context) != "" {
		return strings.TrimSpace(context)
	}
	hookOutput, _ := raw["hookSpecificOutput"].(map[string]interface{})
	context, _ := hookOutput["additionalContext"].(string)
	return strings.TrimSpace(context)
}

func resultReason(result Result, fallback string) string {
	for _, candidate := range []string{result.Stderr, internalPermissionReason(result.Stdout), result.Stdout, fallback} {
		if reason := strings.TrimSpace(candidate); reason != "" {
			return reason
		}
	}
	return fallback
}

func internalPermissionReason(stdout string) string {
	var raw map[string]interface{}
	if json.Unmarshal([]byte(stdout), &raw) != nil {
		return ""
	}
	hookOutput, _ := raw["hookSpecificOutput"].(map[string]interface{})
	decision, _ := hookOutput["decision"].(map[string]interface{})
	message, _ := decision["message"].(string)
	return strings.TrimSpace(message)
}

func copilotJSON(payload map[string]interface{}) string {
	body, _ := json.Marshal(payload)
	return string(body)
}
