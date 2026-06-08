package agentsession

import (
	"os"
	"testing"
)

func TestReadRunLoopDecisionsMissingIsEmpty(t *testing.T) {
	repo := t.TempDir()
	ds, err := ReadRunLoopDecisions(repo, 0)
	if err != nil {
		t.Fatalf("missing log must not error: %v", err)
	}
	if len(ds) != 0 {
		t.Fatalf("missing log must be empty, got %d", len(ds))
	}
}

func TestReadRunLoopDecisionsOrderLimitAndMalformed(t *testing.T) {
	repo := t.TempDir()
	for _, b := range []string{"a", "b", "c"} {
		if err := appendRunLoopDecision(repo, RunLoopDecision{Event: "stop", Branch: b}); err != nil {
			t.Fatalf("append %s: %v", b, err)
		}
	}
	// A malformed line must be skipped, not fail the whole read.
	path, err := runLoopDecisionLogPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{ not valid json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	all, err := ReadRunLoopDecisions(repo, 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("malformed line must be skipped, got %d records", len(all))
	}
	if all[0].Branch != "a" || all[2].Branch != "c" {
		t.Fatalf("append order not preserved: %+v", all)
	}
	last2, err := ReadRunLoopDecisions(repo, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(last2) != 2 || last2[0].Branch != "b" || last2[1].Branch != "c" {
		t.Fatalf("limit must return the last N in order, got %+v", last2)
	}
}

func TestReadRunLoopStatusReflectsState(t *testing.T) {
	repo := t.TempDir()
	info, err := ReadRunLoopStatus(repo)
	if err != nil {
		t.Fatalf("missing state must not error: %v", err)
	}
	if info.Enabled {
		t.Fatal("missing state must be disabled")
	}
	if err := saveRunLoopState(repo, runLoopState{
		Enabled:              true,
		Runtime:              "cursor",
		SessionID:            "sess-1",
		ActiveRunID:          "sess-1",
		AwaitingContinuation: true,
		NoProgressNudges:     2,
	}); err != nil {
		t.Fatal(err)
	}
	info, err = ReadRunLoopStatus(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Enabled || info.Runtime != "cursor" || info.ActiveRunID != "sess-1" || !info.AwaitingContinuation || info.NoProgressNudges != 2 {
		t.Fatalf("status snapshot mismatch: %+v", info)
	}
}
