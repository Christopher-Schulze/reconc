//go:build darwin

package agentsession

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSessionStateAcceptsDarwinCaseAlias(t *testing.T) {
	_, parent := withStateRoot(t)
	repo := filepath.Join(parent, "MixedCaseRepo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	alternate := filepath.Join(parent, "mixedcaserepo")
	if _, err := os.Stat(alternate); err != nil {
		t.Skip("test requires a case-insensitive Darwin filesystem")
	}
	state, err := InitializeSessionState(repo, "darwin-case")
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot := state.RepoRoot
	state.RepoRoot = alternate
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	path := sessionStatePath(canonicalRoot, state.SessionID)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSessionState(repo, state.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	resolved, resolveErr := ResolveRepoRoot(repo)
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if loaded.RepoRoot != resolved {
		t.Fatalf("loaded repo root = %q, want canonical %q", loaded.RepoRoot, resolved)
	}
}
