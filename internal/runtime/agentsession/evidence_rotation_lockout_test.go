package agentsession

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func appendCommandResults(t *testing.T, repo, sessionID string, from, count int) SessionState {
	t.Helper()
	var updated SessionState
	for index := from; index < from+count; index++ {
		var err error
		updated, err = MutateSessionState(repo, sessionID, func(current SessionState) SessionState {
			return AppendCommandResult(current, CommandResult{
				Command: fmt.Sprintf("go test ./pkg/%04d 2>&1 && echo done", index), Outcome: "success", ToolUseID: fmt.Sprintf("tool-%04d", index),
			})
		})
		if err != nil {
			t.Fatalf("append result %d: %v", index, err)
		}
	}
	return updated
}

func TestRotationSetsAsideAnOrphanedSegmentInsteadOfTainting(t *testing.T) {
	_, repo := withStateRoot(t)
	const sessionID = "orphan-segment"
	state, err := InitializeSessionState(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	// A segment left at the next index by a rotation whose state was never
	// published (or by an older chain) must not block the next rotation.
	orphan := evidenceSegmentPath(state.RepoRoot, sessionID, 1)
	if err := ensurePrivateStateDir(filepath.Dir(orphan)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("{\"format_version\":\"raw-evidence-v1\",\"index\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	updated := appendCommandResults(t, repo, sessionID, 0, maxCommandResultItems+1)
	if updated.EvidenceOverflow {
		t.Fatalf("rotation over an orphaned segment tainted the session: %s/%s", updated.EvidenceOverflowReason, updated.EvidenceOverflowLimit)
	}
	if updated.EvidenceSegmentCount != 1 {
		t.Fatalf("segment count = %d, want 1", updated.EvidenceSegmentCount)
	}
	if _, err := os.Stat(evidenceTaintPath(state.RepoRoot)); !os.IsNotExist(err) {
		t.Fatalf("durable taint was written: %v", err)
	}
	set, err := filepath.Glob(orphan + ".orphaned-*")
	if err != nil || len(set) != 1 {
		t.Fatalf("orphaned segment was not preserved aside: %v %v", set, err)
	}
	complete, err := loadCompleteSessionEvidence(state.RepoRoot, updated)
	if err != nil {
		t.Fatalf("rotated chain does not load: %v", err)
	}
	if len(complete.CommandResults) != maxCommandResultItems+1 {
		t.Fatalf("complete evidence holds %d results, want %d", len(complete.CommandResults), maxCommandResultItems+1)
	}
}

func TestOversizedStateRotatesEvidenceInsteadOfTainting(t *testing.T) {
	_, repo := withStateRoot(t)
	const sessionID = "oversized-state"
	state, err := InitializeSessionState(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("d", 120)
	if _, err := MutateSessionState(repo, sessionID, func(current SessionState) SessionState {
		for index := 0; index < 1200; index++ {
			current = AppendReadPath(current, fmt.Sprintf("read/%s/%04d.go", long, index))
			current = AppendWritePath(current, fmt.Sprintf("write/%s/%04d.go", long, index))
		}
		return current
	}); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("p", 60*1024)
	var updated SessionState
	for index := 0; index < 12; index++ {
		updated, err = MutateSessionState(repo, sessionID, func(current SessionState) SessionState {
			return PutPendingToolCall(current, fmt.Sprintf("call-%02d", index), PendingToolCall{
				ToolName: "Read", ToolInput: map[string]interface{}{"payload": payload},
			})
		})
		if err != nil {
			t.Fatalf("pending call %d: %v", index, err)
		}
		if updated.EvidenceOverflow {
			t.Fatalf("state budget tainted the session at call %d instead of rotating evidence: %s/%s", index, updated.EvidenceOverflowReason, updated.EvidenceOverflowLimit)
		}
	}
	if updated.EvidenceSegmentCount == 0 {
		t.Fatal("the oversized state never sealed its evidence into a segment")
	}
	info, err := os.Stat(sessionStatePath(state.RepoRoot, sessionID))
	if err != nil || info.Size() > MaxSessionStateBytes {
		t.Fatalf("published state exceeded its budget: %v %v", info, err)
	}
}

func TestEvidenceFilesDoNotHTMLEscapeShellOperators(t *testing.T) {
	_, repo := withStateRoot(t)
	const sessionID = "shell-operators"
	state, err := InitializeSessionState(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	appendCommandResults(t, repo, sessionID, 0, 1)
	body, err := os.ReadFile(sessionStatePath(state.RepoRoot, sessionID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "2>&1 && echo done") || strings.Contains(string(body), `\u0026`) {
		t.Fatalf("state escapes shell operators: %s", body)
	}
}
