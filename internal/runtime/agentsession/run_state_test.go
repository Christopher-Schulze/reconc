package agentsession

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryRunStateRoundTrip(t *testing.T) {
	repo := t.TempDir()
	want := runLoopState{
		Enabled:              true,
		Mode:                 runLoopModeRepo,
		NoProgressNudges:     2,
		LastCurrent:          "TASK-013|subtask",
		AwaitingContinuation: true,
		EnabledAt:            "2026-07-14T20:00:00Z",
	}
	if err := saveRunLoopState(repo, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("state round trip mismatch: got %+v want %+v", got, want)
	}
}

func TestLegacySessionRunStateIsRejected(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".reconc", "runloop", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"enabled":true,"mode":"session"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.DisabledReason != "legacy_session_mode_removed" {
		t.Fatalf("legacy session state must not activate repository run: %+v", state)
	}
}

func TestRunStateClampsNegativeNoProgressCount(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".reconc", "runloop", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"enabled":true,"mode":"repo","no_progress_nudges":-7}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.NoProgressNudges != 0 {
		t.Fatalf("negative no-progress count was not clamped: %+v", state)
	}
}

func TestUserInterruptRequiresExplicitBooleanFlag(t *testing.T) {
	value := true
	if !isUserStopInterrupt(&HookPayload{IsInterrupt: &value}) {
		t.Fatal("explicit true interrupt flag was not detected")
	}
	value = false
	for _, payload := range []*HookPayload{nil, {}, {IsInterrupt: &value}, {Error: "user interrupted"}, {Raw: map[string]interface{}{"is_compaction": true}}} {
		if isUserStopInterrupt(payload) {
			t.Fatalf("non-explicit interrupt was misclassified: %#v", payload)
		}
	}
}
