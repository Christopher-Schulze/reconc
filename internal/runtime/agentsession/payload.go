package agentsession

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"reconc.dev/reconc/internal/policy"
)

// Hard limits per the threat model in docs/architecture.md.
const (
	// Hook payloads can contain complete file bodies for Read, Write, Edit, and
	// apply_patch events. Keep the cap finite for memory safety, but large
	// enough for real repository artifacts such as generated lockfiles and
	// multi-megabyte specifications.
	MaxPayloadBytes = 64 << 20 // 64 MiB
	MaxJSONDepth    = 32
	StdinTimeout    = 5 * time.Second
)

// WriteToolNames are Claude Code tool names that represent a file
// write. Mirrors the Python WRITE_TOOL_NAMES set.
var WriteToolNames = map[string]struct{}{
	"Edit":         {},
	"MultiEdit":    {},
	"Write":        {},
	"NotebookEdit": {},
	"TabWrite":     {},
	"StrReplace":   {},
	"Delete":       {},
	"apply_patch":  {},
	"edit":         {},
	"multiedit":    {},
	"write":        {},
	"notebookedit": {},
	"tabwrite":     {},
	"strreplace":   {},
	"delete":       {},
}

// ReadToolNames are tools that represent a file read.
var ReadToolNames = map[string]struct{}{"Read": {}, "read": {}, "TabRead": {}, "tabread": {}}

// CommandToolNames are tools that execute a shell command.
var CommandToolNames = map[string]struct{}{"Bash": {}, "bash": {}}

// ErrPayloadTooLarge is returned when stdin produces more than
// MaxPayloadBytes. Callers treat this as fail-closed for PreToolUse /
// Stop per the threat model.
var ErrPayloadTooLarge = errors.New("hook payload exceeds 64 MiB limit")

// ErrPayloadReadTimeout is returned when the host does not deliver the
// payload (EOF included) within StdinTimeout. Without this bound a host
// that spawns the hook but withholds stdin would wedge the process
// forever on platforms whose plugin layer has no own kill timer.
var ErrPayloadReadTimeout = errors.New("hook payload read timed out")

// ReadPayload reads up to MaxPayloadBytes from r, enforcing the size
// cap and the StdinTimeout deadline. The read runs in a goroutine
// because os.Stdin does not reliably support SetReadDeadline across
// platforms; on timeout the goroutine is abandoned (the process exits
// shortly after, so nothing leaks in practice).
func ReadPayload(r io.Reader) ([]byte, error) {
	return readPayloadWithTimeout(r, StdinTimeout)
}

