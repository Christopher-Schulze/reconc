package agentsession

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

var piNativeEvents = newNativeEventRegistry(
	nativeEventBinding{route: "pi-session-start", primary: "session_start"},
	nativeEventBinding{route: "pi-user-prompt-submit", primary: "input"},
	nativeEventBinding{route: "pi-pre-tool-use", primary: "tool_call"},
	nativeEventBinding{route: "pi-user-bash", primary: "user_bash"},
	nativeEventBinding{route: "pi-post-tool-use", primary: "tool_result"},
	nativeEventBinding{route: "pi-post-tool-use-failure", primary: "tool_result"},
	nativeEventBinding{route: "pi-stop", primary: "agent_settled"},
	nativeEventBinding{route: "pi-continuation-requested", primary: "agent_settled"},
	nativeEventBinding{route: "pi-continuation-failed", primary: "agent_settled"},
	nativeEventBinding{route: "pi-continuation-suppressed", primary: "agent_settled"},
	nativeEventBinding{route: "pi-session-end", primary: "session_shutdown"},
	nativeEventBinding{route: "pi-pre-compaction", primary: "session_before_compact"},
	nativeEventBinding{route: "pi-post-compaction", primary: "session_compact"},
)

var piJSONDiagnostics = singleJSONDiagnostics{
	decodePrefix:   "decode Pi payload",
	multipleValues: "multiple JSON values in Pi payload",
	trailingPrefix: "trailing data in Pi payload",
}

type piPayload struct {
	HookEventName      string          `json:"hook_event_name"`
	SessionID          string          `json:"session_id"`
	SessionFile        string          `json:"session_file"`
	CWD                string          `json:"cwd"`
	Prompt             string          `json:"prompt"`
	ToolName           string          `json:"tool_name"`
	ToolInput          json.RawMessage `json:"tool_input"`
	ToolResponse       json.RawMessage `json:"tool_response"`
	ToolCallID         string          `json:"tool_call_id"`
	Error              string          `json:"error"`
	IsError            *bool           `json:"is_error"`
	StopHookActive     *bool           `json:"stop_hook_active"`
	InputSource        string          `json:"input_source"`
	StreamingBehavior  string          `json:"streaming_behavior"`
	Reason             string          `json:"reason"`
	PreviousSession    string          `json:"previous_session_file"`
	TargetSession      string          `json:"target_session_file"`
	WillRetry          *bool           `json:"will_retry"`
	FromExtension      *bool           `json:"from_extension"`
	UserBashCWD        string          `json:"user_bash_cwd"`
	ExcludeFromContext *bool           `json:"exclude_from_context"`
	Continuation       string          `json:"continuation_delivery"`
}

type piNormalizedPayload struct {
	SessionID          string                 `json:"session_id"`
	SessionFile        string                 `json:"session_file,omitempty"`
	Prompt             string                 `json:"prompt,omitempty"`
	ToolName           string                 `json:"tool_name,omitempty"`
	ToolInput          json.RawMessage        `json:"tool_input,omitempty"`
	ToolResponse       json.RawMessage        `json:"tool_response,omitempty"`
	ToolUseID          string                 `json:"tool_use_id,omitempty"`
	Error              string                 `json:"error,omitempty"`
	StopHookActive     *bool                  `json:"stop_hook_active,omitempty"`
	StrictContinuation bool                   `json:"strict_continuation,omitempty"`
	ReconcRuntime      string                 `json:"reconc_runtime"`
	PiEvent            string                 `json:"pi_event"`
	PiInputSource      string                 `json:"pi_input_source,omitempty"`
	PiReason           string                 `json:"pi_reason,omitempty"`
	PiContinuation     string                 `json:"pi_continuation_delivery,omitempty"`
	MCP                *normalizedMCPEnvelope `json:"reconc_mcp,omitempty"`
}

