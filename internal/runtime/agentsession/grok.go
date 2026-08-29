package agentsession

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"reconc.dev/reconc/internal/pathidentity"
)

// NormalizeGrokPayload converts Grok Build's camelCase hook envelope into the
// internal session payload contract. The runner-provided identity variables
// are cross-checked when present so a hand-crafted envelope cannot silently
// claim another Grok session or workspace.
func NormalizeGrokPayload(event string, payloadBytes []byte, repoRoot string) ([]byte, error) {
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return nil, fmt.Errorf("grok payload is empty")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return nil, fmt.Errorf("grok payload is not valid JSON: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("grok payload must be a JSON object")
	}
	if err := validateGrokEvent(event, raw); err != nil {
		return nil, err
	}

	sessionID := cursorFirstString(raw, "sessionId", "session_id")
	envSessionID := strings.TrimSpace(os.Getenv("GROK_SESSION_ID"))
	if sessionID == "" {
		sessionID = envSessionID
	}
	if sessionID == "" {
		return nil, fmt.Errorf("grok payload must include a non-empty sessionId")
	}
	if envSessionID != "" && sessionID != envSessionID {
		return nil, fmt.Errorf("grok payload sessionId %q does not match GROK_SESSION_ID %q", sessionID, envSessionID)
	}
	if err := validateGrokWorkspace(raw, repoRoot); err != nil {
		return nil, err
	}
	if event == "grok-pre-tool-use" {
		if truncatedValue, exists := raw["toolInputTruncated"]; exists {
			truncated, ok := truncatedValue.(bool)
			if !ok {
				return nil, fmt.Errorf("grok PreToolUse toolInputTruncated must be a boolean")
			}
			if truncated {
				return nil, fmt.Errorf("grok PreToolUse toolInput is truncated; refusing to evaluate incomplete policy input")
			}
		}
		toolName := strings.ToLower(strings.TrimSpace(cursorFirstString(raw, "toolName", "tool_name")))
		if !isGuardedGrokTool(toolName) {
			return nil, fmt.Errorf("grok PreToolUse toolName %q is not a supported guarded tool", toolName)
		}
		input, exists := raw["toolInput"]
		if !exists {
			input, exists = raw["tool_input"]
		}
		if _, ok := input.(map[string]interface{}); !exists || !ok {
			return nil, fmt.Errorf("grok PreToolUse toolInput must be a JSON object")
		}
	}
	if event == "grok-stop" {
		if activeValue, exists := raw["stopHookActive"]; exists {
			if _, ok := activeValue.(bool); !ok {
				return nil, fmt.Errorf("grok Stop stopHookActive must be a boolean")
			}
		}
	}

	out := cloneCursorObject(raw)
	out["session_id"] = sessionID
	out["reconc_runtime"] = "grok"
	out["grok_event"] = event
	if prompt := cursorFirstString(raw, "prompt"); prompt != "" {
		out["prompt"] = prompt
	}
	if toolUseID := cursorFirstString(raw, "toolUseId", "tool_use_id"); toolUseID != "" {
		out["tool_use_id"] = toolUseID
	}
	if toolName := normalizeGrokToolName(cursorFirstString(raw, "toolName", "tool_name")); toolName != "" {
		out["tool_name"] = toolName
	}
	if input, ok := raw["toolInput"]; ok {
		out["tool_input"] = grokObject(input)
	} else if input, ok := raw["tool_input"]; ok {
		out["tool_input"] = grokObject(input)
	}
	if result, ok := raw["toolResult"]; ok {
		out["tool_response"] = grokObject(result)
	} else if result, ok := raw["tool_result"]; ok {
		out["tool_response"] = grokObject(result)
	}
	if errText := cursorFirstString(raw, "error"); errText != "" {
		out["error"] = errText
	}
	if event == "grok-stop" {
		if active, ok := raw["stopHookActive"].(bool); ok {
			out["stop_hook_active"] = active
		}
		if grokStopIsInterrupt(cursorFirstString(raw, "reason")) {
			out["is_interrupt"] = true
		}
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("grok payload normalize: %w", err)
	}
	return body, nil
}

// PayloadLooksLikeGrok identifies Grok's native camelCase envelope. Grok also
// reads compatible Claude/Cursor hook files; this signal lets the CLI suppress
// those duplicate routes only when the first-class .grok hook is installed.
func PayloadLooksLikeGrok(payloadBytes []byte) bool {
	if len(bytes.TrimSpace(payloadBytes)) == 0 ||
		!bytes.Contains(payloadBytes, []byte(`"hookEventName"`)) ||
		!bytes.Contains(payloadBytes, []byte(`"workspaceRoot"`)) {
		return false
	}
	var raw map[string]interface{}
	if json.Unmarshal(payloadBytes, &raw) != nil {
		return false
	}
	return cursorFirstString(raw, "hookEventName") != "" &&
		cursorFirstString(raw, "workspaceRoot") != ""
}

