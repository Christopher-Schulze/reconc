package agentsession

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/pathidentity"
)

var kimiCodeNativeEvents = newNativeEventRegistry(
	nativeEventBinding{route: "kimi-session-start", primary: "SessionStart"},
	nativeEventBinding{route: "kimi-user-prompt-submit", primary: "UserPromptSubmit"},
	nativeEventBinding{route: "kimi-pre-tool-use", primary: "PreToolUse"},
	nativeEventBinding{route: "kimi-permission-request", primary: "PermissionRequest"},
	nativeEventBinding{route: "kimi-permission-result", primary: "PermissionResult"},
	nativeEventBinding{route: "kimi-post-tool-use", primary: "PostToolUse"},
	nativeEventBinding{route: "kimi-post-tool-use-failure", primary: "PostToolUseFailure"},
	nativeEventBinding{route: "kimi-stop", primary: "Stop"},
	nativeEventBinding{route: "kimi-stop-failure", primary: "StopFailure"},
	nativeEventBinding{route: "kimi-interrupt", primary: "Interrupt"},
	nativeEventBinding{route: "kimi-session-end", primary: "SessionEnd"},
	nativeEventBinding{route: "kimi-subagent-start", primary: "SubagentStart"},
	nativeEventBinding{route: "kimi-subagent-stop", primary: "SubagentStop"},
	nativeEventBinding{route: "kimi-pre-compaction", primary: "PreCompact"},
	nativeEventBinding{route: "kimi-post-compaction", primary: "PostCompact"},
	nativeEventBinding{route: "kimi-notification", primary: "Notification"},
)

var kimiCodeJSONDiagnostics = singleJSONDiagnostics{
	decodePrefix:   "decode Kimi Code payload",
	multipleValues: "multiple JSON values in Kimi Code payload",
	trailingPrefix: "trailing data in Kimi Code payload",
}

