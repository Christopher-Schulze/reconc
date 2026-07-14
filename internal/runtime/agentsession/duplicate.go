package agentsession

import "strings"

// PayloadMatchesRuntimeSession reports whether a compatible hook payload is a
// duplicate for the first-class runtime that already initialized this session.
// It is read-only and is used only for platforms known to load another
// platform's hook files after their native hook file.
func PayloadMatchesRuntimeSession(repoRoot string, payloadBytes []byte, runtimeName string) bool {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return false
	}
	status, err := ReadRunLoopStatus(repoRoot)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(status.Runtime), strings.TrimSpace(runtimeName)) &&
		strings.TrimSpace(status.SessionID) == strings.TrimSpace(payload.SessionID)
}
