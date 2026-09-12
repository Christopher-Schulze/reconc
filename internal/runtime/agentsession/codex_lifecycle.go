package agentsession

import (
	jsonv2 "encoding/json/v2"
	"fmt"
)

// NormalizeCodexLifecyclePayload validates the exact new lifecycle envelopes.
// Existing routes retain their compatibility input contract. An interruption
// is an observation and never becomes a Stop or session-end request.
func NormalizeCodexLifecyclePayload(event string, body []byte) ([]byte, error) {
	if event != "codex-interrupt" && event != "codex-compaction-recovery" {
		return body, nil
	}
	var payload struct {
		SessionID string `json:"session_id"`
		TurnID    string `json:"turn_id"`
		Event     string `json:"hook_event_name"`
		Source    string `json:"source"`
	}
	if err := jsonv2.Unmarshal(body, &payload); err != nil || payload.SessionID == "" {
		return nil, fmt.Errorf("invalid Codex lifecycle identity")
	}
	if event == "codex-interrupt" && (payload.Event != "Interrupt" || payload.TurnID == "") {
		return nil, fmt.Errorf("native Codex interrupt requires its event and turn identity")
	}
	if event == "codex-compaction-recovery" && (payload.Event != "SessionStart" || payload.Source != "compact") {
		return nil, fmt.Errorf("native Codex recovery requires a compact SessionStart")
	}
	return body, nil
}
