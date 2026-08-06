package agentsession

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

var zcodeNativeEvents = map[string]string{
	"zcode-session-start":         "SessionStart",
	"zcode-user-prompt-submit":    "UserPromptSubmit",
	"zcode-pre-tool-use":          "PreToolUse",
	"zcode-permission-request":    "PermissionRequest",
	"zcode-post-tool-use":         "PostToolUse",
	"zcode-post-tool-use-failure": "PostToolUseFailure",
	"zcode-stop":                  "Stop",
}

type zcodePayload struct {
	HookEventName  string          `json:"hook_event_name"`
	SessionID      string          `json:"session_id"`
	CWD            string          `json:"cwd"`
	Prompt         string          `json:"prompt"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	ToolUseID      string          `json:"tool_use_id"`
	Error          string          `json:"error"`
	IsInterrupt    *bool           `json:"is_interrupt"`
	StopHookActive *bool           `json:"stop_hook_active"`
}

type zcodeNormalizedPayload struct {
	SessionID      string            `json:"session_id"`
	Prompt         string            `json:"prompt,omitempty"`
	ToolName       string            `json:"tool_name,omitempty"`
	ToolInput      json.RawMessage   `json:"tool_input,omitempty"`
	ToolResponse   json.RawMessage   `json:"tool_response,omitempty"`
	ToolUseID      string            `json:"tool_use_id,omitempty"`
	Error          string            `json:"error,omitempty"`
	IsInterrupt    *bool             `json:"is_interrupt,omitempty"`
	StopHookActive *bool             `json:"stop_hook_active,omitempty"`
	ReconcRuntime  string            `json:"reconc_runtime"`
	ZCodeEvent     string            `json:"zcode_event"`
	MCP            *zcodeMCPEnvelope `json:"reconc_mcp,omitempty"`
}

type zcodeMCPEnvelope struct {
	Platform        policy.MCPPlatform `json:"platform"`
	Tool            string             `json:"tool"`
	Observed        bool               `json:"observed"`
	BlockingPreHook bool               `json:"blocking_pre_hook"`
	InputValid      bool               `json:"input_valid"`
	Outcome         string             `json:"outcome,omitempty"`
}

// NormalizeZCodePayload validates ZCode's documented snake_case subprocess
// envelope and converts it into Reconc's platform-neutral session payload.
func NormalizeZCodePayload(event string, payloadBytes []byte, repoRoot string) ([]byte, error) {
	expectedEvent := zcodeNativeEvents[event]
	if expectedEvent == "" {
		return nil, fmt.Errorf("unsupported ZCode hook route %q", event)
	}
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return nil, errors.New("empty ZCode payload")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}
	var raw zcodePayload
	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode ZCode payload: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values in ZCode payload")
		}
		return nil, fmt.Errorf("trailing data in ZCode payload: %w", err)
	}
	if raw.HookEventName != expectedEvent {
		return nil, fmt.Errorf("hook_event_name %q in ZCode payload does not match route %q", raw.HookEventName, event)
	}
	if strings.TrimSpace(raw.SessionID) == "" {
		return nil, errors.New("missing non-empty session_id in ZCode payload")
	}
	if err := validateHookPayloadCWD(raw.CWD, repoRoot, "ZCode"); err != nil {
		return nil, err
	}
	if err := validateZCodeEventPayload(event, raw); err != nil {
		return nil, err
	}
	normalized := zcodeNormalizedPayload{
		SessionID:      strings.TrimSpace(raw.SessionID),
		Prompt:         strings.TrimSpace(raw.Prompt),
		ToolName:       strings.TrimSpace(raw.ToolName),
		ToolInput:      raw.ToolInput,
		ToolResponse:   raw.ToolResponse,
		ToolUseID:      strings.TrimSpace(raw.ToolUseID),
		Error:          strings.TrimSpace(raw.Error),
		IsInterrupt:    raw.IsInterrupt,
		StopHookActive: raw.StopHookActive,
		ReconcRuntime:  "zcode",
		ZCodeEvent:     event,
	}
	if zcodeToolEvent(event) {
		outcome := ""
		if event == "zcode-post-tool-use" {
			outcome = "success"
		} else if event == "zcode-post-tool-use-failure" {
			outcome = "failure"
		}
		normalized.MCP = &zcodeMCPEnvelope{
			Platform: policy.MCPPlatformZCode, Tool: strings.TrimSpace(raw.ToolName),
			Observed: false, BlockingPreHook: true, InputValid: jsonObject(raw.ToolInput), Outcome: outcome,
		}
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("normalize ZCode payload: %w", err)
	}
	return body, nil
}

func validateZCodeEventPayload(event string, raw zcodePayload) error {
	if zcodeToolEvent(event) || event == "zcode-permission-request" {
		if strings.TrimSpace(raw.ToolName) == "" {
			return fmt.Errorf("missing tool_name in ZCode %s payload", raw.HookEventName)
		}
		if !jsonObject(raw.ToolInput) {
			return fmt.Errorf("tool_input in ZCode %s payload must be a JSON object", raw.HookEventName)
		}
	}
	if zcodeToolEvent(event) && strings.TrimSpace(raw.ToolUseID) == "" {
		return fmt.Errorf("missing tool_use_id in ZCode %s payload", raw.HookEventName)
	}
	switch event {
	case "zcode-post-tool-use":
		if !jsonObject(raw.ToolResponse) {
			return errors.New("tool_response in ZCode PostToolUse payload must be a JSON object")
		}
	case "zcode-post-tool-use-failure":
		if strings.TrimSpace(raw.Error) == "" {
			return errors.New("missing error in ZCode PostToolUseFailure payload")
		}
		if raw.IsInterrupt == nil {
			return errors.New("missing is_interrupt in ZCode PostToolUseFailure payload")
		}
	case "zcode-stop":
		if raw.StopHookActive == nil {
			return errors.New("missing stop_hook_active in ZCode Stop payload")
		}
	}
	return nil
}

func zcodeToolEvent(event string) bool {
	switch event {
	case "zcode-pre-tool-use", "zcode-post-tool-use", "zcode-post-tool-use-failure":
		return true
	default:
		return false
	}
}