// NormalizePiPayload validates the generated Pi extension envelope and
// converts it to the platform-neutral session payload. Session identity and
// cwd come from Pi's ExtensionContext, not model-controlled tool input.
func NormalizePiPayload(event string, payloadBytes []byte, repoRoot string) ([]byte, error) {
	binding, supported := piNativeEvents.lookup(event)
	if !supported {
		return nil, fmt.Errorf("unsupported Pi hook route %q", event)
	}
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return nil, errors.New("empty Pi payload")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}

	var raw piPayload
	if err := decodeSingleJSONValue(payloadBytes, &raw, false, piJSONDiagnostics); err != nil {
		return nil, err
	}
	if raw.HookEventName != binding.primary {
		return nil, fmt.Errorf("hook_event_name %q in Pi payload does not match route %q", raw.HookEventName, event)
	}
	if strings.TrimSpace(raw.SessionID) == "" {
		return nil, errors.New("missing non-empty session_id in Pi payload")
	}
	if err := validateHookPayloadCWD(raw.CWD, repoRoot, "Pi"); err != nil {
		return nil, err
	}
	if err := validatePiEventPayload(event, raw, repoRoot); err != nil {
		return nil, err
	}

	normalized := piNormalizedPayload{
		SessionID:      strings.TrimSpace(raw.SessionID),
		SessionFile:    strings.TrimSpace(raw.SessionFile),
		Prompt:         strings.TrimSpace(raw.Prompt),
		ToolName:       normalizePiOMPToolName(raw.ToolName),
		ToolInput:      raw.ToolInput,
		ToolResponse:   raw.ToolResponse,
		ToolUseID:      strings.TrimSpace(raw.ToolCallID),
		Error:          strings.TrimSpace(raw.Error),
		StopHookActive: raw.StopHookActive,
		ReconcRuntime:  "pi",
		PiEvent:        event,
		PiInputSource:  strings.TrimSpace(raw.InputSource),
		PiReason:       strings.TrimSpace(raw.Reason),
		PiContinuation: strings.TrimSpace(raw.Continuation),
	}
	if piToolEvent(event) {
		normalized.MCP = newNativeMCPEnvelope(policy.MCPPlatformPi, raw.ToolName, raw.ToolInput, event, "pi-post-tool-use", "pi-post-tool-use-failure")
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("normalize Pi payload: %w", err)
	}
	return body, nil
}

func validatePiEventPayload(event string, raw piPayload, repoRoot string) error {
	if piToolEvent(event) {
		if strings.TrimSpace(raw.ToolName) == "" {
			return fmt.Errorf("missing tool_name in Pi %s payload", raw.HookEventName)
		}
		if event != "pi-user-bash" && strings.TrimSpace(raw.ToolCallID) == "" {
			return fmt.Errorf("missing tool_call_id in Pi %s payload", raw.HookEventName)
		}
		if !jsonObject(raw.ToolInput) {
			return fmt.Errorf("tool_input in Pi %s payload must be a JSON object", raw.HookEventName)
		}
	}
	switch event {
	case "pi-post-tool-use", "pi-post-tool-use-failure":
		if raw.IsError == nil {
			return errors.New("missing is_error in Pi tool_result payload")
		}
		wantError := event == "pi-post-tool-use-failure"
		if *raw.IsError != wantError {
			return fmt.Errorf("is_error=%t in Pi payload does not match route %q", *raw.IsError, event)
		}
		if !jsonObject(raw.ToolResponse) {
			return errors.New("tool_response in Pi tool_result payload must be a JSON object")
		}
	case "pi-user-bash":
		if !strings.EqualFold(strings.TrimSpace(raw.ToolName), "bash") {
			return errors.New("pi user_bash payload must use the bash tool identity")
		}
		if raw.ExcludeFromContext == nil {
			return errors.New("missing exclude_from_context in Pi user_bash payload")
		}
		if err := validateHookPayloadCWD(raw.UserBashCWD, repoRoot, "Pi user_bash"); err != nil {
			return err
		}
	case "pi-stop":
		if raw.StopHookActive == nil || *raw.StopHookActive {
			return errors.New("pi agent_settled payload must declare stop_hook_active=false")
		}
	case "pi-continuation-requested", "pi-continuation-failed", "pi-continuation-suppressed":
		want := strings.TrimPrefix(event, "pi-continuation-")
		if raw.Continuation != want {
			return fmt.Errorf("continuation_delivery %q in Pi payload does not match route %q", raw.Continuation, event)
		}
	case "pi-session-start":
		if !oneOf(raw.Reason, "startup", "reload", "new", "resume", "fork") {
			return fmt.Errorf("invalid Pi session_start reason %q", raw.Reason)
		}
	case "pi-session-end":
		if !oneOf(raw.Reason, "quit", "reload", "new", "resume", "fork") {
			return fmt.Errorf("invalid Pi session_shutdown reason %q", raw.Reason)
		}
	case "pi-pre-compaction", "pi-post-compaction":
		if !oneOf(raw.Reason, "manual", "threshold", "overflow") {
			return fmt.Errorf("invalid Pi compaction reason %q", raw.Reason)
		}
		if raw.WillRetry == nil {
			return errors.New("missing will_retry in Pi compaction payload")
		}
	}
	return nil
}

func piToolEvent(event string) bool {
	switch event {
	case "pi-pre-tool-use", "pi-user-bash", "pi-post-tool-use", "pi-post-tool-use-failure":
		return true
	default:
		return false
	}
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
