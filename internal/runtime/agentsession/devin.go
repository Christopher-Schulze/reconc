package agentsession

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"reconc.dev/reconc/internal/pathidentity"
)

// NormalizeDevinPayload converts Devin CLI hook payloads into the internal
// payload contract. Devin does not guarantee session_id on every event, so a
// stable repo-scoped identity is derived when no explicit identity exists.
func NormalizeDevinPayload(event string, payloadBytes []byte, repoRoot string) ([]byte, error) {
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return nil, fmt.Errorf("devin payload is empty")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return nil, fmt.Errorf("devin payload is not valid JSON: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("devin payload must be a JSON object")
	}
	if err := validateDevinEvent(event, raw); err != nil {
		return nil, err
	}
	if err := validateHookPayloadCWD(cursorFirstString(raw, "cwd"), repoRoot, "Devin CLI"); err != nil {
		return nil, err
	}

	out := cloneObject(raw)
	delete(out, "reconc_mcp")
	out["session_id"] = devinSessionID(raw, repoRoot)
	out["reconc_runtime"] = "devin"
	out["devin_event"] = event
	if value, ok := cursorFirstBool(raw, "stop_hook_active", "stopHookActive", "isStopHookActive"); ok {
		out["stop_hook_active"] = value
	}
	if value, ok := cursorFirstBool(raw, "is_interrupt", "isInterrupt", "interrupted", "aborted"); ok {
		out["is_interrupt"] = value
	}

	switch event {
	case "devin-pre-tool-use", "devin-post-tool-use", "devin-permission-request":
		name := normalizeDevinToolName(cursorFirstString(raw, "tool_name", "toolName", "name"))
		if name != "" {
			out["tool_name"] = name
		}
		input := cursorFirstObject(raw, "tool_input", "toolInput", "input", "args")
		cursorAddPath(raw, input)
		if command := cursorFirstString(raw, "command", "cmd", "script"); command != "" && strings.TrimSpace(cursorString(input, "command")) == "" {
			input["command"] = command
		}
		out["tool_input"] = input
		response := cursorFirstObject(raw, "tool_response", "toolResponse", "response", "result", "output")
		for _, key := range []string{"exit_code", "exitCode", "status_code", "statusCode", "stdout", "stderr", "error", "success"} {
			if value, ok := raw[key]; ok {
				response[key] = value
			}
		}
		if len(response) > 0 {
			out["tool_response"] = response
		}
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("devin payload normalize: %w", err)
	}
	return body, nil
}

func validateDevinEvent(event string, raw map[string]interface{}) error {
	expected := map[string]string{
		"devin-session-start":      "SessionStart",
		"devin-user-prompt-submit": "UserPromptSubmit",
		"devin-pre-tool-use":       "PreToolUse",
		"devin-permission-request": "PermissionRequest",
		"devin-post-tool-use":      "PostToolUse",
		"devin-stop":               "Stop",
		"devin-session-end":        "SessionEnd",
		"devin-post-compaction":    "PostCompaction",
	}
	native, supported := expected[event]
	if !supported {
		return fmt.Errorf("unsupported Devin CLI hook route %q", event)
	}
	if cursorFirstString(raw, "hook_event_name", "hookEventName") != native {
		return fmt.Errorf("Devin CLI payload hook_event_name does not match the selected route")
	}
	return nil
}

// PayloadLooksLikeDevin detects compatible Claude hooks that Devin also
// loaded. First-class .devin hooks win so duplicate routes do not mutate state
// or run Stop twice.
func PayloadLooksLikeDevin(payloadBytes []byte, repoRoot string) bool {
	if projectDir := strings.TrimSpace(os.Getenv("DEVIN_PROJECT_DIR")); projectDir != "" {
		resolvedProject, projectErr := pathidentity.ResolveExisting(projectDir)
		resolvedRoot, rootErr := pathidentity.ResolveExisting(repoRoot)
		if projectErr == nil && rootErr == nil && resolvedProject == resolvedRoot {
			return true
		}
	}
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return false
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &raw); err != nil || raw == nil {
		return false
	}
	if _, ok := raw["devin_event"]; ok {
		return true
	}
	source := strings.ToLower(cursorFirstString(raw, "source", "runtime", "agent_runtime"))
	return strings.Contains(source, "devin")
}

func devinSessionID(raw map[string]interface{}, repoRoot string) string {
	if sessionID := cursorFirstString(raw,
		"session_id", "sessionId", "conversation_id", "conversationId",
		"generation_id", "generationId", "request_id", "requestId",
		"workspace_id", "workspaceId", "project_id", "projectId",
	); sessionID != "" {
		return sessionID
	}
	if envSession := strings.TrimSpace(os.Getenv("DEVIN_SESSION_ID")); envSession != "" {
		return envSession
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(repoRoot)))
	return "devin-" + hex.EncodeToString(sum[:])[:12]
}

func normalizeDevinToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	cleaned := strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(trimmed))
	switch cleaned {
	case "exec", "shell", "bash", "terminal":
		return "Bash"
	case "read", "grep", "glob":
		return "Read"
	case "edit", "write", "multiedit", "strreplace", "delete", "fileedit":
		return "Write"
	default:
		return trimmed
	}
}
