package agentsession

import (
	"os"
	"testing"
)

func TestReadRunDecisionsMissingIsEmpty(t *testing.T) {
	repo := t.TempDir()
	ds, err := ReadRunDecisions(repo, 0)
	if err != nil {
		t.Fatalf("missing log must not error: %v", err)
	}
	if len(ds) != 0 {
		t.Fatalf("missing log must be empty, got %d", len(ds))
	}
}

func TestReadRunDecisionsOrderLimitAndMalformed(t *testing.T) {
	repo := t.TempDir()
	for _, b := range []string{"a", "b", "c"} {
		if err := appendRunDecision(repo, RunDecision{Event: "stop", Branch: b}); err != nil {
			t.Fatalf("append %s: %v", b, err)
		}
	}
	// A malformed line must be skipped, not fail the whole read.
	path, err := runDecisionLogPath(repo)
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

	all, err := ReadRunDecisions(repo, 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("malformed line must be skipped, got %d records", len(all))
	}
	if all[0].Branch != "a" || all[2].Branch != "c" {
		t.Fatalf("append order not preserved: %+v", all)
	}
	last2, err := ReadRunDecisions(repo, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(last2) != 2 || last2[0].Branch != "b" || last2[1].Branch != "c" {
		t.Fatalf("limit must return the last N in order, got %+v", last2)
	}
}

func TestReadRepositoryRunStatusReflectsState(t *testing.T) {
	repo := t.TempDir()
	info, err := ReadRepositoryRunStatus(repo)
	if err != nil {
		t.Fatalf("missing state must not error: %v", err)
	}
	if info.Enabled {
		t.Fatal("missing state must be disabled")
	}
	if err := saveRepositoryRunState(repo, repositoryRunState{
		Enabled:              true,
		AwaitingContinuation: true,
		NoProgressNudges:     2,
	}); err != nil {
		t.Fatal(err)
	}
	info, err = ReadRepositoryRunStatus(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Enabled || !info.AwaitingContinuation || info.NoProgressNudges != 2 {
		t.Fatalf("status snapshot mismatch: %+v", info)
	}
}
