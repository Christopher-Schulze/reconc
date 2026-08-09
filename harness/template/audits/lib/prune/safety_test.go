package prune

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunZeroPolicyUsesDefaultsInsteadOfDeletingEverything(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	sessions := filepath.Join(home, "sessions", "claude", "projects", projectKey(repo), "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(sessions, "current.json")
	if err := os.WriteFile(state, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := Run(Options{RepoRoot: repo, ReconcHome: home, Policy: Policy{}})
	if report.SessionsDeleted != 0 {
		t.Fatalf("zero policy deleted %d session files", report.SessionsDeleted)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("zero policy removed retained state: %v", err)
	}
}
