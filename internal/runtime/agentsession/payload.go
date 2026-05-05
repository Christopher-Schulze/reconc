package agentsession

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Hard limits per the threat model in docs/architecture.md.
const (
	MaxPayloadBytes = 1 << 20 // 1 MiB
	MaxJSONDepth    = 32
	StdinTimeout    = 5 * time.Second
)

// WriteToolNames are Claude Code tool names that represent a file
// write. Mirrors the Python WRITE_TOOL_NAMES set.
var WriteToolNames = map[string]struct{}{
	"Edit":      {},
	"MultiEdit": {},
	"Write":     {},
}

// ReadToolNames are tools that represent a file read.
var ReadToolNames = map[string]struct{}{"Read": {}}

// CommandToolNames are tools that execute a shell command.
var CommandToolNames = map[string]struct{}{"Bash": {}}

// ErrPayloadTooLarge is returned when stdin produces more than
// MaxPayloadBytes. Callers treat this as fail-closed for PreToolUse /
// Stop per the threat model.
var ErrPayloadTooLarge = errors.New("hook payload exceeds 1 MiB limit")

// ReadPayload reads up to MaxPayloadBytes from r, enforcing the size
// cap. A deadline-enforcing reader is the caller's responsibility
// (typically the CLI wires an io.Reader derived from os.Stdin with a
// deadline or SetReadDeadline on the underlying *os.File).
func ReadPayload(r io.Reader) ([]byte, error) {
	limited := io.LimitReader(r, MaxPayloadBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	if int64(len(data)) > MaxPayloadBytes {
		return nil, ErrPayloadTooLarge
	}
	return data, nil
}

// ParsePayload decodes one hook payload with depth-limited JSON
// decoding. Returns a structured HookPayload with the fields the
// handlers care about; unknown keys are preserved in the Raw map for
// future-compat.
func ParsePayload(data []byte) (*HookPayload, error) {
	if len(data) == 0 {
		return nil, errors.New("hook payload is empty")
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	// Go's stdlib decoder doesn't expose a max-depth knob, so we
	// pre-scan the byte stream for brace/bracket depth before
	// delegating to Unmarshal. Cheap; runs in one pass.
	if err := checkJSONDepth(data, MaxJSONDepth); err != nil {
		return nil, err
	}

	var raw map[string]interface{}
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("hook payload is not valid JSON: %w", err)
	}
	if raw == nil {
		return nil, errors.New("hook payload must be a JSON object")
	}
	return payloadFromMap(raw)
}

// HookPayload is the structured view of the JSON payload Claude Code /
// Codex sends on stdin. Fields we know about are typed; everything
// else stays in Raw for future forward-compat.
type HookPayload struct {
	SessionID      string
	ToolName       string
	ToolInput      map[string]interface{}
	ToolResponse   map[string]interface{}
	ToolUseID      string
	Error          string
	IsInterrupt    *bool
	StopHookActive bool
	Raw            map[string]interface{}
}

// FilePath returns the "file_path" field of tool_input, trimmed, or
// "" if absent.
func (p *HookPayload) FilePath() string {
	if p == nil || p.ToolInput == nil {
		return ""
	}
	v, _ := p.ToolInput["file_path"].(string)
	return strings.TrimSpace(v)
}

// Command returns the "command" field of tool_input, trimmed, or ""
// if absent.
func (p *HookPayload) Command() string {
	if p == nil || p.ToolInput == nil {
		return ""
	}
	v, _ := p.ToolInput["command"].(string)
	return strings.TrimSpace(v)
}

// ExitCode extracts the tool-response exit code, supporting all four
// historical spellings (exit_code, exitCode, status_code, statusCode).
// Returns nil if none are present.
func (p *HookPayload) ExitCode() *int {
	if p == nil || p.ToolResponse == nil {
		return nil
	}
	for _, key := range []string{"exit_code", "exitCode", "status_code", "statusCode"} {
		v, ok := p.ToolResponse[key]
		if !ok {
			continue
		}
		// JSON numbers decode as float64 by default.
		switch n := v.(type) {
		case float64:
			i := int(n)
			return &i
		case int:
			return &n
		}
	}
	return nil
}

// IsWriteTool reports whether the payload's tool_name is a write tool.
func (p *HookPayload) IsWriteTool() bool {
	_, ok := WriteToolNames[p.ToolName]
	return ok
}

// IsReadTool reports whether the payload's tool_name is a read tool.
func (p *HookPayload) IsReadTool() bool {
	_, ok := ReadToolNames[p.ToolName]
	return ok
}

// IsCommandTool reports whether the payload's tool_name is a command
// tool.
func (p *HookPayload) IsCommandTool() bool {
	_, ok := CommandToolNames[p.ToolName]
	return ok
}

// payloadFromMap converts the raw decoded object into a HookPayload
// with the session_id validated. Missing session_id is a hard error;
// every event from Claude Code / Codex must carry one.
func payloadFromMap(raw map[string]interface{}) (*HookPayload, error) {
	sessionID, _ := raw["session_id"].(string)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("hook payload must include a non-empty 'session_id'")
	}

	toolName, _ := raw["tool_name"].(string)
	toolName = strings.TrimSpace(toolName)

	toolInput, _ := raw["tool_input"].(map[string]interface{})
	toolResponse, _ := raw["tool_response"].(map[string]interface{})
	toolUseID, _ := raw["tool_use_id"].(string)
	toolUseID = strings.TrimSpace(toolUseID)

	errString, _ := raw["error"].(string)
	errString = strings.TrimSpace(errString)

	var isInterrupt *bool
	if v, ok := raw["is_interrupt"].(bool); ok {
		isInterrupt = &v
	}

	stopHookActive, _ := raw["stop_hook_active"].(bool)

	return &HookPayload{
		SessionID:      sessionID,
		ToolName:       toolName,
		ToolInput:      toolInput,
		ToolResponse:   toolResponse,
		ToolUseID:      toolUseID,
		Error:          errString,
		IsInterrupt:    isInterrupt,
		StopHookActive: stopHookActive,
		Raw:            raw,
	}, nil
}

// checkJSONDepth scans for nesting depth without allocating a full
// parse tree. Depths > max produce an error. Fast-path: one linear
// scan over the bytes counting { / [ minus } / ] while respecting
// string boundaries and backslash escapes.
func checkJSONDepth(data []byte, max int) error {
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > max {
				return fmt.Errorf("hook payload exceeds %d levels of JSON nesting", max)
			}
		case '}', ']':
			if depth == 0 {
				return fmt.Errorf("unbalanced JSON closing bracket at byte %d", i)
			}
			depth--
		}
	}
	if depth != 0 {
		return errors.New("unbalanced JSON braces in hook payload")
	}
	return nil
}
