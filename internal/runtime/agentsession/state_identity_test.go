package agentsession

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionFileKeyPreventsSanitisationCollisions(t *testing.T) {
	root := t.TempDir()
	if got, want := filepath.Base(sessionStatePath(root, "session_123")), "session_123.json"; got != want {
		t.Fatalf("canonical session filename = %q, want %q", got, want)
	}
	if sessionStatePath(root, "a/b") == sessionStatePath(root, "a_b") {
		t.Fatal("distinct session IDs must not share a state path")
	}
	if sessionLockPath(root, "a/b") == sessionLockPath(root, "a_b") {
		t.Fatal("distinct session IDs must not share a lock path")
	}
}

func TestSessionEntryPointsRejectInvalidIdentifiers(t *testing.T) {
	repo := t.TempDir()
	for _, sessionID := range []string{"", " leading", "trailing ", "control\ncharacter", strings.Repeat("x", maxSessionIDBytes+1)} {
		if _, err := InitializeSessionState(repo, sessionID); err == nil {
			t.Fatalf("InitializeSessionState accepted invalid session ID %q", sessionID)
		}
	}
}

func TestLoadSessionStateMigratesLegacyCollisionPronePath(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	const sessionID = "legacy/session"
	legacy := emptyState(root, sessionID)
	legacy.ReportPath = legacySessionReportPath(root, sessionID)
	body, err := marshalStateDeterministic(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := legacySessionStatePath(root, sessionID)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := EnsureSessionState(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ReportPath != sessionReportPath(root, sessionID) {
		t.Fatalf("migrated report path = %q, want %q", state.ReportPath, sessionReportPath(root, sessionID))
	}
	if _, err := os.Stat(sessionStatePath(root, sessionID)); err != nil {
		t.Fatalf("collision-resistant state was not persisted: %v", err)
	}
}

func TestResolveActiveSessionIDRejectsOversizedPointer(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	path := activeSessionPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxSessionIDBytes+2)), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ResolveActiveSessionID(repo); err == nil {
		t.Fatal("oversized active-session pointer must fail closed")
	}
}

func TestCleanupDoesNotDeleteStateWhenActivePointerIsInvalid(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	state, err := InitializeSessionState(repo, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activeSessionPath(state.RepoRoot), []byte("invalid\nidentifier\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := CleanupSessionState(repo, "session-a"); err == nil {
		t.Fatal("invalid active-session pointer must fail cleanup closed")
	}
	if _, err := os.Stat(sessionStatePath(state.RepoRoot, "session-a")); err != nil {
		t.Fatalf("state was deleted before active-pointer validation: %v", err)
	}
}
