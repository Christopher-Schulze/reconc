package agentsession

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

// Claude Code and Codex publish no dedicated MCP hook event. Both surface MCP
// tool calls on their generic tool events under the exact `mcp__<server>__<tool>`
// name, and both accept a regular-expression matcher, so Reconc routes that one
// namespace into the MCP path and derives the exact identity from the tool name
// instead of inventing a host event that does not exist.
const namespacedMCPPrefix = "mcp__"

// NormalizedMCPTool reports whether a host tool identity is a namespaced MCP
// call. Server and tool segments must both be non-empty: a bare `mcp__` or a
// name with no second separator is not an identity a policy can select.
func NormalizedMCPTool(name string) bool {
	if !strings.HasPrefix(name, namespacedMCPPrefix) {
		return false
	}
	server, tool, found := strings.Cut(strings.TrimPrefix(name, namespacedMCPPrefix), "__")
	return found && server != "" && tool != ""
}

// NormalizeNamespacedMCPPayload converts a Claude Code or Codex generic tool
// payload into the MCP payload contract. The full host tool name is the exact
// selector identity, which keeps the policy selector identical to what the host
// and its own matchers name.
func NormalizeNamespacedMCPPayload(platform policy.MCPPlatform, before bool, payloadBytes []byte) ([]byte, error) {
	if !platform.Valid() {
		return nil, fmt.Errorf("namespaced MCP platform %q is not part of the public contract", string(platform))
	}
	if len(bytes.TrimSpace(payloadBytes)) == 0 {
		return nil, fmt.Errorf("%s MCP payload is empty", platform)
	}
	if err := checkJSONDepth(payloadBytes, MaxJSONDepth); err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return nil, fmt.Errorf("%s MCP payload is not valid JSON: %w", platform, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s MCP payload must be a JSON object", platform)
	}
	tool := cursorToolName(raw)
	if !NormalizedMCPTool(tool) {
		// The route exists only for the MCP namespace. A payload that reaches
		// it with any other identity is envelope drift, not an MCP call.
		return nil, fmt.Errorf("%s MCP route received tool %q, which is not an mcp__<server>__<tool> identity", platform, tool)
	}
	sessionID := cursorFirstString(raw, "session_id", "sessionId", "conversation_id", "conversationId")
	if sessionID == "" {
		return nil, fmt.Errorf("%s MCP payload has no session identity", platform)
	}
	input, inputValid := cursorMCPInput(raw)
	envelope := normalizedMCPEnvelope{
		Platform:        platform,
		Tool:            tool,
		Observed:        true,
		Phase:           MCPPhaseBefore,
		BlockingPreHook: true,
		InputValid:      inputValid,
	}
	if !before {
		envelope.Phase = MCPPhaseAfter
		envelope.Outcome = cursorMCPOutcome(raw)
	}
	// MCP arguments and results carry external server credentials and complete
	// remote payloads. Only the redacted contract continues into shared parsing,
	// diagnostics, and state, exactly as on the hosts with native MCP events.
	out := map[string]interface{}{
		"session_id": sessionID,
		"reconc_mcp": mcpEnvelopeToMap(envelope),
		"tool_input": input,
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("%s MCP payload could not be normalized: %w", platform, err)
	}
	return encoded, nil
}
