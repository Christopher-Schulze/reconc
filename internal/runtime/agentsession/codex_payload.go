package agentsession

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"strings"
)

// NormalizeCodexPayload separates a native child's evidence before any tool or
// MCP normalization. Codex's session_id is shared by the root and descendants;
// agent_id is the concrete child thread identity. Root and legacy payloads
// without child metadata retain their existing session contract.
func NormalizeCodexPayload(event string, body []byte) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := jsonv2.Unmarshal(body, &raw); err != nil || raw == nil {
		return nil, fmt.Errorf("invalid Codex hook envelope")
	}
	agent, hasAgent := raw["agent_id"]
	if !hasAgent {
		var nativeEvent string
		if err := jsonv2.Unmarshal(raw["hook_event_name"], &nativeEvent); err == nil &&
			(nativeEvent == "SubagentStart" || nativeEvent == "SubagentStop") {
			return nil, fmt.Errorf("native Codex child lifecycle requires agent_id")
		}
		return NormalizeCodexLifecyclePayload(event, body)
	}
	for _, field := range []string{"session_id", "agent_id"} {
		var id string
		if err := jsonv2.Unmarshal(raw[field], &id); err != nil || id == "" || strings.TrimSpace(id) != id {
			return nil, fmt.Errorf("invalid Codex %s", field)
		}
	}
	raw["session_id"] = agent
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("normalize Codex child identity: %w", err)
	}
	return NormalizeCodexLifecyclePayload(event, encoded)
}
