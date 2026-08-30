package agentsession

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"reconc.dev/reconc/internal/pathidentity"
)

// NormalizeGitHubCopilotPayload converts GitHub Copilot's documented hook
// envelopes into Reconc's platform-neutral session payload. Reconc generates
// PascalCase compatibility events wherever GitHub documents them, while the
// subagentStart event remains camelCase because GitHub exposes no compatible
// PascalCase payload for that event.
func NormalizeGitHubCopilotPayload(event string, payloadBytes []byte, repoRoot string) ([]byte, error) {
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return nil, fmt.Errorf("GitHub Copilot payload is empty")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("GitHub Copilot payload is not valid JSON: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("GitHub Copilot payload contains multiple JSON values")
		}
		return nil, fmt.Errorf("GitHub Copilot payload has trailing data: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("GitHub Copilot payload must be a JSON object")
	}
	if err := validateGitHubCopilotEvent(event, raw); err != nil {
		return nil, err
	}
	if err := validateGitHubCopilotWorkspace(raw, repoRoot); err != nil {
		return nil, err
	}

	sessionID := cursorFirstString(raw, "session_id", "sessionId")
	if sessionID == "" {
		return nil, fmt.Errorf("GitHub Copilot payload must include a non-empty session_id")
	}
	out := cloneObject(raw)
	out["session_id"] = sessionID
	out["reconc_runtime"] = "github-copilot"
	out["copilot_event"] = event
	if prompt := cursorFirstString(raw, "prompt", "initial_prompt", "initialPrompt"); prompt != "" {
		out["prompt"] = prompt
	}
	if toolName := normalizeGitHubCopilotToolName(cursorFirstString(raw, "tool_name", "toolName")); toolName != "" {
		out["tool_name"] = toolName
	}
	toolInput, hasToolInput := firstGitHubCopilotValue(raw, "tool_input", "toolArgs")
	if hasToolInput {
		if event == "copilot-pre-tool-use" || event == "copilot-permission-request" {
			normalized, err := githubCopilotGuardedToolInput(cursorFirstString(out, "tool_name"), toolInput)
			if err != nil {
				return nil, fmt.Errorf("GitHub Copilot %s: %w", event, err)
			}
			out["tool_input"] = normalized
		} else {
			out["tool_input"] = githubCopilotObject(toolInput)
		}
	}
	if result, ok := firstGitHubCopilotValue(raw, "tool_result", "toolResult"); ok {
		out["tool_response"] = githubCopilotObject(result)
	}
	if errText := githubCopilotError(raw["error"]); errText != "" {
		out["error"] = errText
	}
	if value, present := firstGitHubCopilotValue(raw, "stop_hook_active", "stopHookActive"); present {
		active, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("GitHub Copilot stop_hook_active must be a JSON boolean")
		}
		out["stop_hook_active"] = active
	}

	if event == "copilot-pre-tool-use" || event == "copilot-permission-request" {
		if cursorFirstString(out, "tool_name") == "" {
			return nil, fmt.Errorf("GitHub Copilot %s payload must include tool_name", event)
		}
		if _, ok := out["tool_input"].(map[string]interface{}); !ok {
			return nil, fmt.Errorf("GitHub Copilot %s tool_input must be a JSON object", event)
		}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("GitHub Copilot payload normalize: %w", err)
	}
	return body, nil
}

// AdaptGitHubCopilotResult translates Reconc's neutral control responses into
// the exact GitHub Copilot command-hook output contracts. Empty success output
// deliberately leaves Copilot's normal permission flow intact.
func AdaptGitHubCopilotResult(event string, result Result) Result {
	switch event {
	case "copilot-pre-tool-use":
		if result.ExitCode == 0 && result.Err == nil {
			result.Stdout = ""
			return result
		}
		return resultWithHookJSON(Result{ExitCode: 0, Err: result.Err}, map[string]string{
			"permissionDecision":       "deny",
			"permissionDecisionReason": resultReason(result, "Reconc runtime returned no diagnostic"),
		})
	case "copilot-permission-request":
		return adaptGitHubCopilotPermissionResult(result)
	case "copilot-post-tool-use-failure":
		return adaptGitHubCopilotPostFailureResult(result)
	case "copilot-stop", "copilot-subagent-stop":
		return adaptGitHubCopilotStopResult(result)
	default:
		return result
	}
}

func validateGitHubCopilotEvent(event string, raw map[string]interface{}) error {
	expected := map[string][]string{
		"copilot-session-start":         {"SessionStart"},
		"copilot-user-prompt-submit":    {"UserPromptSubmit"},
		"copilot-pre-tool-use":          {"PreToolUse"},
		"copilot-permission-request":    {"PermissionRequest"},
		"copilot-post-tool-use":         {"PostToolUse"},
		"copilot-post-tool-use-failure": {"PostToolUseFailure"},
		"copilot-stop":                  {"Stop"},
		"copilot-session-end":           {"SessionEnd"},
		"copilot-notification":          {"Notification"},
		"copilot-subagent-start":        {"subagentStart", "SubagentStart"},
		"copilot-subagent-stop":         {"SubagentStop"},
		"copilot-pre-compaction":        {"PreCompact"},
	}[event]
	if len(expected) == 0 {
		return fmt.Errorf("unsupported GitHub Copilot hook route %q", event)
	}
	native := cursorFirstString(raw, "hook_event_name", "hookEventName")
	if native == "" && event == "copilot-subagent-start" {
		return nil
	}
	if native == "" {
		return fmt.Errorf("GitHub Copilot payload must include hook_event_name")
	}
	for _, candidate := range expected {
		if native == candidate {
			return nil
		}
	}
	return fmt.Errorf("GitHub Copilot payload hook_event_name %q does not match route %q", native, event)
}