type kimiCodePayload struct {
	HookEventName  string          `json:"hook_event_name"`
	SessionID      string          `json:"session_id"`
	CWD            string          `json:"cwd"`
	Prompt         string          `json:"prompt"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolCallID     string          `json:"tool_call_id"`
	ToolOutput     *string         `json:"tool_output"`
	Error          json.RawMessage `json:"error"`
	Reason         string          `json:"reason"`
	StopHookActive *bool           `json:"stop_hook_active"`
}

type kimiCodeNormalizedPayload struct {
	SessionID      string                `json:"session_id"`
	Prompt         string                `json:"prompt,omitempty"`
	ToolName       string                `json:"tool_name,omitempty"`
	ToolInput      json.RawMessage       `json:"tool_input,omitempty"`
	ToolResponse   *kimiCodeToolResponse `json:"tool_response,omitempty"`
	ToolUseID      string                `json:"tool_use_id,omitempty"`
	Error          string                `json:"error,omitempty"`
	IsInterrupt    *bool                 `json:"is_interrupt,omitempty"`
	StopHookActive *bool                 `json:"stop_hook_active,omitempty"`
	ReconcRuntime  string                `json:"reconc_runtime"`
	KimiCodeEvent  string                `json:"kimi_code_event"`
}

type kimiCodeToolResponse struct {
	Output string `json:"output"`
}

type kimiCodeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NormalizeKimiCodePayload converts Kimi Code CLI's documented snake_case
// hook envelope into Reconc's platform-neutral session payload. The global
// hook command discovers the repository before calling this function; the
// payload cwd is then constrained to that repository identity.
func NormalizeKimiCodePayload(event string, payloadBytes []byte, repoRoot string) ([]byte, error) {
	binding, supported := kimiCodeNativeEvents.lookup(event)
	if !supported {
		return nil, fmt.Errorf("unsupported Kimi Code hook route %q", event)
	}
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return nil, errors.New("empty Kimi Code payload")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}

	var raw kimiCodePayload
	if err := decodeSingleJSONValue(payloadBytes, &raw, false, kimiCodeJSONDiagnostics); err != nil {
		return nil, err
	}
	expectedEvent := binding.primary
	if raw.HookEventName != expectedEvent {
		return nil, fmt.Errorf("hook_event_name %q in Kimi Code payload does not match route %q", raw.HookEventName, event)
	}
	if strings.TrimSpace(raw.SessionID) == "" {
		return nil, errors.New("missing non-empty session_id in Kimi Code payload")
	}
	if err := validateHookPayloadCWD(raw.CWD, repoRoot, "Kimi Code"); err != nil {
		return nil, err
	}
	if kimiCodeToolEvent(expectedEvent) && strings.TrimSpace(raw.ToolName) == "" {
		return nil, fmt.Errorf("missing tool_name in Kimi Code %s payload", expectedEvent)
	}
	if expectedEvent == "PreToolUse" {
		if err := requireKimiCodeToolInput(raw.ToolInput); err != nil {
			return nil, err
		}
	}
	errorText, err := kimiCodeErrorText(raw.Error)
	if err != nil {
		return nil, err
	}

	normalized := kimiCodeNormalizedPayload{
		SessionID:      strings.TrimSpace(raw.SessionID),
		Prompt:         strings.TrimSpace(raw.Prompt),
		ToolName:       strings.TrimSpace(raw.ToolName),
		ToolInput:      raw.ToolInput,
		ToolUseID:      strings.TrimSpace(raw.ToolCallID),
		Error:          errorText,
		StopHookActive: raw.StopHookActive,
		ReconcRuntime:  "kimi-code",
		KimiCodeEvent:  event,
	}
	if raw.ToolOutput != nil {
		normalized.ToolResponse = &kimiCodeToolResponse{Output: *raw.ToolOutput}
	}
	if expectedEvent == "Interrupt" {
		interrupted := true
		normalized.IsInterrupt = &interrupted
		if normalized.Error == "" {
			normalized.Error = strings.TrimSpace(raw.Reason)
		}
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("normalize Kimi Code payload: %w", err)
	}
	return body, nil
}

// AdaptKimiCodeResult converts Reconc's neutral Stop continuation JSON into
// Kimi Code's native exit-code-2 block contract. Kimi intentionally fails open
// on every other non-zero exit, so an intentional Reconc denial must be exact.
func AdaptKimiCodeResult(event string, result Result) Result {
	switch event {
	case "kimi-pre-tool-use", "kimi-stop", "kimi-user-prompt-submit":
	default:
		return result
	}
	if result.ExitCode != 0 {
		result.ExitCode = 2
		if strings.TrimSpace(result.Stderr) == "" {
			result.Stderr = "Reconc blocked this Kimi Code operation"
		}
		result.Stdout = ""
		return result
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return result
	}
	var decision struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &decision); err != nil ||
		decision.Decision != "block" || strings.TrimSpace(decision.Reason) == "" {
		return Result{ExitCode: 2, Stderr: "Reconc produced an invalid Kimi Code control response"}
	}
	return Result{ExitCode: 2, Stderr: strings.TrimSpace(decision.Reason)}
}

func validateHookPayloadCWD(cwd, repoRoot, platform string) error {
	if strings.TrimSpace(cwd) == "" {
		return fmt.Errorf("missing non-empty cwd in %s payload", platform)
	}
	root, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve %s repository root: %w", platform, err)
	}
	current, err := pathidentity.ResolveExisting(cwd)
	if err != nil {
		return fmt.Errorf("resolve %s cwd: %w", platform, err)
	}
	relative, err := filepath.Rel(root, current)
	if err != nil {
		return fmt.Errorf("compare %s cwd with repository root: %w", platform, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("cwd %q in %s payload is outside repository root %q", current, platform, root)
	}
	return nil
}

func kimiCodeToolEvent(event string) bool {
	switch event {
	case "PreToolUse", "PermissionRequest", "PermissionResult", "PostToolUse", "PostToolUseFailure":
		return true
	default:
		return false
	}
}

func requireKimiCodeToolInput(input json.RawMessage) error {
	if len(bytes.TrimSpace(input)) == 0 {
		return errors.New("missing tool_input in Kimi Code PreToolUse payload")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(input, &object); err != nil || object == nil {
		return errors.New("tool_input in Kimi Code PreToolUse payload must be a JSON object")
	}
	return nil
}

func kimiCodeErrorText(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var message string
	if err := json.Unmarshal(raw, &message); err == nil {
		return strings.TrimSpace(message), nil
	}
	var payload kimiCodeError
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", errors.New("decode Kimi Code error: expected a string or object")
	}
	if message := strings.TrimSpace(payload.Message); message != "" {
		return message, nil
	}
	if code := strings.TrimSpace(payload.Code); code != "" {
		return code, nil
	}
	return "", errors.New("invalid Kimi Code error object: missing code or message")
}