func readPayloadWithTimeout(r io.Reader, timeout time.Duration) ([]byte, error) {
	type outcome struct {
		data []byte
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		limited := io.LimitReader(r, MaxPayloadBytes+1)
		data, err := io.ReadAll(limited)
		done <- outcome{data: data, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-done:
		if result.err != nil {
			return nil, fmt.Errorf("read stdin: %w", result.err)
		}
		if int64(len(result.data)) > MaxPayloadBytes {
			return nil, ErrPayloadTooLarge
		}
		return result.data, nil
	case <-timer.C:
		return nil, ErrPayloadReadTimeout
	}
}

// ParsePayload decodes one hook payload with depth-limited JSON
// decoding. Returns a structured HookPayload with the fields the
// handlers care about; unknown keys are preserved in the Raw map for
// future-compat.
func ParsePayload(data []byte) (*HookPayload, error) {
	if len(data) == 0 {
		return nil, errors.New("hook payload is empty")
	}
	// Go's stdlib decoder doesn't expose a max-depth knob, so we
	// pre-scan the byte stream for brace/bracket depth before
	// delegating to Unmarshal. Cheap; runs in one pass.
	if err := checkJSONDepth(data, MaxJSONDepth); err != nil {
		return nil, err
	}

	var raw map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("hook payload is not valid JSON: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("hook payload contains multiple JSON values")
		}
		return nil, fmt.Errorf("hook payload has trailing data: %w", err)
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
	SessionID          string
	Prompt             string
	ToolName           string
	ToolInput          map[string]interface{}
	ToolResponse       map[string]interface{}
	ToolUseID          string
	Error              string
	IsInterrupt        *bool
	StopHookActive     bool
	StrictContinuation bool
	MCP                *MCPPayload
	Raw                map[string]interface{}
}

// MCPPayload is the redacted platform-neutral MCP lifecycle envelope.
type MCPPayload struct {
	Platform          policy.MCPPlatform
	Tool              string
	ServerFingerprint string
	BlockingPreHook   bool
	InputValid        bool
	Outcome           string
}

// FilePath returns the first known file path field of tool_input without
// changing filename bytes, or "" if absent.
func (p *HookPayload) FilePath() string {
	paths := p.FilePaths()
	if len(paths) > 0 {
		return paths[0]
	}
	return ""
}

// FilePaths returns all known file paths addressed by the tool input.
// Codex reports apply_patch edits through tool_input.command, so parse
// the patch headers instead of treating the patch as a shell command.
func (p *HookPayload) FilePaths() []string {
	if p == nil || p.ToolInput == nil {
		return nil
	}
	var paths []string
	for _, key := range []string{"file_path", "filePath", "path", "file", "target", "absolute_path", "absolutePath", "relative_path", "relativePath", "target_file", "targetFile", "notebook_path", "notebookPath"} {
		if value, _ := p.ToolInput[key].(string); value != "" {
			paths = appendUniquePath(paths, value)
		}
	}
	if p.ToolName == "apply_patch" {
		paths = append(paths, parseApplyPatchPaths(p.Command())...)
	}
	return dedupePaths(paths)
}

// Command returns the first known shell command field of tool_input,
// trimmed, or "" if absent.
func (p *HookPayload) Command() string {
	if p == nil || p.ToolInput == nil {
		return ""
	}
	for _, key := range []string{"command", "cmd", "script"} {
		if v, _ := p.ToolInput[key].(string); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
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
		switch n := v.(type) {
		case json.Number:
			i64, err := n.Int64()
			if err != nil || int64(int(i64)) != i64 {
				continue
			}
			i := int(i64)
			return &i
		case float64:
			i := int(n)
			if float64(i) != n {
				continue
			}
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

func parseApplyPatchPaths(patch string) []string {
	// Collect every referenced path without deduping here; the caller runs
	// one linear dedupePaths pass over the combined result, so this stays
	// O(n) instead of the O(n^2) a per-line appendUniquePath would cost on
	// large patches.
	var paths []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSuffix(line, "\r")
		for _, prefix := range []string{
			"*** Add File: ",
			"*** Update File: ",
			"*** Delete File: ",
			"*** Move to: ",
		} {
			if strings.HasPrefix(line, prefix) {
				if path := strings.TrimPrefix(line, prefix); path != "" {
					paths = append(paths, path)
				}
				break
			}
		}
	}
	return paths
}

func appendUniquePath(paths []string, path string) []string {
	if path == "" {
		return paths
	}
	for _, existing := range paths {
		if existing == path {
			return paths
		}
	}
	return append(paths, path)
}

func dedupePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
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
// with the normalized session_id validated. Platform adapters must derive a
// stable identity before this boundary when the host omits one.
func payloadFromMap(raw map[string]interface{}) (*HookPayload, error) {
	sessionID, ok := raw["session_id"].(string)
	if !ok {
		return nil, errors.New("hook payload must include a non-empty 'session_id'")
	}
	if err := validateSessionID(sessionID); err != nil {
		return nil, fmt.Errorf("hook payload has invalid 'session_id': %w", err)
	}

	toolName, _ := raw["tool_name"].(string)
	toolName = strings.TrimSpace(toolName)

	prompt, _ := raw["prompt"].(string)
	prompt = strings.TrimSpace(prompt)

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
	strictContinuation, _ := raw["strict_continuation"].(bool)
	mcp, err := parseMCPPayload(raw["reconc_mcp"])
	if err != nil {
		return nil, err
	}

	return &HookPayload{
		SessionID:          sessionID,
		Prompt:             prompt,
		ToolName:           toolName,
		ToolInput:          toolInput,
		ToolResponse:       toolResponse,
		ToolUseID:          toolUseID,
		Error:              errString,
		IsInterrupt:        isInterrupt,
		StopHookActive:     stopHookActive,
		StrictContinuation: strictContinuation,
		MCP:                mcp,
		Raw:                raw,
	}, nil
}

func parseMCPPayload(raw interface{}) (*MCPPayload, error) {
	if raw == nil {
		return nil, nil
	}
	mapping, ok := raw.(map[string]interface{})
	if !ok {
		return nil, errors.New("hook payload reconc_mcp must be an object")
	}
	platformText, _ := mapping["platform"].(string)
	platform := policy.MCPPlatform(platformText)
	if !platform.Valid() {
		return nil, errors.New("hook payload reconc_mcp.platform is invalid")
	}
	tool, _ := mapping["tool"].(string)
	if tool == "" || strings.TrimSpace(tool) != tool {
		return nil, errors.New("hook payload reconc_mcp.tool must be an exact non-empty identity")
	}
	fingerprint, _ := mapping["server_fingerprint"].(string)
	if fingerprint != "" {
		probe := policy.MCPToolPolicy{
			Platform:          platform,
			ServerFingerprint: fingerprint,
			Tool:              tool,
			Effect:            policy.MCPEffectExternal,
		}
		if err := probe.Validate(); err != nil {
			return nil, fmt.Errorf("hook payload reconc_mcp.server_fingerprint is invalid: %w", err)
		}
	}
	blocking, _ := mapping["blocking_pre_hook"].(bool)
	inputValid, _ := mapping["input_valid"].(bool)
	outcome, _ := mapping["outcome"].(string)
	if outcome != "" && outcome != "success" && outcome != "failure" {
		return nil, errors.New("hook payload reconc_mcp.outcome must be success or failure")
	}
	return &MCPPayload{
		Platform:          platform,
		Tool:              tool,
		ServerFingerprint: fingerprint,
		BlockingPreHook:   blocking,
		InputValid:        inputValid,
		Outcome:           outcome,
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
