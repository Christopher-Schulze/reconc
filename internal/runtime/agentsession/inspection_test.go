package agentsession

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeInspectionState(t *testing.T, state SessionState) string {
	t.Helper()
	body, err := marshalStateDeterministic(state)
	if err != nil {
		t.Fatalf("marshal inspection state: %v", err)
	}
	path := sessionStatePath(state.RepoRoot, state.SessionID)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write inspection state: %v", err)
	}
	return path
}

func TestInspectSessionStateMissingDoesNotCreatePrivateState(t *testing.T) {
	stateRoot, repo := withStateRoot(t)
	state, err := InspectSessionState(repo, "missing-inspection")
	if err != nil {
		t.Fatalf("InspectSessionState: %v", err)
	}
	if state.SessionID != "missing-inspection" || state.EvidenceOverflow {
		t.Fatalf("unexpected missing state: %+v", state)
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "projects")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspection created private state directories: %v", err)
	}
}

func TestInspectSessionStateReportsOverflowWithoutPersistingTaint(t *testing.T) {
	_, repo := withStateRoot(t)
	state, err := InitializeSessionState(repo, "overflow-inspection")
	if err != nil {
		t.Fatal(err)
	}
	state.EvidenceOverflow = true
	state.EvidenceOverflowReason = "commands"
	state.EvidenceOverflowLimit = "item_bytes"
	statePath := writeInspectionState(t, state)
	taintPath := evidenceTaintPath(state.RepoRoot)
	if err := os.Remove(taintPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}

	inspected, err := InspectSessionState(repo, state.SessionID)
	if err != nil {
		t.Fatalf("InspectSessionState: %v", err)
	}
	if !inspected.EvidenceOverflow || inspected.EvidenceOverflowReason != "commands" || inspected.EvidenceOverflowLimit != "item_bytes" {
		t.Fatalf("overflow was not reported: %+v", inspected)
	}
	if _, err := os.Stat(taintPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspection persisted evidence taint: %v", err)
	}

	if _, err := LoadSessionState(repo, state.SessionID); err != nil {
		t.Fatalf("LoadSessionState enforcement path: %v", err)
	}
	if _, err := os.Stat(taintPath); err != nil {
		t.Fatalf("enforcement path did not persist evidence taint: %v", err)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file disappeared: %v", err)
	}
}

func TestInspectSessionStateRejectsMalformedWithoutCreatingLock(t *testing.T) {
	_, repo := withStateRoot(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "malformed-inspection"
	path := sessionStatePath(root, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	lockPath := sessionLockPath(root, sessionID)
	if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := InspectSessionState(repo, sessionID); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("malformed state error = %v", err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspection created a session lock: %v", err)
	}
}

func TestInspectActiveSessionStateDoesNotCreateLocksOrTaint(t *testing.T) {
	stateRoot, repo := withStateRoot(t)
	state, err := InitializeSessionState(repo, "active-inspection")
	if err != nil {
		t.Fatal(err)
	}
	state.EvidenceOverflow = true
	state.EvidenceOverflowReason = "commands"
	state.EvidenceOverflowLimit = "byte_budget"
	writeInspectionState(t, state)
	if err := os.Remove(evidenceTaintPath(state.RepoRoot)); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, path := range []string{sessionLockPath(state.RepoRoot, state.SessionID), activeSessionLockPath(state.RepoRoot)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}

	active, inspected, err := InspectActiveSessionState(repo)
	if err != nil {
		t.Fatalf("InspectActiveSessionState: %v", err)
	}
	if active != state.SessionID || !inspected.EvidenceOverflow {
		t.Fatalf("unexpected active inspection: %q %+v", active, inspected)
	}
	for _, path := range []string{sessionLockPath(state.RepoRoot, state.SessionID), activeSessionLockPath(state.RepoRoot), evidenceTaintPath(state.RepoRoot)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspection created %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "projects")); err != nil {
		t.Fatalf("expected existing state root fixture: %v", err)
	}
}

func TestInspectActiveSessionStateBoundsConcurrentReplacement(t *testing.T) {
	_, repo := withStateRoot(t)
	state, err := InitializeSessionState(repo, "replace-inspection")
	if err != nil {
		t.Fatal(err)
	}
	statePath := writeInspectionState(t, state)
	first := state
	first.ReadPaths = []string{"first"}
	second := state
	second.ReadPaths = []string{"second"}
	firstBody, err := marshalStateDeterministic(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBody, err := marshalStateDeterministic(second)
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for index := 0; index < 200; index++ {
			select {
			case <-stop:
				return
			default:
			}
			body := firstBody
			if index%2 == 1 {
				body = secondBody
			}
			tmp := statePath + ".replace"
			if os.WriteFile(tmp, body, 0o600) != nil || os.Rename(tmp, statePath) != nil {
				return
			}
		}
	}()

	result := make(chan error, 1)
	go func() {
		_, inspected, inspectErr := InspectActiveSessionState(repo)
		if inspectErr == nil && len(inspected.ReadPaths) > 0 && inspected.ReadPaths[0] != "first" && inspected.ReadPaths[0] != "second" {
			inspectErr = errors.New("inspection returned an inconsistent snapshot")
		}
		result <- inspectErr
	}()
	select {
	case inspectErr := <-result:
		if inspectErr != nil && !strings.Contains(inspectErr.Error(), "changed") && !strings.Contains(inspectErr.Error(), "read session state") {
			t.Fatalf("unexpected concurrent inspection error: %v", inspectErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent inspection did not terminate within its bounded window")
	}
	close(stop)
	writer.Wait()
	_ = os.Remove(statePath + ".replace")
}

func TestInspectSessionStateRejectsOversizedInputWithoutLock(t *testing.T) {
	_, repo := withStateRoot(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "oversized-inspection"
	path := sessionStatePath(root, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxLegacySessionStateBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectSessionState(repo, sessionID); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized state error = %v", err)
	}
	if _, err := os.Stat(sessionLockPath(root, sessionID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized inspection created a session lock: %v", err)
	}
}
