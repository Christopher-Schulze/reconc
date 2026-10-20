package agentsession

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/filelock"
)

// A one-shot hook whose host budget runs out while another process holds its
// session lock reports its own block instead of being killed by the host.
func TestOneShotHookStopsWaitingAtItsHostBudget(t *testing.T) {
	repo := setupPolicyRepo(t)
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	if result := RunHookRequest(root, HookHandlerSessionStart, "claude-session-start", []byte(`{"session_id":"budget"}`)); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}
	lockFile, err := os.OpenFile(sessionLockPath(root.Path(), "budget"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lockFile.Close()
	unlock, err := filelock.LockContext(context.Background(), lockFile, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	SetOneShotHookDeadline(time.Now().Add(300 * time.Millisecond))
	t.Cleanup(func() { SetOneShotHookDeadline(time.Time{}) })
	started := time.Now()
	payload := []byte(`{"session_id":"budget","tool_use_id":"call-1","tool_name":"Bash","tool_input":{"command":"go vet ./..."}}`)
	result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("pre-tool waited %s for a held session lock past its budget", elapsed)
	}
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "host time budget") {
		t.Fatalf("budget-exhausted pre-tool = %+v, want a Reconc block naming the budget", result)
	}
}
