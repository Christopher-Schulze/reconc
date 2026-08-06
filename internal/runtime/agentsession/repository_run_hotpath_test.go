package agentsession

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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
	for _, name := range []string{"state.bin", "decisions.jsonl"} {
		if _, err := os.Stat(filepath.Join(repo, ".reconc", "run", name)); !os.IsNotExist(err) {
			t.Fatalf("disabled no-op events must not create %s: %v", name, err)
		}
	}
}

func TestUnchangedRepositoryRunStateIsNotRewritten(t *testing.T) {
	repo := t.TempDir()
	state := repositoryRunState{Enabled: true}
	if err := saveRepositoryRunState(repo, state); err != nil {
		t.Fatal(err)
	}
	path, err := repositoryRunStatePath(repo)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mutateRepositoryRunState(repo, func(current repositoryRunState) repositoryRunState { return current }); err != nil {
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
	path := filepath.Join(repo, ".reconc", "run", "state.bin")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("{not-json\n")
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetRepositoryRun(repo, true); err == nil {
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
	for _, runtimeName := range []string{"claude", "codex", "github-copilot", "cursor", "opencode", "devin", "antigravity", "kilo", "omp", "pi"} {
		if _, err := SetRepositoryRun(repo, true); err != nil {
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
		state, err := loadRepositoryRunState(repo)
		if err != nil || !repositoryRunEnabled(state) {
			t.Fatalf("%s lifecycle changed repository run state: %+v err=%v", runtimeName, state, err)
		}
	}
}

func TestRepoRunModeSkipsStopPolicyOnlyForExecutableTask(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 42, "terminal gate")
	writeTaskFixture(t, repo)
	if _, err := SetRepositoryRun(repo, true); err != nil {
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
	if result.ExitCode != 0 || !containsRepositoryRunBlock(result.Stdout) {
		t.Fatalf("executable repo run did not continue: %+v", result)
	}
	if got := readCounter(t, counterPath); got != 0 {
		t.Fatalf("routine repo continuation ran terminal policy %d time(s)", got)
	}

	terminalRepo := setupStopScriptPolicyRepo(t, counterPath, 42, "terminal gate")
	if _, err := SetRepositoryRun(terminalRepo, true); err != nil {
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
	if result.ExitCode != 0 || containsRepositoryRunBlock(result.Stdout) || result.Stdout == "" {
		t.Fatalf("terminal repo run did not retain policy gate: %+v", result)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("terminal Stop policy ran %d time(s), want 1", got)
	}
}

func TestRepoRunTerminalStopFailsClosedOnRequireScriptTimeout(t *testing.T) {
	repo := setupStopScriptPolicyRepo(t, filepath.Join(t.TempDir(), "counter"), 0, "")
	scriptPath := filepath.Join(repo, ".reconc", "scripts", "stop-gate.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'untrusted terminal output\\n'\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := `rules:
  - id: stop-script-gate
    kind: require_script
    when_paths: ['src/**']
    script: '.reconc/scripts/stop-gate.sh'
    mode: block
    timeout_sec: 1
    kill_timeout_sec: 1
    message: stop script gate
`
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	if _, err := InitializeSessionState(repo, "terminal-timeout"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "terminal-timeout", func(state SessionState) SessionState {
		return AppendWritePath(state, "src/a.go")
	}); err != nil {
		t.Fatal(err)
	}

	result := RunStop(repo, []byte(`{"session_id":"terminal-timeout","runtime":"codex"}`))
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, `"decision":"block"`) || containsRepositoryRunBlock(result.Stdout) {
		t.Fatalf("terminal timeout did not remain a policy block: %+v", result)
	}
	for _, want := range []string{"[src/a.go]", "timed out after 1s"} {
		if !strings.Contains(result.Stdout, want) {
			t.Fatalf("terminal timeout output missing %q: %s", want, result.Stdout)
		}
	}
	if strings.Contains(result.Stdout, "untrusted terminal output") {
		t.Fatalf("terminal timeout must not trust script output: %s", result.Stdout)
	}
}

func TestRepoRunExecutableStopPublishesPerSessionGuardState(t *testing.T) {
	repo := setupPolicyRepo(t)
	writeTaskFixture(t, repo)
	if _, err := SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	result := RunStop(repo, []byte(`{"session_id":"repo-fastpath","runtime":"codex"}`))
	if result.ExitCode != 0 || !containsRepositoryRunBlock(result.Stdout) {
		t.Fatalf("executable repo run did not continue: %+v", result)
	}
	state, err := LoadSessionState(repo, "repo-fastpath")
	if err != nil {
		t.Fatal(err)
	}
	if !state.RepositoryRunAwaiting || state.RepositoryRunNudges != 0 || state.RepositoryRunProgressHash == "" {
		t.Fatalf("per-session run guard was not persisted: %+v", state)
	}
	decisions, err := ReadRunDecisions(repo, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 || decisions[1].Branch != "run_followup" || decisions[1].SessionID != "repo-fastpath" {
		t.Fatalf("continuation was not observable: %#v", decisions)
	}
}

func TestRepoRunNoProgressGuardIsIsolatedPerSession(t *testing.T) {
	repo := setupPolicyRepo(t)
	writeTaskFixture(t, repo)
	if _, err := SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	stop := func(session string) Result {
		return RunStop(repo, []byte(`{"session_id":"`+session+`","runtime":"codex"}`))
	}
	if result := stop("session-a"); !containsRepositoryRunBlock(result.Stdout) {
		t.Fatalf("session A first continuation: %+v", result)
	}
	if result := stop("session-a"); !containsRepositoryRunBlock(result.Stdout) {
		t.Fatalf("session A second continuation: %+v", result)
	}
	if result := stop("session-b"); !containsRepositoryRunBlock(result.Stdout) {
		t.Fatalf("session B first continuation: %+v", result)
	}
	a, err := LoadSessionState(repo, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadSessionState(repo, "session-b")
	if err != nil {
		t.Fatal(err)
	}
	if a.RepositoryRunNudges != 1 || b.RepositoryRunNudges != 0 {
		t.Fatalf("session counters interfered: a=%d b=%d", a.RepositoryRunNudges, b.RepositoryRunNudges)
	}
	for range 5 {
		_ = stop("session-a")
	}
	a, _ = LoadSessionState(repo, "session-a")
	b, _ = LoadSessionState(repo, "session-b")
	if a.RepositoryRunAwaiting || a.RepositoryRunNudges != 0 || !b.RepositoryRunAwaiting || b.RepositoryRunNudges != 0 {
		t.Fatalf("one-session release affected another: a=%+v b=%+v", a, b)
	}
}

func TestRepoRunNoProgressGuardConcurrentSessionIsolation(t *testing.T) {
	repo := setupPolicyRepo(t)
	writeTaskFixture(t, repo)
	if _, err := SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	stop := func(session string) Result {
		return RunStop(repo, []byte(`{"session_id":"`+session+`","runtime":"codex"}`))
	}
	for _, session := range []string{"parallel-a", "parallel-b"} {
		if result := stop(session); !containsRepositoryRunBlock(result.Stdout) {
			t.Fatalf("seed %s: %+v", session, result)
		}
	}
	var wait sync.WaitGroup
	errorsOut := make(chan string, 8)
	for _, session := range []string{"parallel-a", "parallel-b"} {
		for range 4 {
			wait.Add(1)
			go func(session string) {
				defer wait.Done()
				if result := stop(session); !containsRepositoryRunBlock(result.Stdout) {
					errorsOut <- fmt.Sprintf("%s: %+v", session, result)
				}
			}(session)
		}
	}
	wait.Wait()
	close(errorsOut)
	for message := range errorsOut {
		t.Error(message)
	}
	a, _ := LoadSessionState(repo, "parallel-a")
	b, _ := LoadSessionState(repo, "parallel-b")
	if a.RepositoryRunNudges != 4 || b.RepositoryRunNudges != 4 {
		t.Fatalf("concurrent counters lost or crossed updates: a=%d b=%d", a.RepositoryRunNudges, b.RepositoryRunNudges)
	}
	_ = stop("parallel-a")
	_ = stop("parallel-b")
	if result := stop("parallel-a"); result.Stdout != "" {
		t.Fatalf("parallel A sixth no-progress Stop did not release: %+v", result)
	}
	a, _ = LoadSessionState(repo, "parallel-a")
	b, _ = LoadSessionState(repo, "parallel-b")
	if a.RepositoryRunAwaiting || !b.RepositoryRunAwaiting || b.RepositoryRunNudges != 5 {
		t.Fatalf("parallel release crossed sessions: a=%+v b=%+v", a, b)
	}
}

func TestStrictContinuationUsesNoSixStopRelease(t *testing.T) {
	repo := setupPolicyRepo(t)
	writeTaskFixture(t, repo)
	if _, err := SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 8; index++ {
		result := RunStop(repo, []byte(`{"session_id":"strict","runtime":"grok","strict_continuation":true}`))
		if result.ExitCode != 0 || !containsRepositoryRunBlock(result.Stdout) {
			t.Fatalf("strict continuation %d released early: %+v", index+1, result)
		}
	}
	state, err := LoadSessionState(repo, "strict")
	if err != nil {
		t.Fatal(err)
	}
	if state.RepositoryRunNudges != 0 || !state.RepositoryRunAwaiting {
		t.Fatalf("strict continuation consumed six-stop guard: %+v", state)
	}
	decisions, err := ReadRunDecisions(repo, 1)
	if err != nil || len(decisions) != 1 || !decisions[0].StrictContinuation {
		t.Fatalf("strict continuation bound is not observable: %#v err=%v", decisions, err)
	}
}

func TestRepoRunEquivalentContinuationLogsOnlyBoundedCheckpoints(t *testing.T) {
	repo := setupPolicyRepo(t)
	writeTaskFixture(t, repo)
	if _, err := SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 7; index++ {
		result := RunStop(repo, []byte(`{"session_id":"bounded-log","runtime":"codex"}`))
		if result.ExitCode != 0 {
			t.Fatalf("Stop %d: %+v", index+1, result)
		}
	}
	decisions, err := ReadRunDecisions(repo, 0)
	if err != nil {
		t.Fatal(err)
	}
	branches := []string{}
	for _, decision := range decisions {
		if decision.Event == "stop" {
			branches = append(branches, decision.Branch)
		}
	}
	want := []string{"run_followup", "run_followup", "run_followup", "repo_no_progress_release"}
	if !reflect.DeepEqual(branches, want) {
		t.Fatalf("bounded continuation decisions = %#v, want %#v", branches, want)
	}
}

func TestRepoRunContinuationFailsClosedWhenDecisionPublicationFails(t *testing.T) {
	repo := setupPolicyRepo(t)
	writeTaskFixture(t, repo)
	if _, err := SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	decisionPath := filepath.Join(repo, ".reconc", "run", "decisions.jsonl")
	if err := os.Remove(decisionPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(decisionPath, 0o700); err != nil {
		t.Fatal(err)
	}
	result := RunStop(repo, []byte(`{"session_id":"decision-publication","runtime":"codex"}`))
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "record repository continuation") {
		t.Fatalf("decision publication failure = %+v", result)
	}
}

func TestRepoRunNoProgressGuardReleasesOneStopWithoutDisabling(t *testing.T) {
	repo := setupPolicyRepo(t)
	writeTaskFixture(t, repo)
	if _, err := SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	runState, err := inspectRepositoryRunTask(repo)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := loadRepositoryRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	progressHash := repositoryRunProgressHash(runState, 0)
	if _, err := MutateSessionState(repo, "repo-run", func(state SessionState) SessionState {
		state.RepositoryRunEnabledAt = durable.EnabledAt
		state.RepositoryRunProgressHash = hex.EncodeToString(progressHash[:])
		state.RepositoryRunNudges = 5
		state.RepositoryRunAwaiting = true
		return state
	}); err != nil {
		t.Fatal(err)
	}
	result := RunStop(repo, []byte(`{"session_id":"repo-run","runtime":"codex"}`))
	if result.ExitCode != 0 || result.Stdout != "" {
		t.Fatalf("sixth no-progress Stop must release once: %+v", result)
	}
	state, err := loadRepositoryRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.NoProgressNudges != 0 || state.AwaitingContinuation {
		t.Fatalf("repo mode must remain enabled after one-stop release: %+v", state)
	}
}

func TestRepoRunExplicitInterruptReleasesCurrentStopWithoutDisabling(t *testing.T) {
	repo := setupPolicyRepo(t)
	writeTaskFixture(t, repo)
	if _, err := SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	result := RunStop(repo, []byte(`{"session_id":"repo-run","runtime":"codex","is_interrupt":true}`))
	if result.ExitCode != 0 || result.Stdout != "" {
		t.Fatalf("interrupt did not release Stop: %+v", result)
	}
	state, err := loadRepositoryRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !repositoryRunEnabled(state) {
		t.Fatalf("interrupt changed durable repository run mode: %+v", state)
	}
}

func TestRepoRunAutomaticallyDisablesWhenTaskPlaneIsAbsent(t *testing.T) {
	repo := setupPolicyRepo(t)
	if _, err := SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	result := RunStop(repo, []byte(`{"session_id":"terminal","runtime":"codex"}`))
	if result.ExitCode != 0 || result.Stdout != "" {
		t.Fatalf("terminal absent TASK plane did not stop cleanly: %+v", result)
	}
	state, err := loadRepositoryRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.DisabledReason != repositoryRunDisabledTaskPlaneAbsent {
		t.Fatalf("absent TASK plane did not auto-disable repository run: %+v", state)
	}
}

func TestRepoRunAutomaticallyDisablesWhenTaskQueueIsComplete(t *testing.T) {
	repo := t.TempDir()
	if _, err := SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	result, handled, err := runRepositoryContinuation(root, nil, &HookPayload{SessionID: "complete"}, "codex", tasklifecycle.RunState{Disposition: tasklifecycle.RunComplete})
	if err != nil {
		t.Fatal(err)
	}
	if !handled || result.ExitCode != 0 || result.Stdout != "" {
		t.Fatalf("complete TASK queue did not stop cleanly: handled=%v result=%+v", handled, result)
	}
	state, err := loadRepositoryRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.DisabledReason != repositoryRunDisabledTaskComplete {
		t.Fatalf("complete TASK queue did not auto-disable repository run: %+v", state)
	}
	decisions, err := ReadRunDecisions(repo, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].Branch != "disable_task_complete" || decisions[0].EnabledAfter {
		t.Fatalf("terminal self-disable is not visible in run log: %#v", decisions)
	}
}

func TestRepoRunBlockedOrInvalidTaskStateNeverSilentlyDisables(t *testing.T) {
	for _, disposition := range []tasklifecycle.RunDisposition{tasklifecycle.RunBlocked, tasklifecycle.RunInvalid} {
		t.Run(string(disposition), func(t *testing.T) {
			repo := t.TempDir()
			if _, err := SetRepositoryRun(repo, true); err != nil {
				t.Fatal(err)
			}
			root, err := ResolveRepoRoot(repo)
			if err != nil {
				t.Fatal(err)
			}
			result, handled, err := runRepositoryContinuation(root, nil, &HookPayload{SessionID: "blocked"}, "codex", tasklifecycle.RunState{Disposition: disposition})
			if err != nil {
				t.Fatal(err)
			}
			if handled || result != (Result{}) {
				t.Fatalf("non-terminal blocker was incorrectly handled as terminal: handled=%v result=%+v", handled, result)
			}
			state, err := loadRepositoryRunState(repo)
			if err != nil {
				t.Fatal(err)
			}
			if !repositoryRunEnabled(state) {
				t.Fatalf("%s task state silently disabled repository run: %+v", disposition, state)
			}
		})
	}
}

func containsRepositoryRunBlock(stdout string) bool {
	return strings.Contains(stdout, `"decision":"block"`) && strings.Contains(stdout, "Reconc run is ON")
}
