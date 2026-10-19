package agentsession

import (
	"os"
	"strings"
	"testing"
)

// An operator-confirmed evidence recovery must complete even when the active
// or origin session state file is unusable; the file is set aside instead of
// leaving manual removal as the only way out.
func TestEvidenceRecoverySetsAsideUnusableSessionState(t *testing.T) {
	for _, test := range []struct {
		name   string
		active string
	}{
		{name: "active origin", active: "origin"},
		{name: "inactive origin", active: "successor"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, repo := withStateRoot(t)
			root, err := ResolveRepoRoot(repo)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := MutateSessionState(root, "origin", func(state SessionState) SessionState {
				markEvidenceOverflowWithLimit(&state, "claims", "item_bytes")
				return state
			}); err != nil {
				t.Fatal(err)
			}
			status, err := ReadEvidenceTaintStatus(root)
			if err != nil || !status.Present {
				t.Fatalf("taint fixture missing: %+v %v", status, err)
			}
			if err := writeActiveSession(root, test.active); err != nil {
				t.Fatal(err)
			}
			unusable := []byte("{\"session_id\": \"origin\", truncated")
			if err := os.WriteFile(sessionStatePath(root, "origin"), unusable, 0o600); err != nil {
				t.Fatal(err)
			}

			resolved, err := ResolveEvidenceTaint(root, status.Token, "reviewed unusable origin state")
			if err != nil {
				t.Fatalf("recovery stopped at the unusable state file: %v", err)
			}
			if len(resolved.SetAsideStates) != 1 || !strings.HasPrefix(resolved.SetAsideStates[0], sessionStatePath(root, "origin")+".unusable-") {
				t.Fatalf("set-aside report = %v", resolved.SetAsideStates)
			}
			kept, err := os.ReadFile(resolved.SetAsideStates[0])
			if err != nil || string(kept) != string(unusable) {
				t.Fatalf("set-aside state lost its bytes: %q %v", kept, err)
			}
			after, err := ReadEvidenceTaintStatus(root)
			if err != nil || after.Present {
				t.Fatalf("taint survived recovery: %+v %v", after, err)
			}
			state, err := LoadSessionState(root, "origin")
			if err != nil || state.EvidenceOverflow {
				t.Fatalf("origin did not continue from an empty window: %+v %v", state, err)
			}
		})
	}
}
