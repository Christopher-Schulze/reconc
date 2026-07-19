package grokacp

import (
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestPrepareStrictTUIStopDoesNotDependOnLeaderDiscovery(t *testing.T) {
	t.Setenv("GROK_SESSION_ID", "strict-without-leader")
	t.Setenv(SteerEnv, "")
	t.Setenv(leaderSocketEnv, t.TempDir()+"/missing.sock")
	payload := []byte(`{"session_id":"strict-without-leader","reconc_runtime":"grok"}`)

	prepared, strict, err := PrepareStrictTUIStop(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strict {
		t.Fatal("eligible Grok stop must stay strict even when no leader endpoint is discoverable")
	}
	parsed, err := agentsession.ParsePayload(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.StrictContinuation {
		t.Fatalf("prepared payload = %s", prepared)
	}
}