// AdaptGrokResult emits Grok's exact blocking wire contract. Denials use exit
// zero deliberately: the repo-local wrapper reserves non-zero for a broken or
// obsolete Reconc binary and can therefore translate every infrastructure
// failure into its own explicit deny instead of falling into Grok's fail-open
// error path.
func AdaptGrokResult(event string, result Result) Result {
	if event == "grok-pre-tool-use" {
		if result.ExitCode != 0 || result.Err != nil {
			reason := grokResultReason(result)
			return resultWithHookJSON(Result{ExitCode: 0, Err: result.Err}, map[string]string{"decision": "deny", "reason": reason})
		}
		return resultWithHookJSON(Result{ExitCode: 0, Stderr: result.Stderr, Err: result.Err}, map[string]string{"decision": "allow"})
	}

	if event == "grok-stop" {
		if result.ExitCode != 0 || result.Err != nil {
			return resultWithHookJSON(Result{ExitCode: 0, Err: result.Err}, map[string]string{
				"decision": "block",
				"reason":   "Reconc could not evaluate this Grok Stop: " + grokResultReason(result),
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
		if err := json.Unmarshal([]byte(stdout), &decision); err != nil ||
			decision.Decision != "block" || strings.TrimSpace(decision.Reason) == "" {
			return grokStopBlockResult("Reconc produced an invalid non-empty Grok Stop decision")
		}
		return resultWithHookJSON(Result{ExitCode: 0, Stderr: result.Stderr, Err: result.Err}, map[string]string{
			"decision": "block",
			"reason":   strings.TrimSpace(decision.Reason),
		})
	}
	result.ExitCode = 0
	result.Stdout = ""
	return result
}

func grokStopBlockResult(reason string) Result {
	return resultWithHookJSON(Result{ExitCode: 0}, map[string]string{"decision": "block", "reason": strings.TrimSpace(reason)})
}

func grokResultReason(result Result) string {
	for _, candidate := range []string{result.Stderr, result.Stdout} {
		if reason := strings.TrimSpace(candidate); reason != "" {
			return reason
		}
	}
	return "Reconc runtime returned no diagnostic"
}

func validateGrokEvent(event string, raw map[string]interface{}) error {
	native := cursorFirstString(raw, "hookEventName", "hook_event_name")
	if native == "" {
		return fmt.Errorf("grok payload must include hookEventName")
	}
	expected := map[string]string{
		"grok-session-start":         "session_start",
		"grok-user-prompt-submit":    "user_prompt_submit",
		"grok-pre-tool-use":          "pre_tool_use",
		"grok-post-tool-use":         "post_tool_use",
		"grok-post-tool-use-failure": "post_tool_use_failure",
		"grok-permission-denied":     "permission_denied",
		"grok-stop":                  "stop",
		"grok-stop-failure":          "stop_failure",
		"grok-notification":          "notification",
		"grok-subagent-start":        "subagent_start",
		"grok-subagent-stop":         "subagent_stop",
		"grok-pre-compaction":        "pre_compact",
		"grok-post-compaction":       "post_compact",
		"grok-session-end":           "session_end",
	}[event]
	if expected == "" {
		return fmt.Errorf("unsupported Grok hook route %q", event)
	}
	if native == expected {
		return nil
	}
	return fmt.Errorf("grok payload hookEventName %q does not match route %q", native, event)
}

func validateGrokWorkspace(raw map[string]interface{}, repoRoot string) error {
	root, err := canonicalGrokPath(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve Grok repository root: %w", err)
	}
	envelopeRoot := cursorFirstString(raw, "workspaceRoot", "cwd")
	if envelopeRoot == "" {
		return fmt.Errorf("grok payload must include workspaceRoot")
	}
	envRoot := strings.TrimSpace(os.Getenv("GROK_WORKSPACE_ROOT"))
	candidates := []struct {
		name string
		path string
	}{
		{name: "workspaceRoot", path: envelopeRoot},
		{name: "GROK_WORKSPACE_ROOT", path: envRoot},
	}
	for _, candidate := range candidates {
		if candidate.path == "" {
			continue
		}
		canonical, err := canonicalGrokPath(candidate.path)
		if err != nil {
			return fmt.Errorf("resolve Grok %s: %w", candidate.name, err)
		}
		if !sameGrokPath(canonical, root) {
			return fmt.Errorf("grok %s %q does not match repository root %q", candidate.name, candidate.path, root)
		}
	}
	return nil
}

func sameGrokPath(left, right string) bool {
	if left == right {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func canonicalGrokPath(path string) (string, error) {
	return pathidentity.ResolveExisting(path)
}

func grokObject(value interface{}) map[string]interface{} {
	if object, ok := value.(map[string]interface{}); ok {
		return cloneCursorObject(object)
	}
	return map[string]interface{}{"value": value}
}

func normalizeGrokToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	switch strings.ToLower(trimmed) {
	case "read_file", "hashline_read", "grep", "hashline_grep", "list_dir":
		return "Read"
	case "write":
		return "Write"
	case "search_replace", "hashline_edit":
		return "Edit"
	case "run_terminal_command", "run_terminal_cmd":
		return "Bash"
	default:
		return trimmed
	}
}

func isGuardedGrokTool(name string) bool {
	switch name {
	case "write", "search_replace", "hashline_edit", "run_terminal_command", "run_terminal_cmd":
		return true
	default:
		return false
	}
}

func grokStopIsInterrupt(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "cancelled", "canceled", "interrupt", "interrupted", "aborted", "user_abort", "channel_closed", "shutdown":
		return true
	default:
		return false
	}
}
