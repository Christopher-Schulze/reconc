package agentsession

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/filelock"
)

// A hook whose host budget runs out while another process holds its session
// lock reports its own block instead of being killed by the host.
func TestHookStopsWaitingAtItsHostBudget(t *testing.T) {
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

	t.Cleanup(BeginHookRequest(time.Now().Add(300 * time.Millisecond)))
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

// A policy script still running when the hook's budget ends is stopped with
// it: the hook answers within its budget and fails closed instead of letting
// the host kill it while the script runs on.
func TestHookBudgetStopsAPolicyScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the slow policy script is a POSIX shell script")
	}
	repo := setupPolicyRepo(t)
	script := filepath.Join(repo, ".reconc", "scripts", "slow.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	rules := "rules:\n  - id: slow-script\n    kind: require_script\n    when_paths: ['touched.txt']\n" +
		"    script: '.reconc/scripts/slow.sh'\n    timeout_sec: 10\n    mode: block\n    message: slow script\n"
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "budget"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "touched.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := InitializeSessionState(repo, "budget-script")
	if err != nil {
		t.Fatal(err)
	}
	state.WritePaths = []string{"touched.txt"}
	if err := SaveSessionState(state); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(BeginHookRequest(time.Now().Add(300 * time.Millisecond)))
	started := time.Now()
	result := RunHookRequest(root, HookHandlerStop, "claude-stop", []byte(`{"session_id":"budget-script"}`))
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Stop ran its policy script %s past a 300 ms budget: %+v", elapsed, result)
	}
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "policy script") || !strings.Contains(result.Stderr, "host time budget") {
		t.Fatalf("budget-exhausted Stop = %+v, want a Reconc block naming the budget and the policy script", result)
	}
}
