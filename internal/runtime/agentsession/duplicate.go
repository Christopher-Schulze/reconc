package agentsession

import "strings"

// PayloadMatchesRuntimeSession reports whether a compatible hook payload is a
// duplicate for the first-class runtime that already initialized this session.
// Runtime ownership lives in session evidence, never in repository run state.
func PayloadMatchesRuntimeSession(repoRoot string, payloadBytes []byte, runtimeName string) bool {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return false
	}
	state, err := LoadSessionState(repoRoot, payload.SessionID)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(state.Runtime), strings.TrimSpace(runtimeName))
}
