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

var ompNativeEvents = map[string]string{
	"omp-session-start":         "session_start",
	"omp-user-prompt-submit":    "input",
	"omp-pre-tool-use":          "tool_call",
	"omp-user-bash":             "user_bash",
	"omp-user-python":           "user_python",
	"omp-permission-request":    "tool_approval_requested",
	"omp-permission-result":     "tool_approval_resolved",
	"omp-post-tool-use":         "tool_result",
	"omp-post-tool-use-failure": "tool_result",
	"omp-stop":                  "session_stop",
	"omp-session-end":           "session_shutdown",
	"omp-pre-compaction":        "auto_compaction_start",
	"omp-post-compaction":       "auto_compaction_end",
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
	OMPEvent           string          `json:"omp_event"`
	OMPInputSource     string          `json:"omp_input_source,omitempty"`
	OMPApprovalMode    string          `json:"omp_approval_mode,omitempty"`
	OMPApproved        *bool           `json:"omp_approved,omitempty"`
	MCP                *ompMCPEnvelope `json:"reconc_mcp,omitempty"`
}

type ompMCPEnvelope struct {
	Platform        policy.MCPPlatform `json:"platform"`
	Tool            string             `json:"tool"`
	Observed        bool               `json:"observed"`
	BlockingPreHook bool               `json:"blocking_pre_hook"`
	InputValid      bool               `json:"input_valid"`
	Outcome         string             `json:"outcome,omitempty"`
}

// NormalizeOMPPayload validates Reconc's generated OMP ExtensionAPI envelope
// and converts it into the platform-neutral session payload. The OMP extension
// derives identity from ExtensionContext rather than model-controlled input.
func NormalizeOMPPayload(event string, payloadBytes []byte, repoRoot string) ([]byte, error) {
	expectedEvent := ompNativeEvents[event]
	if expectedEvent == "" {
		return nil, fmt.Errorf("unsupported OMP hook route %q", event)
	}
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return nil, errors.New("empty OMP payload")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}

	var raw ompPayload
	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode OMP payload: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values in OMP payload")
		}
		return nil, fmt.Errorf("trailing data in OMP payload: %w", err)
	}
	if raw.HookEventName != expectedEvent {
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
		ToolName:        normalizeOMPToolName(raw.ToolName),
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
	if ompToolEvent(event) {
		outcome := ""
		if event == "omp-post-tool-use" {
			outcome = "success"
		} else if event == "omp-post-tool-use-failure" {
			outcome = "failure"
		}
		normalized.MCP = &ompMCPEnvelope{
			Platform:        policy.MCPPlatformOMP,
			Tool:            strings.TrimSpace(raw.ToolName),
			Observed:        false,
			BlockingPreHook: true,
			InputValid:      jsonObject(raw.ToolInput),
			Outcome:         outcome,
		}
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("normalize OMP payload: %w", err)
	}
	return body, nil
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

func normalizeOMPToolName(native string) string {
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