func validateGitHubCopilotWorkspace(raw map[string]interface{}, repoRoot string) error {
	root, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve GitHub Copilot repository root: %w", err)
	}
	cwd := cursorFirstString(raw, "cwd")
	if cwd == "" {
		return fmt.Errorf("GitHub Copilot payload must include cwd")
	}
	resolved, err := pathidentity.ResolveExisting(cwd)
	if err != nil {
		return fmt.Errorf("resolve GitHub Copilot cwd: %w", err)
	}
	if resolved == root {
		return nil
	}
	rootInfo, rootErr := os.Stat(root)
	cwdInfo, cwdErr := os.Stat(resolved)
	if rootErr == nil && cwdErr == nil && os.SameFile(rootInfo, cwdInfo) {
		return nil
	}
	return fmt.Errorf("GitHub Copilot cwd %q does not match repository root %q", cwd, root)
}

func normalizeGitHubCopilotToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	switch strings.ToLower(trimmed) {
	case "bash", "powershell":
		return "Bash"
	case "view":
		return "Read"
	case "create":
		return "Write"
	case "edit", "str_replace_editor", "apply_patch":
		return "Edit"
	default:
		return trimmed
	}
}

func firstGitHubCopilotValue(raw map[string]interface{}, keys ...string) (interface{}, bool) {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func githubCopilotObject(value interface{}) map[string]interface{} {
	if object, ok := value.(map[string]interface{}); ok {
		return cloneObject(object)
	}
	return map[string]interface{}{"value": value}
}

func githubCopilotGuardedToolInput(toolName string, value interface{}) (map[string]interface{}, error) {
	if object, ok := value.(map[string]interface{}); ok {
		return cloneObject(object), nil
	}
	if encoded, ok := value.(string); ok {
		var object map[string]interface{}
		if json.Unmarshal([]byte(encoded), &object) == nil && object != nil {
			return cloneObject(object), nil
		}
		if toolName == "Bash" && strings.TrimSpace(encoded) != "" {
			return map[string]interface{}{"command": encoded}, nil
		}
	}
	return nil, fmt.Errorf("guarded %s tool_input cannot be evaluated safely", toolName)
}

func githubCopilotError(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]interface{}:
		return cursorFirstString(typed, "message", "name")
	default:
		return ""
	}
}

func adaptGitHubCopilotPermissionResult(result Result) Result {
	if result.ExitCode != 0 || result.Err != nil {
		return resultWithHookJSON(Result{ExitCode: 0, Err: result.Err}, map[string]string{"behavior": "deny", "message": resultReason(result, "Reconc runtime returned no diagnostic")})
	}
	var envelope map[string]interface{}
	if strings.TrimSpace(result.Stdout) == "" || json.Unmarshal([]byte(result.Stdout), &envelope) != nil {
		result.Stdout = ""
		return result
	}
	hookOutput, _ := envelope["hookSpecificOutput"].(map[string]interface{})
	decision, _ := hookOutput["decision"].(map[string]interface{})
	if strings.TrimSpace(cursorFirstString(decision, "behavior")) != "deny" {
		result.Stdout = ""
		return result
	}
	message := cursorFirstString(decision, "message")
	if message == "" {
		message = "Reconc denied this GitHub Copilot permission request."
	}
	return resultWithHookJSON(Result{ExitCode: 0, Stderr: result.Stderr, Err: result.Err}, map[string]string{"behavior": "deny", "message": message})
}

func adaptGitHubCopilotPostFailureResult(result Result) Result {
	var envelope map[string]interface{}
	if strings.TrimSpace(result.Stdout) == "" || json.Unmarshal([]byte(result.Stdout), &envelope) != nil {
		result.ExitCode = 0
		result.Stdout = ""
		return result
	}
	hookOutput, _ := envelope["hookSpecificOutput"].(map[string]interface{})
	context := cursorFirstString(hookOutput, "additionalContext")
	if context == "" {
		result.ExitCode = 0
		result.Stdout = ""
		return result
	}
	return resultWithHookJSON(Result{ExitCode: 0, Stderr: result.Stderr, Err: result.Err}, map[string]string{"additionalContext": context})
}

func adaptGitHubCopilotStopResult(result Result) Result {
	if result.ExitCode != 0 || result.Err != nil {
		return resultWithHookJSON(Result{ExitCode: 0, Err: result.Err}, map[string]string{
			"decision": "block",
			"reason":   "Reconc could not evaluate this GitHub Copilot stop: " + resultReason(result, "Reconc runtime returned no diagnostic"),
		})
	}
	stdout := strings.TrimSpace(result.Stdout)
	if stdout == "" {
		return Result{ExitCode: 0, Stderr: result.Stderr}
	}
	var decision struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if json.Unmarshal([]byte(stdout), &decision) != nil || decision.Decision != "block" || strings.TrimSpace(decision.Reason) == "" {
		return gitHubCopilotStopBlockResult("Reconc produced an invalid non-empty GitHub Copilot stop decision")
	}
	return resultWithHookJSON(Result{ExitCode: 0, Stderr: result.Stderr, Err: result.Err}, map[string]string{"decision": "block", "reason": strings.TrimSpace(decision.Reason)})
}

func gitHubCopilotStopBlockResult(reason string) Result {
	return resultWithHookJSON(Result{ExitCode: 0}, map[string]string{"decision": "block", "reason": strings.TrimSpace(reason)})
}
