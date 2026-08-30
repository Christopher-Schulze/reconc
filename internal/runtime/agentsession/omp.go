package agentsession

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

var ompNativeEvents = newNativeEventRegistry(
	nativeEventBinding{route: "omp-session-start", primary: "session_start"},
	nativeEventBinding{route: "omp-user-prompt-submit", primary: "input"},
	nativeEventBinding{route: "omp-pre-tool-use", primary: "tool_call"},
	nativeEventBinding{route: "omp-user-bash", primary: "user_bash"},
	nativeEventBinding{route: "omp-user-python", primary: "user_python"},
	nativeEventBinding{route: "omp-permission-request", primary: "tool_approval_requested"},
	nativeEventBinding{route: "omp-permission-result", primary: "tool_approval_resolved"},
	nativeEventBinding{route: "omp-post-tool-use", primary: "tool_result"},
	nativeEventBinding{route: "omp-post-tool-use-failure", primary: "tool_result"},
	nativeEventBinding{route: "omp-stop", primary: "session_stop"},
	nativeEventBinding{route: "omp-session-end", primary: "session_shutdown"},
	nativeEventBinding{route: "omp-pre-compaction", primary: "auto_compaction_start"},
	nativeEventBinding{route: "omp-post-compaction", primary: "auto_compaction_end"},
)

var ompJSONDiagnostics = singleJSONDiagnostics{
	decodePrefix:   "decode OMP payload",
	multipleValues: "multiple JSON values in OMP payload",
	trailingPrefix: "trailing data in OMP payload",
}

