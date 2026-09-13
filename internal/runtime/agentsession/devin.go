package agentsession

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"strings"

	"reconc.dev/reconc/internal/pathidentity"
)

var devinNativeEvents = newNativeEventRegistry(
	nativeEventBinding{route: "devin-session-start", primary: "SessionStart"},
	nativeEventBinding{route: "devin-user-prompt-submit", primary: "UserPromptSubmit"},
	nativeEventBinding{route: "devin-pre-tool-use", primary: "PreToolUse"},
	nativeEventBinding{route: "devin-permission-request", primary: "PermissionRequest"},
	nativeEventBinding{route: "devin-post-tool-use", primary: "PostToolUse"},
	nativeEventBinding{route: "devin-stop", primary: "Stop"},
	nativeEventBinding{route: "devin-session-end", primary: "SessionEnd"},
	nativeEventBinding{route: "devin-post-compaction", primary: "PostCompaction"},
)

// ErrDevinUnsupportedTool lets the CLI expose a fixed, non-sensitive block
// reason without reflecting untrusted tool arguments or names into diagnostics.
var ErrDevinUnsupportedTool = errors.New("unsupported Devin tool is blocked")

// NormalizeDevinPayload converts Devin CLI hook payloads into the internal
// payload contract. The native session_id separates concurrent sessions in
// one repository; prompt_id binds a tool call to its originating turn.
func NormalizeDevinPayload(event string, payloadBytes []byte, repoRoot string) ([]byte, error) {
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return nil, fmt.Errorf("devin payload is empty")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := jsonv2.Unmarshal(payloadBytes, &raw); err != nil {
		return nil, fmt.Errorf("devin payload is not valid JSON: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("devin payload must be a JSON object")
	}
	if err := validateDevinEvent(event, raw); err != nil {
		return nil, err
	}
	if err := validateDevinProjectBinding(raw, repoRoot); err != nil {
		return nil, err
	}
	sessionID, ok := raw["session_id"].(string)
	if !ok {
		return nil, fmt.Errorf("devin payload requires native session_id")
	}
	if err := validateSessionID(sessionID); err != nil {
		return nil, fmt.Errorf("invalid Devin session_id: %w", err)
	}
	promptID, hasPrompt := raw["prompt_id"].(string)
	if value, present := raw["prompt_id"]; present {
		if !hasPrompt {
			return nil, fmt.Errorf("invalid Devin prompt_id type %T", value)
		}
		if err := validateSessionID(promptID); err != nil {
			return nil, fmt.Errorf("invalid Devin prompt_id: %w", err)
		}
	}

	out := cloneObject(raw)
	delete(out, "reconc_mcp")
	out["session_id"] = sessionID
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
		delete(out, "error")
		delete(out, "exit_code")
		delete(out, "exitCode")
		delete(out, "status_code")
		delete(out, "statusCode")
		if !hasPrompt {
			return nil, fmt.Errorf("devin tool event has no native prompt_id; refusing unbound tool evidence")
		}
		callID, ok := raw["tool_use_id"].(string)
		if !ok {
			return nil, fmt.Errorf("devin tool event requires native tool_use_id")
		}
		if err := validateSessionID(callID); err != nil {
			return nil, fmt.Errorf("invalid Devin tool_use_id: %w", err)
		}
		sum := sha256.Sum256([]byte(promptID + "\x00" + callID))
		out["tool_use_id"] = "devin-" + hex.EncodeToString(sum[:])
		rawName, ok := raw["tool_name"].(string)
		if !ok || rawName == "" || strings.TrimSpace(rawName) != rawName {
			return nil, fmt.Errorf("devin tool event requires an exact native tool_name")
		}
		if event != "devin-post-tool-use" {
			if err := validateDevinPreTool(rawName); err != nil {
				return nil, err
			}
		}
		input, valid := raw["tool_input"].(map[string]interface{})
		if !valid {
			return nil, fmt.Errorf("devin tool event requires native tool_input object")
		}
		out["tool_name"] = normalizeDevinToolName(rawName)
		if event != "devin-post-tool-use" && rawName == "exec" && strings.TrimSpace(cursorString(input, "command")) == "" {
			return nil, fmt.Errorf("devin exec has no native command and is blocked")
		}
		out["tool_input"] = input
		response := cursorFirstObject(raw, "tool_response")
		if value, ok := raw["stderr"]; ok {
			if _, present := response["stderr"]; !present {
				response["stderr"] = value
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

func validateDevinPreTool(name string) error {
	switch name {
	case "exec", "read", "write", "edit", "apply_patch", "notebook_edit", "grep", "glob", "get_output", "kill_shell":
		return nil
	case "write_to_process":
		return fmt.Errorf("%w: write_to_process cannot be bound to the original exec", ErrDevinUnsupportedTool)
	case "mcp_call_tool":
		return fmt.Errorf("%w: mcp_call_tool has no verified selector envelope", ErrDevinUnsupportedTool)
	default:
		if strings.HasPrefix(name, namespacedMCPPrefix) {
			return fmt.Errorf("%w: namespaced MCP has no verified host envelope", ErrDevinUnsupportedTool)
		}
		return fmt.Errorf("%w: unclassified tool name", ErrDevinUnsupportedTool)
	}
}

func validateDevinEvent(event string, raw map[string]interface{}) error {
	binding, supported := devinNativeEvents.lookup(event)
	if !supported {
		return fmt.Errorf("unsupported Devin CLI hook route %q", event)
	}
	if cursorFirstString(raw, "hook_event_name", "hookEventName") != binding.primary {
		return fmt.Errorf("devin CLI payload hook_event_name does not match the selected route")
	}
	return nil
}

func validateDevinProjectBinding(raw map[string]interface{}, repoRoot string) error {
	projectDir := os.Getenv("DEVIN_PROJECT_DIR")
	if projectDir != "" {
		project, projectErr := pathidentity.ResolveExisting(projectDir)
		root, rootErr := pathidentity.ResolveExisting(repoRoot)
		if projectErr != nil || rootErr != nil || project != root {
			return fmt.Errorf("devin CLI project directory does not match the resolved repository")
		}
	}
	if value, present := raw["cwd"]; present {
		cwd, ok := value.(string)
		if !ok {
			return fmt.Errorf("devin CLI payload cwd must be a string")
		}
		return validateHookPayloadCWD(cwd, repoRoot, "Devin CLI")
	}
	if projectDir == "" {
		return fmt.Errorf("devin CLI payload has no cwd and DEVIN_PROJECT_DIR is unavailable")
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
	case "notebookedit":
		return "NotebookEdit"
	case "applypatch":
		return "apply_patch"
	default:
		return trimmed
	}
}
