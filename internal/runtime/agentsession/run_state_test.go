package agentsession

import "testing"

func TestRepositoryRunStateRoundTrip(t *testing.T) {
	repo := t.TempDir()
	want := repositoryRunState{
		Enabled:              true,
		NoProgressNudges:     2,
		LastProgressHash:     [32]byte{1, 2, 3},
		AwaitingContinuation: true,
		EnabledAt:            1_752_524_800_000_000_000,
	}
	if err := saveRepositoryRunState(repo, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadRepositoryRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("state round trip mismatch: got %+v want %+v", got, want)
	}
}

func TestRunStateClampsNegativeNoProgressCount(t *testing.T) {
	repo := t.TempDir()
	if err := saveRepositoryRunState(repo, repositoryRunState{Enabled: true, NoProgressNudges: -7}); err != nil {
		t.Fatal(err)
	}
	state, err := loadRepositoryRunState(repo)
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
