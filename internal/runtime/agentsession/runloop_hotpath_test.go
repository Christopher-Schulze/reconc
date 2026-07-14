package agentsession

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/tasklifecycle"
)

func TestDisabledRunEventsCreateNoStateFiles(t *testing.T) {
	repo := setupPolicyRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	result := RunStop(repo, []byte(`{"session_id":"disabled","runtime":"codex"}`))
	if result.ExitCode != 0 || result.Stderr != "" {
		t.Fatalf("disabled stop: %+v", result)
	}
	for _, name := range []string{"state.json", "decisions.jsonl"} {
		if _, err := os.Stat(filepath.Join(repo, ".reconc", "runloop", name)); !os.IsNotExist(err) {
			t.Fatalf("disabled no-op events must not create %s: %v", name, err)
		}
	}
}

func TestUnchangedRunLoopStateIsNotRewritten(t *testing.T) {
	repo := t.TempDir()
	state := runLoopState{Enabled: true, Mode: runLoopModeRepo}
	if err := saveRunLoopState(repo, state); err != nil {
		t.Fatal(err)
	}
	path, err := runLoopStatePath(repo)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mutateRunLoopState(repo, func(current runLoopState) runLoopState { return current }); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		t.Fatalf("unchanged state was rewritten: before=%v/%d after=%v/%d", before.ModTime(), before.Size(), after.ModTime(), after.Size())
	}
}

func TestRunControlFailsClosedWithoutReplacingCorruptState(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".reconc", "runloop", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("{not-json\n")
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetRunLoopRepoMode(repo, true); err == nil {
		t.Fatal("run on must fail closed on corrupt state")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(corrupt) {
		t.Fatalf("corrupt state was replaced: %q", after)
	}
}

func TestRepoRunModePersistsAcrossSessionsRuntimesAndInterrupts(t *testing.T) {
	repo := t.TempDir()
	for _, runtimeName := range []string{"claude", "codex", "cursor", "opencode", "devin", "antigravity", "copilot", "kilo"} {
		if _, err := SetRunLoopRepoMode(repo, true); err != nil {
			t.Fatal(err)
		}
		sessionID := runtimeName + "-session"
		start := RunSessionStart(repo, []byte(`{"session_id":"`+sessionID+`","runtime":"`+runtimeName+`"}`))
		if start.ExitCode != 0 {
			t.Fatalf("%s session start: %+v", runtimeName, start)
		}
		interrupt := RunStop(repo, []byte(`{"session_id":"`+sessionID+`","runtime":"`+runtimeName+`","is_interrupt":true}`))
		if interrupt.ExitCode != 0 || interrupt.Stdout != "" {
			t.Fatalf("%s interrupt: %+v", runtimeName, interrupt)
		}
		end := RunSessionEnd(repo, []byte(`{"session_id":"`+sessionID+`","runtime":"`+runtimeName+`"}`))
		if end.ExitCode != 0 {
			t.Fatalf("%s session end: %+v", runtimeName, end)
		}
		state, err := loadRunLoopState(repo)
		if err != nil || !runLoopStateApplies(state) {
			t.Fatalf("%s lifecycle changed repository run state: %+v err=%v", runtimeName, state, err)
		}
	}
}

func TestRepoRunModeSkipsStopPolicyOnlyForExecutableTask(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 42, "terminal gate")
	writeTaskFixture(t, repo)
	if _, err := SetRunLoopRepoMode(repo, true); err != nil {
		t.Fatal(err)
	}
	if _, err := InitializeSessionState(repo, "repo-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "repo-run", func(state SessionState) SessionState {
		return AppendWritePath(state, "src/a.go")
	}); err != nil {
		t.Fatal(err)
	}
	result := RunStop(repo, []byte(`{"session_id":"repo-run","runtime":"codex"}`))
	if result.ExitCode != 0 || !containsRunLoopBlock(result.Stdout) {
		t.Fatalf("executable repo run did not continue: %+v", result)
	}
	if got := readCounter(t, counterPath); got != 0 {
		t.Fatalf("routine repo continuation ran terminal policy %d time(s)", got)
	}

	terminalRepo := setupStopScriptPolicyRepo(t, counterPath, 42, "terminal gate")
	if _, err := SetRunLoopRepoMode(terminalRepo, true); err != nil {
		t.Fatal(err)
	}
	if _, err := InitializeSessionState(terminalRepo, "terminal"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(terminalRepo, "terminal", func(state SessionState) SessionState {
		return AppendWritePath(state, "src/a.go")
	}); err != nil {
		t.Fatal(err)
	}
	result = RunStop(terminalRepo, []byte(`{"session_id":"terminal","runtime":"codex"}`))
	if result.ExitCode != 0 || containsRunLoopBlock(result.Stdout) || result.Stdout == "" {
		t.Fatalf("terminal repo run did not retain policy gate: %+v", result)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("terminal Stop policy ran %d time(s), want 1", got)
	}
}

