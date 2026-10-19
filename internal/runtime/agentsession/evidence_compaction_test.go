package agentsession

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// sealRecurringEvidence seals count segments that repeat the same reads,
// commands and claims, as a long session does when it revisits its files.
func sealRecurringEvidence(t *testing.T, root, sessionID string, count int) {
	t.Helper()
	for index := range count {
		if _, err := MutateSessionState(root, sessionID, func(state SessionState) SessionState {
			state = AppendReadPath(state, "src/shared.go")
			state = AppendReadPath(state, fmt.Sprintf("src/round-%d.go", index%3))
			state = AppendCommand(state, "go test ./...")
			return AppendClaim(state, "reviewed")
		}); err != nil {
			t.Fatal(err)
		}
		if err := withSessionLock(root, sessionID, func() error {
			current, err := loadSessionStateResolved(root, sessionID)
			if err != nil {
				return err
			}
			rotated, err := rotateSessionEvidenceLocked(root, current)
			if err != nil {
				return err
			}
			return saveSessionStateLocked(rotated)
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// overflowLiveClaims fills the live claims to their item ceiling and then
// records one more claim, as consecutive hook events do; the last event
// overflows the field and rotates the chain.
func overflowLiveClaims(t *testing.T, root, sessionID, prefix string) {
	t.Helper()
	if _, err := MutateSessionState(root, sessionID, func(state SessionState) SessionState {
		for index := range maxClaimEvidenceItems {
			state = AppendClaim(state, fmt.Sprintf("%s-%04d", prefix, index))
		}
		return state
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(root, sessionID, func(state SessionState) SessionState {
		return AppendClaim(state, prefix+"-overflow")
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLongSessionCompactsEvidenceAtTheSegmentCeiling(t *testing.T) {
	_, repo := withStateRoot(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	sealRecurringEvidence(t, root, "long", maxEvidenceSegments)
	before, err := LoadSessionState(root, "long")
	if err != nil || before.EvidenceSegmentCount != maxEvidenceSegments {
		t.Fatalf("fixture did not reach the ceiling: %+v %v", before, err)
	}
	// One more field overflow at the ceiling must compact, not taint.
	overflowLiveClaims(t, root, "long", "claim")
	after, err := LoadSessionState(root, "long")
	if err != nil {
		t.Fatal(err)
	}
	status, err := ReadEvidenceTaintStatus(root)
	if err != nil || status.Present {
		t.Fatalf("a recurring long session tainted the repository: %+v %v", status, err)
	}
	if after.EvidenceSegmentBase != maxEvidenceSegments+1 || after.EvidenceSegmentCount == 0 || after.EvidenceSegmentCount >= maxEvidenceSegments {
		t.Fatalf("chain was not compacted after the old one: base=%d count=%d", after.EvidenceSegmentBase, after.EvidenceSegmentCount)
	}
	complete, err := loadCompleteSessionEvidence(root, after)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"src/shared.go", "src/round-0.go", "src/round-1.go", "src/round-2.go"} {
		if !containsString(complete.ReadPaths, want) {
			t.Fatalf("compaction lost read evidence %q: %v", want, complete.ReadPaths)
		}
	}
	if !containsString(complete.Commands, "go test ./...") || !containsString(complete.Claims, "reviewed") ||
		!containsString(complete.Claims, "claim-0000") || !containsString(complete.Claims, "claim-overflow") {
		t.Fatalf("compaction lost command or claim evidence: commands=%v claims=%d", complete.Commands, len(complete.Claims))
	}
	entries, err := os.ReadDir(evidenceSegmentsDir(root, "long"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if index, ok := evidenceSegmentFileIndex(entry.Name()); ok && index < after.EvidenceSegmentBase {
			t.Fatalf("superseded segment %s was not reclaimed after publication", entry.Name())
		}
	}
}

func TestCompactedChainPreservesMergedEvidence(t *testing.T) {
	_, repo := withStateRoot(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	long := "echo " + strings.Repeat("payload", maxCommandBytes/4)
	exit := 3
	for index := range 6 {
		if _, err := MutateSessionState(root, "mixed", func(state SessionState) SessionState {
			state = AppendReadPath(state, fmt.Sprintf("docs/%d.md", index%2))
			state = AppendWritePath(state, fmt.Sprintf("src/%d.go", index))
			state = AppendCommand(state, long)
			state = AppendClaim(state, fmt.Sprintf("claim-%d", index%2))
			state.CommandResults = append(state.CommandResults, CommandResult{
				Command: "go test ./...", Outcome: "failure", EvidenceEpoch: state.EvidenceEpoch,
				ToolUseID: fmt.Sprintf("call-%d", index), ExitCode: &exit,
			})
			return state
		}); err != nil {
			t.Fatal(err)
		}
		if err := withSessionLock(root, "mixed", func() error {
			current, err := loadSessionStateResolved(root, "mixed")
			if err != nil {
				return err
			}
			rotated, err := rotateSessionEvidenceLocked(root, current)
			if err != nil {
				return err
			}
			return saveSessionStateLocked(rotated)
		}); err != nil {
			t.Fatal(err)
		}
	}
	original, err := LoadSessionState(root, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	want, err := loadCompleteSessionEvidence(root, original)
	if err != nil {
		t.Fatal(err)
	}
	var compacted SessionState
	if err := withSessionLock(root, "mixed", func() error {
		compacted, err = compactSessionEvidenceLocked(root, original)
		if err != nil {
			return err
		}
		return saveSessionStateLocked(compacted)
	}); err != nil {
		t.Fatal(err)
	}
	got, err := loadCompleteSessionEvidence(root, compacted)
	if err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2][]string{
		"reads":    {want.ReadPaths, got.ReadPaths},
		"writes":   {want.WritePaths, got.WritePaths},
		"commands": {want.Commands, got.Commands},
		"claims":   {want.Claims, got.Claims},
	} {
		if !reflect.DeepEqual(sortedCopy(pair[0]), sortedCopy(pair[1])) {
			t.Fatalf("%s changed through compaction:\nwant %v\ngot  %v", name, pair[0], pair[1])
		}
	}
	if !reflect.DeepEqual(want.WriteEpochs, got.WriteEpochs) || !reflect.DeepEqual(want.CommandResults, got.CommandResults) {
		t.Fatalf("write epochs or ordered command results changed:\nwant %v %v\ngot  %v %v",
			want.WriteEpochs, want.CommandResults, got.WriteEpochs, got.CommandResults)
	}
	if !containsString(got.Commands, long) {
		t.Fatal("the long command lost its full text through compaction")
	}
}

func TestInterruptedCompactionKeepsTheOldChainAuthoritative(t *testing.T) {
	_, repo := withStateRoot(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	sealRecurringEvidence(t, root, "interrupted", maxEvidenceSegments)
	old, err := LoadSessionState(root, "interrupted")
	if err != nil {
		t.Fatal(err)
	}
	// The compacted files are written, but the state is never published.
	if err := withSessionLock(root, "interrupted", func() error {
		_, err := compactSessionEvidenceLocked(root, old)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCompleteSessionEvidence(root, old); err != nil {
		t.Fatalf("unpublished compaction broke the authoritative chain: %v", err)
	}
	// A later compaction with different live evidence reuses those indices.
	overflowLiveClaims(t, root, "interrupted", "retry")
	after, err := LoadSessionState(root, "interrupted")
	if err != nil {
		t.Fatal(err)
	}
	if status, err := ReadEvidenceTaintStatus(root); err != nil || status.Present {
		t.Fatalf("retry after an interrupted compaction tainted the repository: %+v %v", status, err)
	}
	if _, err := loadCompleteSessionEvidence(root, after); err != nil {
		t.Fatalf("published compacted chain does not load: %v", err)
	}
	staleCopy := filepath.Join(evidenceSegmentsDir(root, "interrupted"), "00000001.json")
	if _, err := os.Stat(staleCopy); !os.IsNotExist(err) {
		t.Fatalf("superseded chain was not reclaimed: %v", err)
	}
}

func sortedCopy(values []string) []string {
	copied := append([]string{}, values...)
	sort.Strings(copied)
	return copied
}
