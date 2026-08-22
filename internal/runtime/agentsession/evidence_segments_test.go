package agentsession

import (
	"encoding/json"
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
	commands := make([]string, 400)
	boundary := 0
	bytes := 0
	for index := 0; index < 400; index++ {
		commands[index] = fmt.Sprintf("%04d-%s", index, strings.Repeat("x", 500))
		if bytes+len(commands[index]) <= maxCommandEvidenceBytes {
			bytes += len(commands[index])
			boundary = index + 1
		}
	}
	appendCommandValues(t, repo, "byte-rotation", commands[:boundary])
	appendCommandValues(t, repo, "byte-rotation", commands[boundary:])
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
	appendCommandRange(t, repo, "item-rotation", 0, maxCommandEvidenceItems)
	appendCommandRange(t, repo, "item-rotation", maxCommandEvidenceItems, 8)
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

func TestEvidenceSegmentChainLinksMultipleSegments(t *testing.T) {
	_, repo := withStateRoot(t)
	const sessionID = "multi-segment"
	if _, err := InitializeSessionState(repo, sessionID); err != nil {
		t.Fatal(err)
	}
	commandCount := 2*maxCommandEvidenceItems + 17
	appendCommandRange(t, repo, sessionID, 0, maxCommandEvidenceItems)
	appendCommandRange(t, repo, sessionID, maxCommandEvidenceItems, maxCommandEvidenceItems)
	appendCommandRange(t, repo, sessionID, 2*maxCommandEvidenceItems, 17)
	state, err := LoadSessionState(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.EvidenceSegmentCount != 2 {
		t.Fatalf("segment count = %d, want 2", state.EvidenceSegmentCount)
	}
	first, err := readEvidenceSegment(state.RepoRoot, sessionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := readEvidenceSegment(state.RepoRoot, sessionID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if second.PreviousDigest != first.Digest || state.EvidenceSegmentDigest != second.Digest {
		t.Fatalf("segment chain is not linked: first=%s second.previous=%s head=%s second=%s",
			first.Digest, second.PreviousDigest, state.EvidenceSegmentDigest, second.Digest)
	}
	complete, err := loadCompleteSessionEvidence(state.RepoRoot, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(complete.Commands) != commandCount {
		t.Fatalf("complete command count = %d, want %d", len(complete.Commands), commandCount)
	}
}

func TestEvidenceSegmentChainRejectsRedigestedBrokenLink(t *testing.T) {
	_, repo := withStateRoot(t)
	const sessionID = "broken-link"
	if _, err := InitializeSessionState(repo, sessionID); err != nil {
		t.Fatal(err)
	}
	appendCommandRange(t, repo, sessionID, 0, maxCommandEvidenceItems)
	appendCommandRange(t, repo, sessionID, maxCommandEvidenceItems, maxCommandEvidenceItems)
	appendCommandRange(t, repo, sessionID, 2*maxCommandEvidenceItems, 1)
	state, err := LoadSessionState(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := readEvidenceSegment(state.RepoRoot, sessionID, 2)
	if err != nil {
		t.Fatal(err)
	}
	second.PreviousDigest = strings.Repeat("0", 64)
	second.Digest, err = evidenceSegmentDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.MarshalIndent(second, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, '\n')
	if err := os.WriteFile(evidenceSegmentPath(state.RepoRoot, sessionID, 2), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCompleteSessionEvidence(state.RepoRoot, state); err == nil ||
		!strings.Contains(err.Error(), "previous digest mismatch") {
		t.Fatalf("re-digested broken link was accepted: %v", err)
	}
	taint, err := loadEvidenceTaint(state.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if taint == nil || taint.Field != "evidence_segments" || taint.Limit != "chain_integrity" {
		t.Fatalf("broken link did not persist chain-integrity taint: %+v", taint)
	}
}

func TestStopPolicyConsumesWriteEvidenceFromSealedSegment(t *testing.T) {
	repo := setupPolicyRepo(t)
	const sessionID = "sealed-policy-evidence"
	if _, err := InitializeSessionState(repo, sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, sessionID, func(state SessionState) SessionState {
		return AppendWritePath(state, "src/a.go")
	}); err != nil {
		t.Fatal(err)
	}
	appendCommandRange(t, repo, sessionID, 0, maxCommandEvidenceItems)
	appendCommandRange(t, repo, sessionID, maxCommandEvidenceItems, 1)
	live, err := LoadSessionState(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if live.EvidenceSegmentCount != 1 || len(live.WritePaths) != 0 {
		t.Fatalf("write evidence was not isolated in the sealed segment: %+v", live)
	}
	result := RunStop(repo, []byte(`{"session_id":"sealed-policy-evidence"}`))
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, `"decision":"block"`) ||
		!strings.Contains(result.Stdout, "require-ci-green") {
		t.Fatalf("Stop policy ignored sealed write evidence: %+v", result)
	}
}

func TestCommandResultIdentityPreservesNullableValues(t *testing.T) {
	zero := 0
	zeroAgain := 0
	falseValue := false
	falseAgain := false
	base := CommandResult{Command: "go test ./...", Outcome: "success"}
	if commandResultsEqual(base, CommandResult{Command: base.Command, Outcome: base.Outcome, ExitCode: &zero}) {
		t.Fatal("nil exit code compared equal to zero")
	}
	if !commandResultsEqual(
		CommandResult{Command: base.Command, Outcome: base.Outcome, ExitCode: &zero},
		CommandResult{Command: base.Command, Outcome: base.Outcome, ExitCode: &zeroAgain},
	) {
		t.Fatal("equal exit-code values at different addresses compared unequal")
	}
	if commandResultsEqual(base, CommandResult{Command: base.Command, Outcome: base.Outcome, IsInterrupt: &falseValue}) {
		t.Fatal("nil interrupt marker compared equal to false")
	}
	if !commandResultsEqual(
		CommandResult{Command: base.Command, Outcome: base.Outcome, IsInterrupt: &falseValue},
		CommandResult{Command: base.Command, Outcome: base.Outcome, IsInterrupt: &falseAgain},
	) {
		t.Fatal("equal interrupt values at different addresses compared unequal")
	}
}

func appendCommandRange(t *testing.T, repo, sessionID string, start, count int) {
	t.Helper()
	commands := make([]string, count)
	for index := range commands {
		commands[index] = fmt.Sprintf("command-%04d", start+index)
	}
	appendCommandValues(t, repo, sessionID, commands)
}

func appendCommandValues(t *testing.T, repo, sessionID string, commands []string) {
	t.Helper()
	if _, err := MutateSessionState(repo, sessionID, func(state SessionState) SessionState {
		for _, command := range commands {
			state = AppendCommand(state, command)
		}
		return state
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceSegmentChainRejectsTampering(t *testing.T) {
	_, repo := withStateRoot(t)
	if _, err := InitializeSessionState(repo, "tamper"); err != nil {
		t.Fatal(err)
	}
	commands := make([]string, maxCommandEvidenceItems+1)
	for index := range commands {
		commands[index] = fmt.Sprintf("command-%04d-%s", index, strings.Repeat("x", 20))
	}
	appendCommandValues(t, repo, "tamper", commands[:maxCommandEvidenceItems])
	appendCommandValues(t, repo, "tamper", commands[maxCommandEvidenceItems:])
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
	appendCommandRange(t, repo, "clean-segments", 0, maxCommandEvidenceItems)
	appendCommandRange(t, repo, "clean-segments", maxCommandEvidenceItems, 1)
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

func TestEvidenceTaintResolutionRequiresEndedSessionAndExactToken(t *testing.T) {
	_, repo := withStateRoot(t)
	if _, err := InitializeSessionState(repo, "resolve-origin"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "resolve-origin", func(state SessionState) SessionState {
		markEvidenceOverflowWithLimit(&state, "commands", "byte_budget")
		return state
	}); err != nil {
		t.Fatal(err)
	}
	status, err := ReadEvidenceTaintStatus(repo)
	if err != nil || !status.Present || status.Token == "" {
		t.Fatalf("taint status: %+v err=%v", status, err)
	}
	if _, err := ResolveEvidenceTaint(repo, status.Token, "recover"); err == nil ||
		!strings.Contains(err.Error(), "must end") {
		t.Fatalf("active session resolution was accepted: %v", err)
	}
	if result := RunSessionEnd(repo, []byte(`{"session_id":"resolve-origin"}`)); result.ExitCode != 0 {
		t.Fatalf("session end: %+v", result)
	}
	if _, err := ResolveEvidenceTaint(repo, strings.Repeat("0", 64), "recover"); err == nil ||
		!strings.Contains(err.Error(), "token changed") {
		t.Fatalf("wrong-token resolution was accepted: %v", err)
	}
	resolved, err := ResolveEvidenceTaint(repo, status.Token, "abandon incomplete epoch and reproduce evidence")
	if err != nil || resolved.Token != status.Token {
		t.Fatalf("resolve taint: %+v err=%v", resolved, err)
	}
	if current, err := ReadEvidenceTaintStatus(repo); err != nil || current.Present {
		t.Fatalf("resolved taint remained active: %+v err=%v", current, err)
	}
	root, _ := ResolveRepoRoot(repo)
	if _, err := os.Stat(evidenceTaintResolutionPath(root, status.Token)); err != nil {
		t.Fatalf("resolution receipt missing: %v", err)
	}
	successor, err := InitializeSessionState(repo, "resolve-successor")
	if err != nil || successor.EvidenceOverflow {
		t.Fatalf("fresh evidence window remained tainted: %+v err=%v", successor, err)
	}
}