type ompPayload struct {
	HookEventName  string          `json:"hook_event_name"`
	SessionID      string          `json:"session_id"`
	SessionFile    string          `json:"session_file"`
	CWD            string          `json:"cwd"`
	Prompt         string          `json:"prompt"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	ToolCallID     string          `json:"tool_call_id"`
	Error          string          `json:"error"`
	Approved       *bool           `json:"approved"`
	Reason         string          `json:"reason"`
	StopHookActive *bool           `json:"stop_hook_active"`
	IsError        *bool           `json:"is_error"`
	InputSource    string          `json:"input_source"`
	ApprovalMode   string          `json:"approval_mode"`
	// user_bash carries the command the user typed rather than a tool call, so
	// it has no tool_call_id and reports its own working directory.
	UserBashCWD        string `json:"user_bash_cwd"`
	ExcludeFromContext *bool  `json:"exclude_from_context"`
	// user_python is observed, never decided: the code itself never leaves the
	// host, only its size and where it ran.
	UserPythonCWD string `json:"user_python_cwd"`
	CodeBytes     *int   `json:"code_bytes"`
}

type ompNormalizedPayload struct {
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
	OMPEvent           string                 `json:"omp_event"`
	OMPInputSource     string                 `json:"omp_input_source,omitempty"`
	OMPApprovalMode    string                 `json:"omp_approval_mode,omitempty"`
	OMPApproved        *bool                  `json:"omp_approved,omitempty"`
	UserPythonCWD      string                 `json:"user_python_cwd,omitempty"`
	ExcludeFromContext *bool                  `json:"exclude_from_context,omitempty"`
	CodeBytes          *int                   `json:"code_bytes,omitempty"`
	MCP                *normalizedMCPEnvelope `json:"reconc_mcp,omitempty"`
}

// NormalizeOMPPayload validates Reconc's generated OMP ExtensionAPI envelope
// and converts it into the platform-neutral session payload. The OMP extension
// derives identity from ExtensionContext rather than model-controlled input.
func NormalizeOMPPayload(event string, payloadBytes []byte, repoRoot string) ([]byte, error) {
	binding, supported := ompNativeEvents.lookup(event)
	if !supported {
		return nil, fmt.Errorf("unsupported OMP hook route %q", event)
	}
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return nil, errors.New("empty OMP payload")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}

	var raw ompPayload
	if err := decodeSingleJSONValue(payloadBytes, &raw, false, ompJSONDiagnostics); err != nil {
		return nil, err
	}
	if raw.HookEventName != binding.primary {
		return nil, fmt.Errorf("hook_event_name %q in OMP payload does not match route %q", raw.HookEventName, event)
	}
	if strings.TrimSpace(raw.SessionID) == "" {
		return nil, errors.New("missing non-empty session_id in OMP payload")
	}
	if err := validateHookPayloadCWD(raw.CWD, repoRoot, "OMP"); err != nil {
		return nil, err
	}
	if err := validateOMPEventPayload(event, raw, repoRoot); err != nil {
		return nil, err
	}

	normalized := ompNormalizedPayload{
		SessionID:       strings.TrimSpace(raw.SessionID),
		SessionFile:     strings.TrimSpace(raw.SessionFile),
		Prompt:          strings.TrimSpace(raw.Prompt),
		ToolName:        normalizePiOMPToolName(raw.ToolName),
		ToolInput:       raw.ToolInput,
		ToolResponse:    raw.ToolResponse,
		ToolUseID:       strings.TrimSpace(raw.ToolCallID),
		Error:           strings.TrimSpace(raw.Error),
		StopHookActive:  raw.StopHookActive,
		ReconcRuntime:   "omp",
		OMPEvent:        event,
		OMPInputSource:  strings.TrimSpace(raw.InputSource),
		OMPApprovalMode: strings.TrimSpace(raw.ApprovalMode),
		OMPApproved:     raw.Approved,
	}
	if event == "omp-stop" {
		normalized.StrictContinuation = true
	}
	if event == "omp-user-python" {
		normalized.UserPythonCWD = raw.UserPythonCWD
		normalized.ExcludeFromContext = raw.ExcludeFromContext
		normalized.CodeBytes = raw.CodeBytes
	}
	if ompToolEvent(event) {
		normalized.MCP = newNativeMCPEnvelope(policy.MCPPlatformOMP, raw.ToolName, raw.ToolInput, event, "omp-post-tool-use", "omp-post-tool-use-failure")
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("normalize OMP payload: %w", err)
	}
	return body, nil
}

func recordOMPUserPythonObservationResolved(root string, payload *HookPayload) error {
	if payload == nil || payload.Raw["reconc_runtime"] != "omp" || payload.Raw["omp_event"] != "omp-user-python" {
		return nil
	}
	workingDirectory, ok := payload.Raw["user_python_cwd"].(string)
	if !ok || workingDirectory == "" {
		return errors.New("normalized OMP user_python observation is missing its working directory")
	}
	codeBytes, ok := strictInteger(payload.Raw["code_bytes"])
	if !ok || codeBytes < 0 {
		return errors.New("normalized OMP user_python observation has an invalid code size")
	}
	excludeFromContext, ok := payload.Raw["exclude_from_context"].(bool)
	if !ok {
		return errors.New("normalized OMP user_python observation is missing its context flag")
	}
	return recordHookObservationResolved(root, "omp", "omp-user-python", workingDirectory, codeBytes, excludeFromContext)
}

func validateOMPEventPayload(event string, raw ompPayload, repoRoot string) error {
	if ompToolEvent(event) || event == "omp-permission-request" || event == "omp-permission-result" {
		if strings.TrimSpace(raw.ToolName) == "" {
			return fmt.Errorf("missing tool_name in OMP %s payload", raw.HookEventName)
		}
		// A user-typed command is not a tool call and carries no call id.
		if event != "omp-user-bash" && strings.TrimSpace(raw.ToolCallID) == "" {
			return fmt.Errorf("missing tool_call_id in OMP %s payload", raw.HookEventName)
		}
	}
	if ompToolEvent(event) && !jsonObject(raw.ToolInput) {
		return fmt.Errorf("tool_input in OMP %s payload must be a JSON object", raw.HookEventName)
	}
	switch event {
	case "omp-user-bash":
		if !strings.EqualFold(strings.TrimSpace(raw.ToolName), "bash") {
			return errors.New("OMP user_bash payload must use the bash tool identity")
		}
		if raw.ExcludeFromContext == nil {
			return errors.New("missing exclude_from_context in OMP user_bash payload")
		}
		if err := validateHookPayloadCWD(raw.UserBashCWD, repoRoot, "OMP user_bash"); err != nil {
			return err
		}
	case "omp-user-python":
		if raw.ExcludeFromContext == nil {
			return errors.New("missing exclude_from_context in OMP user_python payload")
		}
		if raw.CodeBytes == nil || *raw.CodeBytes < 0 {
			return errors.New("OMP user_python payload must report a non-negative code size")
		}
		if err := validateHookPayloadCWD(raw.UserPythonCWD, repoRoot, "OMP user_python"); err != nil {
			return err
		}
	case "omp-post-tool-use", "omp-post-tool-use-failure":
		if raw.IsError == nil {
			return errors.New("missing is_error in OMP tool_result payload")
		}
		wantError := event == "omp-post-tool-use-failure"
		if *raw.IsError != wantError {
			return fmt.Errorf("is_error=%t in OMP payload does not match route %q", *raw.IsError, event)
		}
		if !jsonObject(raw.ToolResponse) {
			return errors.New("tool_response in OMP tool_result payload must be a JSON object")
		}
	case "omp-permission-result":
		if raw.Approved == nil {
			return errors.New("missing approved decision in OMP tool_approval_resolved payload")
		}
	case "omp-stop":
		if raw.StopHookActive == nil {
			return errors.New("missing stop_hook_active in OMP session_stop payload")
		}
	}
	return nil
}

func ompToolEvent(event string) bool {
	switch event {
	case "omp-pre-tool-use", "omp-user-bash", "omp-post-tool-use", "omp-post-tool-use-failure":
		return true
	default:
		return false
	}
}

func jsonObject(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}