func TestRepoRunExecutableStopDoesNotPublishEmptySessionState(t *testing.T) {
	repo := setupPolicyRepo(t)
	writeTaskFixture(t, repo)
	if _, err := SetRunLoopRepoMode(repo, true); err != nil {
		t.Fatal(err)
	}
	result := RunStop(repo, []byte(`{"session_id":"repo-fastpath","runtime":"codex"}`))
	if result.ExitCode != 0 || !containsRunLoopBlock(result.Stdout) {
		t.Fatalf("executable repo run did not continue: %+v", result)
	}
	for _, path := range []string{sessionStatePath(repo, "repo-fastpath"), activeSessionPath(repo)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("routine repo Stop published session state %s: %v", path, err)
		}
	}
}

func TestRepoRunNoProgressGuardReleasesOneStopWithoutDisabling(t *testing.T) {
	repo := setupPolicyRepo(t)
	writeTaskFixture(t, repo)
	if _, err := SetRunLoopRepoMode(repo, true); err != nil {
		t.Fatal(err)
	}
	runState, err := inspectRunLoopTask(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mutateRunLoopState(repo, func(state runLoopState) runLoopState {
		state.NoProgressNudges = 5
		state.LastCurrent = runLoopTaskProgressFingerprint(runState) + "|material=0"
		state.AwaitingContinuation = true
		return state
	}); err != nil {
		t.Fatal(err)
	}
	result := RunStop(repo, []byte(`{"session_id":"repo-run","runtime":"codex"}`))
	if result.ExitCode != 0 || result.Stdout != "" {
		t.Fatalf("sixth no-progress Stop must release once: %+v", result)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.Mode != runLoopModeRepo || state.NoProgressNudges != 0 || state.AwaitingContinuation {
		t.Fatalf("repo mode must remain enabled after one-stop release: %+v", state)
	}
}

func TestRepoRunExplicitInterruptReleasesCurrentStopWithoutDisabling(t *testing.T) {
	repo := setupPolicyRepo(t)
	writeTaskFixture(t, repo)
	if _, err := SetRunLoopRepoMode(repo, true); err != nil {
		t.Fatal(err)
	}
	result := RunStop(repo, []byte(`{"session_id":"repo-run","runtime":"codex","is_interrupt":true}`))
	if result.ExitCode != 0 || result.Stdout != "" {
		t.Fatalf("interrupt did not release Stop: %+v", result)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !runLoopStateApplies(state) {
		t.Fatalf("interrupt changed durable repository run mode: %+v", state)
	}
}

func TestRepoRunAutomaticallyDisablesWhenTaskPlaneIsAbsent(t *testing.T) {
	repo := setupPolicyRepo(t)
	if _, err := SetRunLoopRepoMode(repo, true); err != nil {
		t.Fatal(err)
	}
	result := RunStop(repo, []byte(`{"session_id":"terminal","runtime":"codex"}`))
	if result.ExitCode != 0 || result.Stdout != "" {
		t.Fatalf("terminal absent TASK plane did not stop cleanly: %+v", result)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.DisabledReason != "task_plane_absent" {
		t.Fatalf("absent TASK plane did not auto-disable repository run: %+v", state)
	}
}

func TestRepoRunAutomaticallyDisablesWhenTaskQueueIsComplete(t *testing.T) {
	repo := t.TempDir()
	if _, err := SetRunLoopRepoMode(repo, true); err != nil {
		t.Fatal(err)
	}
	result, handled, err := runRunLoopContinuation(repo, &HookPayload{SessionID: "complete"}, "codex", tasklifecycle.RunState{Disposition: tasklifecycle.RunComplete}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || result.ExitCode != 0 || result.Stdout != "" {
		t.Fatalf("complete TASK queue did not stop cleanly: handled=%v result=%+v", handled, result)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.DisabledReason != "task_complete" {
		t.Fatalf("complete TASK queue did not auto-disable repository run: %+v", state)
	}
}

func TestRepoRunBlockedOrInvalidTaskStateNeverSilentlyDisables(t *testing.T) {
	for _, disposition := range []tasklifecycle.RunDisposition{tasklifecycle.RunBlocked, tasklifecycle.RunInvalid} {
		t.Run(string(disposition), func(t *testing.T) {
			repo := t.TempDir()
			if _, err := SetRunLoopRepoMode(repo, true); err != nil {
				t.Fatal(err)
			}
			result, handled, err := runRunLoopContinuation(repo, &HookPayload{SessionID: "blocked"}, "codex", tasklifecycle.RunState{Disposition: disposition}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if handled || result != (Result{}) {
				t.Fatalf("non-terminal blocker was incorrectly handled as terminal: handled=%v result=%+v", handled, result)
			}
			state, err := loadRunLoopState(repo)
			if err != nil {
				t.Fatal(err)
			}
			if !runLoopStateApplies(state) {
				t.Fatalf("%s task state silently disabled repository run: %+v", disposition, state)
			}
		})
	}
}

func containsRunLoopBlock(stdout string) bool {
	return strings.Contains(stdout, `"decision":"block"`) && strings.Contains(stdout, "Reconc run is ON")
}
