package agentsession

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
)

var dshNativeEvents = newNativeEventRegistry(
	nativeEventBinding{route: "dsh-session-start", primary: "session_start"},
	nativeEventBinding{route: "dsh-pre-tool-use", primary: "tools/pre-execute"},
	nativeEventBinding{route: "dsh-post-tool-use", primary: "tools/result"},
	nativeEventBinding{route: "dsh-post-tool-use-failure", primary: "tools/result"},
	nativeEventBinding{route: "dsh-stop", primary: "agent/turn-stopping"},
)

var dshJSONDiagnostics = singleJSONDiagnostics{
	decodePrefix:   "decode DSH payload",
	multipleValues: "multiple JSON values in DSH payload",
	trailingPrefix: "trailing data in DSH payload",
}

type dshPayload struct {
	HookEventName  string          `json:"hook_event_name"`
	SessionID      string          `json:"session_id"`
	CWD            string          `json:"cwd"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolCallID     string          `json:"tool_call_id"`
	RootCallID     string          `json:"root_call_id"`
	AgentID        string          `json:"agent_id"`
	IsError        *bool           `json:"is_error"`
	Error          string          `json:"error"`
	Observed       *bool           `json:"result_observed"`
	StopHookActive *bool           `json:"stop_hook_active"`
}

type dshNormalizedPayload struct {
	SessionID      string                 `json:"session_id"`
	ToolName       string                 `json:"tool_name,omitempty"`
	ToolInput      json.RawMessage        `json:"tool_input,omitempty"`
	ToolUseID      string                 `json:"tool_use_id,omitempty"`
	Error          string                 `json:"error,omitempty"`
	ReconcRuntime  string                 `json:"reconc_runtime"`
	DSHEvent       string                 `json:"dsh_event"`
	DSHCWD         string                 `json:"dsh_cwd"`
	DSHAgentID     string                 `json:"dsh_agent_id,omitempty"`
	DSHRootCallID  string                 `json:"dsh_root_call_id,omitempty"`
	DSHIsError     *bool                  `json:"dsh_is_error,omitempty"`
	DSHObserved    *bool                  `json:"dsh_result_observed,omitempty"`
	StopHookActive *bool                  `json:"stop_hook_active,omitempty"`
	MCP            *normalizedMCPEnvelope `json:"reconc_mcp,omitempty"`
}

// NormalizeDSHPayload validates the generated Cordis extension's typed
// execution envelope. Its post routes are observations of the host's final
// result, never proof that an underlying tool body or shell process succeeded.
func NormalizeDSHPayload(event string, payloadBytes []byte, repoRoot string) ([]byte, error) {
	binding, supported := dshNativeEvents.lookup(event)
	if !supported {
		return nil, fmt.Errorf("unsupported DSH hook route %q", event)
	}
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return nil, errors.New("empty DSH payload")
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}
	var raw dshPayload
	if err := decodeSingleJSONValue(payloadBytes, &raw, false, dshJSONDiagnostics); err != nil {
		return nil, err
	}
	if raw.HookEventName != binding.primary {
		return nil, fmt.Errorf("hook_event_name %q in DSH payload does not match route %q", raw.HookEventName, event)
	}
	if strings.TrimSpace(raw.SessionID) == "" {
		return nil, errors.New("missing non-empty session_id in DSH payload")
	}
	if err := validateHookPayloadCWD(raw.CWD, repoRoot, "DSH"); err != nil {
		return nil, err
	}
	root, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve DSH repository root: %w", err)
	}
	current, err := pathidentity.ResolveExisting(raw.CWD)
	if err != nil {
		return nil, fmt.Errorf("resolve DSH cwd: %w", err)
	}
	if relative, err := filepath.Rel(root, current); err != nil || relative != "." {
		return nil, errors.New("DSH session cwd must equal repository root")
	}
	if event != "dsh-session-start" && event != "dsh-stop" {
		if strings.TrimSpace(raw.ToolName) == "" || strings.TrimSpace(raw.ToolCallID) == "" || strings.TrimSpace(raw.RootCallID) == "" {
			return nil, errors.New("DSH tool event requires name, call ID, and root call ID")
		}
		if !jsonObject(raw.ToolInput) {
			return nil, errors.New("DSH tool_input must be a JSON object")
		}
	}
	if event == "dsh-post-tool-use" || event == "dsh-post-tool-use-failure" {
		if raw.IsError == nil || raw.Observed == nil || !*raw.Observed || *raw.IsError != (event == "dsh-post-tool-use-failure") {
			return nil, errors.New("DSH final result observation does not match its route")
		}
	}
	if event == "dsh-stop" && raw.StopHookActive == nil {
		return nil, errors.New("DSH turn-stopping requires stop_hook_active")
	}
	if event == "dsh-pre-tool-use" {
		switch raw.ToolName {
		case "pwsh":
			return nil, errors.New("DSH PowerShell has no Reconc command-policy parser")
		case "run_code":
			return nil, errors.New("DSH run_code has no inspectable command contract; set DSH_TOOLS_MODE=native and use read/write/edit/bash")
		case "terminal_open", "terminal_send", "terminal_signal":
			return nil, errors.New("DSH raw terminal state has no inspectable command contract; use one-shot bash from the repository root")
		}
		if raw.ToolName == "bash" {
			var input struct {
				Workdir *string `json:"workdir"`
			}
			if err := json.Unmarshal(raw.ToolInput, &input); err != nil {
				return nil, fmt.Errorf("decode DSH bash workdir: %w", err)
			}
			if input.Workdir != nil {
				working := *input.Workdir
				if !filepath.IsAbs(working) {
					working = filepath.Join(raw.CWD, working)
				}
				if err := validateHookPayloadCWD(working, repoRoot, "DSH bash workdir"); err != nil {
					return nil, err
				}
				resolved, err := pathidentity.ResolveExisting(working)
				if err != nil {
					return nil, fmt.Errorf("resolve DSH bash workdir: %w", err)
				}
				if relative, err := filepath.Rel(root, resolved); err != nil || relative != "." {
					return nil, errors.New("DSH bash workdir must equal repository root")
				}
			}
		}
	}
	name := normalizePiOMPToolName(raw.ToolName)
	if raw.ToolName == "str_replace_editor" && (event == "dsh-pre-tool-use" || !bytes.Equal(bytes.TrimSpace(raw.ToolInput), []byte("{}"))) {
		var input struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(raw.ToolInput, &input); err != nil {
			return nil, fmt.Errorf("decode DSH str_replace_editor command: %w", err)
		}
		switch input.Command {
		case "view":
			name = "Read"
		case "create", "str_replace", "insert":
			name = "Write"
		default:
			return nil, fmt.Errorf("unsupported DSH str_replace_editor command %q", input.Command)
		}
	}
	normalized := dshNormalizedPayload{
		SessionID:      strings.TrimSpace(raw.SessionID),
		ToolName:       name,
		ToolInput:      raw.ToolInput,
		ToolUseID:      strings.TrimSpace(raw.ToolCallID),
		Error:          strings.TrimSpace(raw.Error),
		ReconcRuntime:  "dsh",
		DSHEvent:       event,
		DSHCWD:         raw.CWD,
		DSHAgentID:     strings.TrimSpace(raw.AgentID),
		DSHRootCallID:  strings.TrimSpace(raw.RootCallID),
		DSHIsError:     raw.IsError,
		DSHObserved:    raw.Observed,
		StopHookActive: raw.StopHookActive,
	}
	if event == "dsh-pre-tool-use" {
		// The schema-backed built-in platform list is immutable under its
		// published identity; custom:dsh is the explicit selector namespace.
		normalized.MCP = newNativeMCPEnvelope(policy.MCPPlatform("custom:dsh"), raw.ToolName, raw.ToolInput, event, "", "")
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("normalize DSH payload: %w", err)
	}
	return body, nil
}
