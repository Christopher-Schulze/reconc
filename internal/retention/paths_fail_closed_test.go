package retention

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionFileIDIsCollisionResistant(t *testing.T) {
	if got := SessionFileID("session_123"); got != "session_123" {
		t.Fatalf("canonical session ID changed: %q", got)
	}
	if SessionFileID("a/b") == SessionFileID("a_b") {
		t.Fatal("distinct session IDs produced the same storage key")
	}
	if err := ValidateSessionID(strings.Repeat("x", MaxSessionIDBytes+1)); err == nil {
		t.Fatal("oversized session ID was accepted")
	}
}

func TestRetentionDoesNotDeleteStateWhenActivePointerIsInvalid(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	project := ProjectDir(stateRoot, repo)
	sessionDir := filepath.Join(project, "sessions")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(sessionDir, "old-session.json")
	if err := os.WriteFile(statePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(statePath, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "active-session.txt"), []byte("invalid\nidentifier\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy()
	policy.Sessions = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	policy.StateTotalBytes = 0

	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: time.Now(), TempRoot: t.TempDir()})
	if len(report.Errors) == 0 || !strings.Contains(strings.Join(report.Errors, "\n"), "resolve active session") {
		t.Fatalf("invalid active pointer was not reported: %+v", report.Errors)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state was deleted under an untrusted active pointer: %v", err)
	}
}

func TestGeneratedBinaryActivityScanReportsUnreadableCache(t *testing.T) {
	root := t.TempDir()
	cachePath := filepath.Join(root, "cache")
	if err := os.WriteFile(cachePath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := generatedBinaryActiveNames(cachePath, time.Now(), time.Hour); err == nil {
		t.Fatal("non-directory cache must fail activity discovery closed")
	}
}

func TestRepoRuntimeRetentionRejectsNonDirectoryCacheWithoutDeletingIt(t *testing.T) {
	repo := t.TempDir()
	cachePath := filepath.Join(repo, ".reconc", "cache")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("owned state, not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ownedRepoRuntimeBytes(repo); err == nil {
		t.Fatal("non-directory repository cache must fail byte accounting closed")
	}

	policy := DefaultPolicy()
	policy.RepoRuntimeBytes = 0
	report := Run(Options{
		RepoRoot:  repo,
		StateRoot: t.TempDir(),
		TempRoot:  t.TempDir(),
		Policy:    policy,
		Now:       time.Now(),
	})
	if !strings.Contains(strings.Join(report.Errors, "\n"), "not a directory") {
		t.Fatalf("non-directory repository cache was not reported: %+v", report.Errors)
	}
	if content, err := os.ReadFile(cachePath); err != nil || string(content) != "owned state, not a directory" {
		t.Fatalf("invalid cache path was modified: content=%q err=%v", content, err)
	}
}
