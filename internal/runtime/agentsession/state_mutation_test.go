package agentsession

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestAggregateSessionBudgetPersistsTaintWithoutPartialStateWrite(t *testing.T) {
	_, repo := withStateRoot(t)
	const sessionID = "aggregate-budget"
	state, err := InitializeSessionState(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	path := sessionStatePath(state.RepoRoot, sessionID)
	payload := strings.Repeat("x", 60*1024)
	overflowed := false
	for index := 0; index < maxPendingToolCalls; index++ {
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		updated, err := MutateSessionState(repo, sessionID, func(current SessionState) SessionState {
			return PutPendingToolCall(current, fmt.Sprintf("call-%02d", index), PendingToolCall{
				ToolName: "Read", ToolInput: map[string]interface{}{"payload": payload},
			})
		})
		if err != nil {
			t.Fatalf("aggregate mutation %d returned an unrecoverable error: %v", index, err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !updated.EvidenceOverflow {
			if len(after) > MaxSessionStateBytes {
				t.Fatalf("published state exceeded budget: %d", len(after))
			}
			continue
		}
		overflowed = true
		if updated.EvidenceOverflowReason != "session_state" || updated.EvidenceOverflowLimit != "byte_budget" {
			t.Fatalf("aggregate overflow marker = %s/%s", updated.EvidenceOverflowReason, updated.EvidenceOverflowLimit)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("aggregate overflow partially published the rejected mutation")
		}
		if _, err := os.Stat(evidenceTaintPath(state.RepoRoot)); err != nil {
			t.Fatalf("aggregate overflow did not persist durable taint: %v", err)
		}
		loaded, err := LoadSessionState(repo, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if !loaded.EvidenceOverflow || loaded.EvidenceOverflowReason != "session_state" {
			t.Fatalf("durable aggregate taint was not inherited: %+v", loaded)
		}
		break
	}
	if !overflowed {
		t.Fatal("combined pending-call state never reached the aggregate budget")
	}
}

func TestStateMapMutatorsDoNotAliasLoadedState(t *testing.T) {
	original := emptyState("/repo", "map-alias")
	original.WritePaths = []string{"src/a.go"}
	original.WriteEpochs = map[string]uint64{"src/a.go": 1}
	original.EvidenceEpoch = 7
	original.PendingToolCalls = map[string]PendingToolCall{"call-1": {ToolName: "Read"}}

	withWrite := RecordWriteEvent(original, []string{"src/a.go"})
	withPending := PutPendingToolCall(original, "call-2", PendingToolCall{ToolName: "Write"})
	if original.WriteEpochs["src/a.go"] != 1 || withWrite.WriteEpochs["src/a.go"] == 1 {
		t.Fatalf("write-epoch mutation aliased input: original=%v result=%v", original.WriteEpochs, withWrite.WriteEpochs)
	}
	if _, found := original.PendingToolCalls["call-2"]; found || len(withPending.PendingToolCalls) != 2 {
		t.Fatalf("pending-call mutation aliased input: original=%v result=%v", original.PendingToolCalls, withPending.PendingToolCalls)
	}
}

func TestCompletionExecutionInputsOwnsEmptyEpochMap(t *testing.T) {
	root := t.TempDir()
	state := emptyState(root, "completion-alias")
	inputs, err := completionExecutionInputs(root, state, true, true, []string{"src/new.go"}, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.WriteEpochs) != 0 {
		t.Fatalf("completion capture mutated source epochs: %v", state.WriteEpochs)
	}
	if inputs.WriteEpochs["src/new.go"] == 0 {
		t.Fatalf("completion capture did not annotate dirty path: %v", inputs.WriteEpochs)
	}
}
