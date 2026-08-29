package agentsession

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestNormalizedAwaySessionMutationSkipsWrite(t *testing.T) {
	_, repo := withStateRoot(t)
	if _, err := InitializeSessionState(repo, "normalize-away"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "normalize-away", func(state SessionState) SessionState {
		state.Commands = append(state.Commands, "git status")
		return state
	}); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	statePath := sessionStatePath(root, "normalize-away")
	activePath := activeSessionPath(root)
	fixed := time.Unix(1_700_000_000, 0)
	for _, path := range []string{statePath, activePath} {
		if err := os.Chtimes(path, fixed, fixed); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "normalize-away", func(state SessionState) SessionState {
		state.Commands = append(state.Commands, "  git status  ")
		return state
	}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("normalization-equivalent mutation rewrote state bytes")
	}
	for _, path := range []string{statePath, activePath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(fixed) {
			t.Fatalf("normalization-equivalent mutation rewrote %s: modtime=%s", path, info.ModTime())
		}
	}
}

func TestMaximumNormalizedStateMutationComparesOnceEquivalent(t *testing.T) {
	state := normalizeSessionState(maximumNormalizationState())
	mutated := state
	mutated.Commands = append(mutated.Commands, "  "+state.Commands[0]+"  ")
	mutated = normalizeSessionState(mutated)
	if !reflect.DeepEqual(state, mutated) {
		t.Fatal("maximum-state normalization-equivalent mutation changed state")
	}
}

func BenchmarkNormalizedMaximumStateMutationComparison(b *testing.B) {
	state := normalizeSessionState(maximumNormalizationState())
	mutated := state
	mutated.Commands = append(mutated.Commands, "  "+state.Commands[0]+"  ")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		candidate := normalizeSessionState(mutated)
		if !reflect.DeepEqual(state, candidate) {
			b.Fatal("maximum-state mutation unexpectedly changed normalized state")
		}
	}
}

func TestSessionStateMissingFileStillForcesPublication(t *testing.T) {
	_, repo := withStateRoot(t)
	if _, err := InitializeSessionState(repo, "missing-publication"); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	path := sessionStatePath(root, "missing-publication")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	state := emptyState(root, "missing-publication")
	written, err := saveSessionStateLockedIfChanged(state)
	if err != nil || !written {
		t.Fatalf("missing state publication: written=%v err=%v", written, err)
	}
	if _, err := os.Stat(filepath.Clean(path)); err != nil {
		t.Fatalf("state was not republished: %v", err)
	}
}
