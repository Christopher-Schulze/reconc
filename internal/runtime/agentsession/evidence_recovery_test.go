package agentsession

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func damagedCommandRecoveryFixture(t *testing.T, rotated bool) (nativeApprovalFixture, SessionState, string) {
	t.Helper()
	fixture := declaredCommandFixture(t, "rules: []\n")
	root, err := ResolveRepoRoot(fixture.repo)
	if err != nil {
		t.Fatal(err)
	}
	fixture.repo = root
	count := 1
	if rotated {
		count = 6
	}
	for index := range count {
		command := strings.Repeat(string(rune('a'+index)), maxCommandBytes+100)
		appendCommandValues(t, fixture.repo, fixture.sessionID, []string{command})
	}
	state, err := loadSessionStateWithLockResolved(fixture.repo, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := commandTextDigest(boundCommandText(strings.Repeat("a", maxCommandBytes+100)))
	object := commandTextPath(state, digest)
	damageCommandText(t, object, "changed")
	if _, err := loadCompleteSessionEvidence(fixture.repo, state); err == nil {
		t.Fatal("damaged command evidence was accepted")
	}
	return fixture, state, object
}

func TestEvidenceRecoveryCannotEraseANewConcurrentFailure(t *testing.T) {
	_, repo := withStateRoot(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(root, "original", func(state SessionState) SessionState {
		markEvidenceOverflowWithLimit(&state, "claims", "item_bytes")
		return state
	}); err != nil {
		t.Fatal(err)
	}
	old, err := ReadEvidenceTaintStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	entered, finished := make(chan struct{}), make(chan error, 1)
	err = withEvidenceTaintLock(root, func() error {
		go func() {
			close(entered)
			finished <- withSessionLock(root, "new-failure", func() error {
				state := emptyState(root, "new-failure")
				markEvidenceOverflowWithLimit(&state, "commands", "item_bytes")
				return persistEvidenceTaint(root, state)
			})
		}()
		<-entered
		select {
		case err := <-finished:
			return errors.Join(errors.New("concurrent failure bypassed the held taint lease"), err)
		case <-time.After(30 * time.Millisecond):
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent writer did not finish after releasing its lease")
	}
	if _, err := ResolveEvidenceTaint(root, old.Token, "stale review"); err == nil {
		t.Fatal("old review erased the concurrent failure")
	}
	current, err := ReadEvidenceTaintStatus(root)
	if err != nil || !current.Present || current.SessionID != "new-failure" {
		t.Fatalf("new failure disappeared: %+v %v", current, err)
	}
}

func TestEvidenceTaintPublicationWaitsForGlobalRetention(t *testing.T) {
	_, repo := withStateRoot(t)
	state, err := InitializeSessionState(repo, "existing-private-boundary")
	if err != nil {
		t.Fatal(err)
	}
	markEvidenceOverflowWithLimit(&state, "claims", "item_bytes")
	finished := make(chan error, 1)
	err = withProjectStatePublicationLock(stateRoot(), func() error {
		go func() { finished <- persistEvidenceTaint(state.RepoRoot, state) }()
		select {
		case result := <-finished:
			return errors.Join(errors.New("taint publication bypassed global retention"), result)
		case <-time.After(50 * time.Millisecond):
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("taint publication did not resume after global retention")
	}
}

func TestEvidenceTaintContentionDoesNotHoldGlobalRetention(t *testing.T) {
	_, repo := withStateRoot(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	err = withEvidenceTaintLock(root, func() error {
		go func() {
			release, contended, err := tryEvidenceTaintLock(root)
			if release != nil {
				err = errors.Join(err, release())
			}
			if !contended {
				err = errors.Join(err, errors.New("held evidence lock was not reported as contended"))
			}
			err = errors.Join(err, withProjectStatePublicationLock(stateRoot(), func() error { return nil }))
			finished <- err
		}()
		select {
		case err := <-finished:
			return err
		case <-time.After(time.Second):
			return errors.New("evidence contention blocked global retention")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceRecoveryReclaimsBrokenWindowWithoutEndingHostSession(t *testing.T) {
	fixture, state, object := damagedCommandRecoveryFixture(t, true)
	status, err := ReadEvidenceTaintStatus(fixture.repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveEvidenceTaint(fixture.repo, status.Token, "abandon damaged evidence and reproduce it"); err != nil {
		t.Fatal(err)
	}
	reset, err := LoadSessionState(fixture.repo, fixture.sessionID)
	if err != nil || reset.EvidenceOverflow || len(reset.Commands) != 0 || reset.EvidenceSegmentCount != 0 || reset.SessionID != fixture.sessionID {
		t.Fatalf("recovery did not establish an empty window in the same session: %+v %v", reset, err)
	}
	for _, path := range []string{object, evidenceSegmentsDir(fixture.repo, state.SessionID)} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("abandoned storage was not reclaimed: %v", err)
		}
	}
	command := "echo " + strings.Repeat("new", maxCommandBytes)
	if _, err := MutateSessionState(fixture.repo, fixture.sessionID, func(current SessionState) SessionState {
		return AppendCommand(current, command)
	}); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(struct {
		SessionID string            `json:"session_id"`
		ToolName  string            `json:"tool_name"`
		ToolInput map[string]string `json:"tool_input"`
	}{fixture.sessionID, "Bash", map[string]string{"command": "ls"}})
	if err != nil {
		t.Fatal(err)
	}
	if result := RunPreToolUse(fixture.repo, payload); result.ExitCode != 0 {
		t.Fatalf("host session could not continue after recovery: %+v", result)
	}
}

func TestEvidenceRecoveryPreservesUnknownFilesAndReceiptAcrossRetry(t *testing.T) {
	fixture, state, _ := damagedCommandRecoveryFixture(t, true)
	foreign := filepath.Join(evidenceSegmentsDir(fixture.repo, state.SessionID), "user-owned.txt")
	if err := os.WriteFile(foreign, []byte("preserve me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := ReadEvidenceTaintStatus(fixture.repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveEvidenceTaint(fixture.repo, status.Token, "first review"); err == nil || !strings.Contains(err.Error(), "unknown entry") {
		t.Fatalf("unknown evidence file was not preserved: %v", err)
	}
	if current, err := ReadEvidenceTaintStatus(fixture.repo); err != nil || !current.Present || current.Token != status.Token {
		t.Fatalf("failed recovery lost the taint: %+v %v", current, err)
	}
	if err := os.Rename(foreign, filepath.Join(fixture.repo, "preserved-user-file.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveEvidenceTaint(fixture.repo, status.Token, "retry after reviewing the foreign file"); err != nil {
		t.Fatal(err)
	}
	resolution, present, err := readEvidenceResolution(state.RepoRoot, status.Token)
	if err != nil || !present || resolution.Reason != "first review" {
		t.Fatalf("retry replaced the immutable original resolution: %+v %v", resolution, err)
	}
}

func TestEvidenceRecoveryDoesNotResurrectPropagatedTaint(t *testing.T) {
	_, repo := withStateRoot(t)
	if _, err := MutateSessionState(repo, "origin", func(state SessionState) SessionState {
		markEvidenceOverflowWithLimit(&state, "claims", "item_bytes")
		return state
	}); err != nil {
		t.Fatal(err)
	}
	original, err := ReadEvidenceTaintStatus(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"successor", "active"} {
		if _, err := InitializeSessionState(repo, id); err != nil {
			t.Fatal(err)
		}
		if _, err := MutateSessionState(repo, id, func(state SessionState) SessionState { return AppendReadPath(state, "read.md") }); err != nil {
			t.Fatal(err)
		}
	}
	if status, err := ReadEvidenceTaintStatus(repo); err != nil || status.Token != original.Token || status.SessionID != "origin" {
		t.Fatalf("propagated flags replaced their origin: %+v %v", status, err)
	}
	if _, err := ResolveEvidenceTaint(repo, original.Token, "reviewed original gap"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"origin", "successor", "active"} {
		state, err := LoadSessionState(repo, id)
		if err != nil || state.EvidenceOverflow {
			t.Fatalf("resolved flags re-sealed %s: %+v %v", id, state, err)
		}
	}
	if _, err := MutateSessionState(repo, "successor", func(state SessionState) SessionState {
		markEvidenceOverflowWithLimit(&state, "claims", "item_bytes")
		return state
	}); err != nil {
		t.Fatal(err)
	}
	status, err := ReadEvidenceTaintStatus(repo)
	if err != nil || !status.Present || status.SessionID != "successor" || status.Token == original.Token {
		t.Fatalf("a new independent gap was hidden by the old receipt: %+v %v", status, err)
	}
}
