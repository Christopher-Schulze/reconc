package agentsession

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHookLivenessIsRateLimitedPerRuntime(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if err := recordHookLivenessAt(root, "codex", "session_start", first); err != nil {
		t.Fatal(err)
	}
	path := hookLivenessPath(root)
	infoBefore, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordHookLivenessAt(root, "codex", "session_start", first.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	infoAfter, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Fatal("rate-limited liveness event rewrote state")
	}
	if err := recordHookLivenessAt(root, "codex", "session_start", first.Add(7*time.Hour)); err != nil {
		t.Fatal(err)
	}
	records, err := ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	if records["codex"].LastSeen != first.Add(7*time.Hour).Format(time.RFC3339Nano) {
		t.Fatalf("liveness timestamp not refreshed: %+v", records["codex"])
	}
}

func TestHookLivenessReadIsBoundedAndFailClosed(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	path := hookLivenessPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, maxHookLivenessBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHookLiveness(repo); err == nil {
		t.Fatal("oversized liveness state must fail closed")
	}
}

func TestSessionStartRecordsNormalizedRuntimeLiveness(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	t.Setenv(reconcHookRuntimeEnv, "codex-session-start")
	repo := t.TempDir()
	result := RunSessionStart(repo, []byte(`{"session_id":"live"}`))
	if result.ExitCode != 0 || result.Stderr != "" {
		t.Fatalf("session start failed: %+v", result)
	}
	records, err := ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	if records["codex"].Event != "session_start" {
		t.Fatalf("normalized runtime liveness missing: %+v", records)
	}
}
