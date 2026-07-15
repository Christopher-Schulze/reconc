package agentsession

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHookLivenessIsRateLimitedPerRoute(t *testing.T) {
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
	if err := recordHookLivenessAt(root, "codex", "codex-stop", first.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
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
	if len(records["codex"].Routes) != 2 {
		t.Fatalf("per-route liveness missing: %+v", records["codex"])
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

func TestHookLivenessRebuildsMissingFastPathMarkerWithoutRewritingState(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if err := recordHookLivenessAt(root, "codex", "codex-stop", first); err != nil {
		t.Fatal(err)
	}
	stateInfo, err := os.Stat(hookLivenessPath(root))
	if err != nil {
		t.Fatal(err)
	}
	markerPath := hookLivenessMarkerPath(root, "codex", "codex-stop")
	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}
	if err := recordHookLivenessAt(root, "codex", "codex-stop", first.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	markerInfo, err := os.Stat(markerPath)
	if err != nil {
		t.Fatalf("fast-path marker was not rebuilt: %v", err)
	}
	if !markerInfo.ModTime().Equal(first) {
		t.Fatalf("rebuilt marker must preserve the recorded route time: %s", markerInfo.ModTime())
	}
	stateInfoAfter, err := os.Stat(hookLivenessPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if !stateInfo.ModTime().Equal(stateInfoAfter.ModTime()) {
		t.Fatal("rebuilding a marker rewrote liveness state")
	}
}

func TestRecordHookLivenessNormalizesRuntimeAndPreservesRoute(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	if err := RecordHookLiveness(repo, "codex-session-start", "codex-session-start"); err != nil {
		t.Fatal(err)
	}
	records, err := ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	if records["codex"].Event != "codex-session-start" || records["codex"].Routes["codex-session-start"] == "" {
		t.Fatalf("normalized runtime liveness missing: %+v", records)
	}
}
