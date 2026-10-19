package agentsession

import (
	"strings"
	"testing"
	"time"
)

func TestPreDecisionReadsTheOperatorPauseOncePerCall(t *testing.T) {
	repo := setupPolicyRepo(t)
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	previous := readHookPauseForHook
	t.Cleanup(func() { readHookPauseForHook = previous })
	readHookPauseForHook = func(root string) (hookPauseFile, error) {
		reads++
		return previous(root)
	}
	write := []byte(`{"session_id":"pause-once","tool_name":"Write","tool_input":{"file_path":"generated/out.txt"}}`)
	if result := RunHookRequest(root, HookHandlerPreToolUse, "claude-code", write); result.ExitCode != 2 {
		t.Fatalf("denied write was not blocked: %+v", result)
	}
	if reads != 1 {
		t.Fatalf("one pre-tool decision read the pause %d times, want once", reads)
	}
	if _, err := PauseHooks(repo, time.Hour, "maintenance", time.Now()); err != nil {
		t.Fatal(err)
	}
	paused := RunHookRequest(root, HookHandlerPreToolUse, "claude-code", write)
	if paused.ExitCode != 0 || !strings.Contains(paused.Stderr, "maintenance") {
		t.Fatalf("an active pause must still lift the policy gate: %+v", paused)
	}
}
