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

var piNativeEvents = map[string]string{
	"pi-session-start":           "session_start",
	"pi-user-prompt-submit":      "input",
	"pi-pre-tool-use":            "tool_call",
	"pi-user-bash":               "user_bash",
	"pi-post-tool-use":           "tool_result",
	"pi-post-tool-use-failure":   "tool_result",
	"pi-stop":                    "agent_settled",
	"pi-continuation-requested":  "agent_settled",
	"pi-continuation-failed":     "agent_settled",
	"pi-continuation-suppressed": "agent_settled",
	"pi-session-end":             "session_shutdown",
	"pi-pre-compaction":          "session_before_compact",
	"pi-post-compaction":         "session_compact",
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
	SessionID          string          `json:"session_id"`
	SessionFile        string          `json:"session_file,omitempty"`
	Prompt             string          `json:"prompt,omitempty"`
	ToolName           string          `json:"tool_name,omitempty"`
	ToolInput          json.RawMessage `json:"tool_input,omitempty"`
	ToolResponse       json.RawMessage `json:"tool_response,omitempty"`
	ToolUseID          string          `json:"tool_use_id,omitempty"`
	Error              string          `json:"error,omitempty"`
	StopHookActive     *bool           `json:"stop_hook_active,omitempty"`
	StrictContinuation bool            `json:"strict_continuation,omitempty"`
	ReconcRuntime      string          `json:"reconc_runtime"`
	PiEvent            string          `json:"pi_event"`
	PiInputSource      string          `json:"pi_input_source,omitempty"`
	PiReason           string          `json:"pi_reason,omitempty"`
	PiContinuation     string          `json:"pi_continuation_delivery,omitempty"`
	MCP                *piMCPEnvelope  `json:"reconc_mcp,omitempty"`
}

type piMCPEnvelope struct {
	Platform        policy.MCPPlatform `json:"platform"`
	Tool            string             `json:"tool"`
	Observed        bool               `json:"observed"`
	BlockingPreHook bool               `json:"blocking_pre_hook"`
	InputValid      bool               `json:"input_valid"`
	Outcome         string             `json:"outcome,omitempty"`
}

// NormalizePiPayload validates the generated Pi extension envelope and
// converts it to the platform-neutral session payload. Session identity and
// cwd come from Pi's ExtensionContext, not model-controlled tool input.
func NormalizePiPayload(event string, payloadBytes []byte, repoRoot string) ([]byte, error) {
	expectedEvent := piNativeEvents[event]
	if expectedEvent == "" {
		return nil, fmt.Errorf("unsupported Pi hook route %q", event)
	}
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return nil, errors.New("empty Pi payload")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}

	var raw piPayload
	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode Pi payload: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values in Pi payload")
		}
		return nil, fmt.Errorf("trailing data in Pi payload: %w", err)
	}
	if raw.HookEventName != expectedEvent {
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
		ToolName:       normalizePiToolName(raw.ToolName),
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
		outcome := ""
		if event == "pi-post-tool-use" {
			outcome = "success"
		} else if event == "pi-post-tool-use-failure" {
			outcome = "failure"
		}
		normalized.MCP = &piMCPEnvelope{
			Platform:        policy.MCPPlatformPi,
			Tool:            strings.TrimSpace(raw.ToolName),
			Observed:        false,
			BlockingPreHook: true,
			InputValid:      jsonObject(raw.ToolInput),
			Outcome:         outcome,
		}
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

func normalizePiToolName(native string) string {
	native = strings.TrimSpace(native)
	switch strings.ToLower(native) {
	case "bash":
		return "Bash"
	case "read":
		return "Read"
	case "write":
		return "Write"
	case "edit":
		return "Edit"
	default:
		return native
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
