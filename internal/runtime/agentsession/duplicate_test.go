package agentsession

import "testing"

func TestPayloadMatchesRuntimeSession(t *testing.T) {
	repo := t.TempDir()
	if _, err := initializeSessionState(repo, "session-1", "copilot"); err != nil {
		t.Fatal(err)
	}
	if !PayloadMatchesRuntimeSession(repo, []byte(`{"session_id":"session-1"}`), "copilot") {
		t.Fatal("matching compatible payload should be suppressed")
	}
	if PayloadMatchesRuntimeSession(repo, []byte(`{"session_id":"session-2"}`), "copilot") {
		t.Fatal("different session must not be suppressed")
	}
	if PayloadMatchesRuntimeSession(repo, []byte(`{"session_id":"session-1"}`), "devin") {
		t.Fatal("different runtime must not be suppressed")
	}
	if PayloadMatchesRuntimeSession(repo, []byte(`not-json`), "copilot") {
		t.Fatal("malformed payload must fail open")
	}
}
