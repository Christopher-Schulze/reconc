package agentsession

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestEvidenceRotationPreservesByteBoundedCommands(t *testing.T) {
	_, repo := withStateRoot(t)
	if _, err := InitializeSessionState(repo, "byte-rotation"); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 400; index++ {
		command := fmt.Sprintf("%04d-%s", index, strings.Repeat("x", 500))
		if _, err := MutateSessionState(repo, "byte-rotation", func(state SessionState) SessionState {
			return AppendCommand(state, command)
		}); err != nil {
			t.Fatal(err)
		}
	}
	state, err := LoadSessionState(repo, "byte-rotation")
	if err != nil {
		t.Fatal(err)
	}
	if state.EvidenceOverflow || state.EvidenceSegmentCount == 0 || state.EvidenceSegmentDigest == "" {
		t.Fatalf("byte rotation did not seal a clean segment: %+v", state)
	}
	complete, err := loadCompleteSessionEvidence(state.RepoRoot, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(complete.Commands) != 400 {
		t.Fatalf("complete command count = %d, want 400", len(complete.Commands))
	}
}

func TestEvidenceRotationPreservesItemBoundedCommands(t *testing.T) {
	_, repo := withStateRoot(t)
	if _, err := InitializeSessionState(repo, "item-rotation"); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxCommandEvidenceItems+8; index++ {
		command := fmt.Sprintf("command-%04d", index)
		if _, err := MutateSessionState(repo, "item-rotation", func(state SessionState) SessionState {
			return AppendCommand(state, command)
		}); err != nil {
			t.Fatal(err)
		}
	}
	state, err := LoadSessionState(repo, "item-rotation")
	if err != nil {
		t.Fatal(err)
	}
	complete, err := loadCompleteSessionEvidence(state.RepoRoot, state)
	if err != nil {
		t.Fatal(err)
	}
	if state.EvidenceOverflow || state.EvidenceSegmentCount == 0 || len(complete.Commands) != maxCommandEvidenceItems+8 {
		t.Fatalf("item rotation lost evidence: live=%+v complete=%d", state, len(complete.Commands))
	}
}

func TestEvidenceSegmentChainRejectsTampering(t *testing.T) {
	_, repo := withStateRoot(t)
	if _, err := InitializeSessionState(repo, "tamper"); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxCommandEvidenceItems+1; index++ {
		command := fmt.Sprintf("command-%04d-%s", index, strings.Repeat("x", 20))
		if _, err := MutateSessionState(repo, "tamper", func(state SessionState) SessionState {
			return AppendCommand(state, command)
		}); err != nil {
			t.Fatal(err)
		}
	}
	state, err := LoadSessionState(repo, "tamper")
	if err != nil {
		t.Fatal(err)
	}
	path := evidenceSegmentPath(state.RepoRoot, state.SessionID, 1)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body[len(body)/2] ^= 1
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCompleteSessionEvidence(state.RepoRoot, state); err == nil {
		t.Fatal("tampered evidence segment was accepted")
	}
	taint, err := loadEvidenceTaint(state.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if taint == nil || taint.Field != "evidence_segments" || taint.Limit != "chain_integrity" {
		t.Fatalf("chain tampering did not persist taint: %+v", taint)
	}
}

func TestSessionEndRemovesOnlyVerifiedCleanSegments(t *testing.T) {
	_, repo := withStateRoot(t)
	if _, err := InitializeSessionState(repo, "clean-segments"); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxCommandEvidenceItems+1; index++ {
		command := fmt.Sprintf("command-%04d", index)
		if _, err := MutateSessionState(repo, "clean-segments", func(state SessionState) SessionState {
			return AppendCommand(state, command)
		}); err != nil {
			t.Fatal(err)
		}
	}
	state, err := LoadSessionState(repo, "clean-segments")
	if err != nil {
		t.Fatal(err)
	}
	segmentsDir := evidenceSegmentsDir(state.RepoRoot, state.SessionID)
	if _, err := os.Stat(segmentsDir); err != nil {
		t.Fatal(err)
	}
	if err := CleanupSessionState(repo, "clean-segments"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(segmentsDir); !os.IsNotExist(err) {
		t.Fatalf("clean evidence segments survived SessionEnd: %v", err)
	}
}

func TestTrueOverflowPersistsAcrossReloadAndSuccessorSession(t *testing.T) {
	_, repo := withStateRoot(t)
	if _, err := InitializeSessionState(repo, "origin"); err != nil {
		t.Fatal(err)
	}
	oversized := strings.Repeat("x", maxCommandBytes+1)
	state, err := MutateSessionState(repo, "origin", func(state SessionState) SessionState {
		return AppendCommand(state, oversized)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.EvidenceOverflow || state.EvidenceOverflowReason != "commands" || state.EvidenceOverflowLimit != "item_bytes" {
		t.Fatalf("overflow cause was not exact: %+v", state)
	}
	reloaded, err := LoadSessionState(repo, "origin")
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.EvidenceOverflow || reloaded.EvidenceOverflowLimit != "item_bytes" {
		t.Fatalf("overflow did not survive reload: %+v", reloaded)
	}
	if result := RunSessionEnd(repo, []byte(`{"session_id":"origin"}`)); result.ExitCode != 0 {
		t.Fatalf("session end: %+v", result)
	}
	successor, err := InitializeSessionState(repo, "successor")
	if err != nil {
		t.Fatal(err)
	}
	if !successor.EvidenceOverflow || successor.EvidenceOverflowReason != "commands" ||
		successor.EvidenceOverflowLimit != "item_bytes" {
		t.Fatalf("successor did not inherit taint: %+v", successor)
	}
	completion, err := CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !completion.EvidenceOverflow || completion.EvidenceOverflowLimit != "item_bytes" {
		t.Fatalf("completion candidate lost taint: %+v", completion)
	}
	if _, err := RecordClaim(repo, "ci-green", "successor"); err == nil ||
		!strings.Contains(err.Error(), "claims") {
		t.Fatalf("claim was not rejected under taint: %v", err)
	}
}

func TestTrueOverflowBlocksMaterialToolsButAllowsReads(t *testing.T) {
	_, repo := withStateRoot(t)
	if _, err := InitializeSessionState(repo, "blocked-tools"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "blocked-tools", func(state SessionState) SessionState {
		markEvidenceOverflowWithLimit(&state, "commands", "byte_budget")
		return state
	}); err != nil {
		t.Fatal(err)
	}
	command := RunPreToolUse(repo, []byte(`{"session_id":"blocked-tools","tool_name":"Bash","tool_input":{"command":"git add -A"}}`))
	if command.ExitCode != 2 || !strings.Contains(command.Stderr, "commands exceeded byte_budget") {
		t.Fatalf("command escaped overflow gate: %+v", command)
	}
	write := RunPreToolUse(repo, []byte(`{"session_id":"blocked-tools","tool_name":"Write","tool_input":{"file_path":"x.txt"}}`))
	if write.ExitCode != 2 {
		t.Fatalf("write escaped overflow gate: %+v", write)
	}
	read := RunPreToolUse(repo, []byte(`{"session_id":"blocked-tools","tool_name":"Read","tool_input":{"file_path":"x.txt"}}`))
	if read.ExitCode != 0 {
		t.Fatalf("read was blocked under overflow: %+v", read)
	}
}
