package customruntime

import (
	"encoding/json"
	"fmt"
	"strings"

	"reconc.dev/reconc/internal/schema"
)

// NeutralRequest is the public, versioned transport envelope. Payload contains
// only selected neutral fields, never the complete host request.
type NeutralRequest struct {
	Schema        string                 `json:"$schema"`
	FormatVersion string                 `json:"format_version"`
	Runtime       string                 `json:"runtime"`
	HostEvent     string                 `json:"host_event"`
	Event         Event                  `json:"event"`
	Payload       map[string]interface{} `json:"payload"`
}

// NormalizeHostPayload applies exact JSON Pointer mappings without executing
// code, expressions, templates, shell expansion, or network operations.
func NormalizeHostPayload(manifest Manifest, route Route, body []byte) (NeutralRequest, []byte, error) {
	if len(body) == 0 || len(body) > maxHostPayloadBytes {
		return NeutralRequest{}, nil, fmt.Errorf("custom host payload must be 1..%d bytes", maxHostPayloadBytes)
	}
	selected, err := selectHostValues(route.Fields, body)
	if err != nil {
		return NeutralRequest{}, nil, err
	}
	neutral, err := buildNeutralPayload(manifest, route, selected)
	if err != nil {
		return NeutralRequest{}, nil, err
	}
	request := NeutralRequest{
		Schema:        schema.Resolve(schema.NeutralHookRequest),
		FormatVersion: RequestFormatVersion, Runtime: manifest.Runtime(),
		HostEvent: route.HostEvent, Event: route.Event, Payload: neutral,
	}
	encoded, err := json.Marshal(neutral)
	if err != nil {
		return NeutralRequest{}, nil, fmt.Errorf("encode neutral hook payload: %w", err)
	}
	return request, encoded, nil
}

func buildNeutralPayload(manifest Manifest, route Route, selected selectedHostValues) (map[string]interface{}, error) {
	sessionID, err := requiredString(selected, route.Fields.SessionID, "session_id")
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{
		"session_id": sessionID, "reconc_runtime": manifest.Runtime(),
	}
	if err := copyOptionalString(selected, out, route.Fields.ToolName, "tool_name"); err != nil {
		return nil, err
	}
	if err := copyOptionalObject(selected, out, route.Fields.ToolInput, "tool_input"); err != nil {
		return nil, err
	}
	if err := copyOptionalObject(selected, out, route.Fields.ToolResponse, "tool_response"); err != nil {
		return nil, err
	}
	for _, mapping := range []struct{ pointer, field string }{
		{route.Fields.ToolUseID, "tool_use_id"}, {route.Fields.Error, "error"},
	} {
		if err := copyOptionalString(selected, out, mapping.pointer, mapping.field); err != nil {
			return nil, err
		}
	}
	for _, mapping := range []struct{ pointer, field string }{
		{route.Fields.IsInterrupt, "is_interrupt"}, {route.Fields.StopHookActive, "stop_hook_active"},
		{route.Fields.StrictContinuation, "strict_continuation"},
	} {
		if err := copyOptionalBool(selected, out, mapping.pointer, mapping.field); err != nil {
			return nil, err
		}
	}
	if route.Fields.ExitCode != "" {
		value, ok := selected[route.Fields.ExitCode]
		if !ok {
			return nil, fmt.Errorf("mapped exit_code is missing")
		}
		exitCode, ok := exactInt(value)
		if !ok {
			return nil, fmt.Errorf("mapped exit_code must be an exact integer")
		}
		response, _ := out["tool_response"].(map[string]interface{})
		if response == nil {
			response = map[string]interface{}{}
		}
		response["exit_code"] = exitCode
		out["tool_response"] = response
	}
	if route.Event == EventMCPBefore || route.Event == EventMCPAfter {
		mcp, err := buildMCPEnvelope(manifest, route, selected)
		if err != nil {
			return nil, err
		}
		out["reconc_mcp"] = mcp
	}
	return out, nil
}

func buildMCPEnvelope(manifest Manifest, route Route, selected selectedHostValues) (map[string]interface{}, error) {
	tool, err := requiredString(selected, route.Fields.MCPTool, "mcp_tool")
	if err != nil {
		return nil, err
	}
	mcp := map[string]interface{}{
		"platform": manifest.Runtime(), "tool": tool, "observed": true,
		"blocking_pre_hook": route.Event == EventMCPBefore && route.Guarantees.PreExecution && route.Guarantees.SynchronousResponse,
		"input_valid":       true,
	}
	if err := copyOptionalString(selected, mcp, route.Fields.MCPServerFingerprint, "server_fingerprint"); err != nil {
		return nil, err
	}
	if err := copyOptionalString(selected, mcp, route.Fields.MCPOutcome, "outcome"); err != nil {
		return nil, err
	}
	return mcp, nil
}

func requiredString(selected selectedHostValues, pointer, field string) (string, error) {
	value, ok := selected[pointer]
	if !ok {
		return "", fmt.Errorf("mapped %s is missing", field)
	}
	text, ok := value.(string)
	if !ok || text == "" || strings.TrimSpace(text) != text {
		return "", fmt.Errorf("mapped %s must be an exact non-empty string", field)
	}
	return text, nil
}

func copyOptionalString(selected selectedHostValues, out map[string]interface{}, pointer, field string) error {
	if pointer == "" {
		return nil
	}
	value, ok := selected[pointer]
	if !ok {
		return fmt.Errorf("mapped %s is missing", field)
	}
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("mapped %s must be a string", field)
	}
	out[field] = text
	return nil
}

func copyOptionalBool(selected selectedHostValues, out map[string]interface{}, pointer, field string) error {
	if pointer == "" {
		return nil
	}
	value, ok := selected[pointer]
	if !ok {
		return fmt.Errorf("mapped %s is missing", field)
	}
	flag, ok := value.(bool)
	if !ok {
		return fmt.Errorf("mapped %s must be a boolean", field)
	}
	out[field] = flag
	return nil
}

func copyOptionalObject(selected selectedHostValues, out map[string]interface{}, pointer, field string) error {
	if pointer == "" {
		return nil
	}
	value, ok := selected[pointer]
	if !ok {
		return fmt.Errorf("mapped %s is missing", field)
	}
	object, ok := value.(map[string]interface{})
	if !ok {
		return fmt.Errorf("mapped %s must be an object", field)
	}
	out[field] = object
	return nil
}

func exactInt(value interface{}) (int, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := number.Int64()
	if err != nil || int64(int(parsed)) != parsed {
		return 0, false
	}
	return int(parsed), true
}

func validJSONPointer(pointer string) bool {
	if pointer == "" {
		return true
	}
	if !strings.HasPrefix(pointer, "/") {
		return false
	}
	for index := 0; index < len(pointer); index++ {
		if pointer[index] != '~' {
			continue
		}
		if index+1 >= len(pointer) || pointer[index+1] != '0' && pointer[index+1] != '1' {
			return false
		}
		index++
	}
	return true
}
