package agentsession

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

// setupPolicyRepo creates a compiled repo with one deny_write rule on
// generated/** plus one warn-level require_claim on ci-green (for
// Stop-gate tests).
func setupPolicyRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := `rules:
  - id: deny-generated
    kind: deny_write
    paths: ['generated/**']
    mode: block
    message: no writes to generated
  - id: require-ci-green
    kind: require_claim
    when_paths: ['**']
    claims: ['ci-green']
    mode: block
    message: need ci-green
`
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	// Point the agentsession state-root at an isolated temp dir so
	// tests don't collide across runs.
	t.Setenv(StateRootEnv, t.TempDir())
	return repo
}

func TestRunSessionStartInitialises(t *testing.T) {
	repo := setupPolicyRepo(t)
	result := RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	if result.ExitCode != 0 {
		t.Errorf("expected exit 0, got %d (stderr: %s)", result.ExitCode, result.Stderr)
	}
	// State file should now exist.
	root, _ := ResolveRepoRoot(repo)
	if _, err := os.Stat(sessionStatePath(root, "s1")); err != nil {
		t.Errorf("state file not created: %v", err)
	}
}

func TestRunSessionStartRejectsMalformedPayload(t *testing.T) {
	repo := setupPolicyRepo(t)
	result := RunSessionStart(repo, []byte(`{not json`))
	if result.ExitCode != 2 {
		t.Errorf("expected exit 2 for malformed payload, got %d", result.ExitCode)
	}
}

func TestRunUserPromptSubmitSwitchesRunLoop(t *testing.T) {
	repo := setupPolicyRepo(t)
	enable := RunUserPromptSubmit(repo, []byte(`{"session_id":"s1","prompt":"/runloop"}`))
	if enable.ExitCode != 0 {
		t.Fatalf("enable prompt: exit=%d stderr=%s", enable.ExitCode, enable.Stderr)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.ActiveRunID != "s1" {
		t.Fatalf("expected enabled runloop for s1, got %+v", state)
	}
	stopPath, err := runLoopStopPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stopPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	normal := RunUserPromptSubmit(repo, []byte(`{"session_id":"s1","prompt":"normal follow-up"}`))
	if normal.ExitCode != 0 {
		t.Fatalf("normal prompt: exit=%d stderr=%s", normal.ExitCode, normal.Stderr)
	}
	state, err = loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.DisabledReason != "user_prompt" {
		t.Fatalf("expected user prompt to disable runloop, got %+v", state)
	}
	if _, err := os.Stat(stopPath); err != nil {
		t.Fatalf("expected stop file after normal prompt: %v", err)
	}

	enableAgain := RunUserPromptSubmit(repo, []byte(`{"session_id":"s1","prompt":"/runloop"}`))
	if enableAgain.ExitCode != 0 {
		t.Fatalf("enable again: exit=%d stderr=%s", enableAgain.ExitCode, enableAgain.Stderr)
	}
	state, err = loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.DisabledReason != "" {
		t.Fatalf("expected enabled after explicit runloop prompt, got %+v", state)
	}
	if _, err := os.Stat(stopPath); !os.IsNotExist(err) {
		t.Fatalf("expected activation to clear stop file, got %v", err)
	}
}

func TestRunPreToolUseAllowsNonWriteTool(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	result := RunPreToolUse(repo, []byte(`{"session_id":"s1","tool_name":"Read"}`))
	if result.ExitCode != 0 {
		t.Errorf("non-write tool should not block, got %d (%s)", result.ExitCode, result.Stderr)
	}
}

func TestRunPreToolUseNonWriteSkipsSessionStateMutation(t *testing.T) {
	repo := setupPolicyRepo(t)
	result := RunPreToolUse(repo, []byte(`{"session_id":"s-read-fast","tool_name":"Read","tool_input":{"file_path":"docs/tasks.md"}}`))
	if result.ExitCode != 0 {
		t.Fatalf("non-write tool should not block, got exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessionStatePath(root, "s-read-fast")); !os.IsNotExist(err) {
		t.Fatalf("non-write PreToolUse must not create session state, stat err=%v", err)
	}
}

func TestRunPreToolUseNonWriteDoesNotResolveRepoRoot(t *testing.T) {
	result := RunPreToolUse("/does/not/exist/anywhere", []byte(`{"session_id":"s-read-fast","tool_name":"Read","tool_input":{"file_path":"docs/tasks.md"}}`))
	if result.ExitCode != 0 {
		t.Fatalf("non-write tool should not need repo resolution, got exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
}

func TestRunPreToolUseWriteStillRequiresRepoRoot(t *testing.T) {
	result := RunPreToolUse("/does/not/exist/anywhere", []byte(`{"session_id":"s-write","tool_name":"Write","tool_input":{"file_path":"docs/tasks.md"}}`))
	if result.ExitCode != 2 {
		t.Fatalf("write tool should fail closed on bad repo root, got exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "repo path does not exist") {
		t.Fatalf("stderr should report bad repo root, got %s", result.Stderr)
	}
}

func TestRunPreToolUseBlocksDestructiveGitCommands(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{
			name:    "git clean",
			command: `git clean -fd`,
			want:    "git clean",
		},
		{
			name:    "absolute git clean after reset",
			command: `/usr/bin/git reset --hard HEAD 2>&1 && /usr/bin/git clean -fd 2>&1`,
			want:    "git reset --hard",
		},
		{
			name:    "git clean with repo option",
			command: `git -C "$repo" clean -fd`,
			want:    "git clean",
		},
		{
			name:    "nested shell git clean",
			command: `sh -lc 'git clean -fd'`,
			want:    "git clean",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{"session_id":"s1","tool_name":"Bash","tool_input":{"command":%q}}`, tt.command)
			result := RunPreToolUse(repo, []byte(payload))
			if result.ExitCode != 2 {
				t.Fatalf("expected destructive command to block, got exit=%d stderr=%s", result.ExitCode, result.Stderr)
			}
			if !strings.Contains(result.Stderr, tt.want) {
				t.Fatalf("stderr should mention %q, got %s", tt.want, result.Stderr)
			}
		})
	}
}

func TestRunPreToolUseBlocksDestructiveGitCommandsWithoutSessionState(t *testing.T) {
	repo := setupPolicyRepo(t)
	payload := `{"session_id":"s-shell-fast","tool_name":"Bash","tool_input":{"command":"git reset --hard HEAD"}}`
	result := RunPreToolUse(repo, []byte(payload))
	if result.ExitCode != 2 {
		t.Fatalf("expected destructive command to block without session state, got exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "git reset --hard") {
		t.Fatalf("stderr should cite destructive command, got %s", result.Stderr)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessionStatePath(root, "s-shell-fast")); !os.IsNotExist(err) {
		t.Fatalf("blocked shell PreToolUse must not create session state, stat err=%v", err)
	}
}

func TestRunPreToolUseAllowsSafeGitCommands(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	for _, command := range []string{
		`git status --short`,
		`git clean -nd`,
		`git -C "$repo" clean --dry-run -d`,
		`rg -n "git clean" tools/reconc/internal/runtime/agentsession`,
		`grep -R "git reset --hard" docs`,
	} {
		payload := fmt.Sprintf(`{"session_id":"s1","tool_name":"Bash","tool_input":{"command":%q}}`, command)
		result := RunPreToolUse(repo, []byte(payload))
		if result.ExitCode != 0 {
			t.Fatalf("expected safe command %q to pass, got exit=%d stderr=%s", command, result.ExitCode, result.Stderr)
		}
	}
}

func TestForbiddenShellCommandReasonOnlyRecursesExecutableShellStrings(t *testing.T) {
	allowed := []string{
		`rg -n "git clean" tools/reconc`,
		`grep -R "git reset --hard" docs`,
		`printf '%s\n' "git clean -fd"`,
	}
	for _, command := range allowed {
		if reason := forbiddenShellCommandReason(command); reason != "" {
			t.Fatalf("literal command text %q should not block, got %s", command, reason)
		}
	}

	blocked := []string{
		`sh -lc 'git clean -fd'`,
		`bash -c "git reset --hard HEAD"`,
		`eval "git clean -fd"`,
		`find . -exec sh -c 'git clean -fd' \;`,
	}
	for _, command := range blocked {
		if reason := forbiddenShellCommandReason(command); reason == "" {
			t.Fatalf("executable nested shell command %q should block", command)
		}
	}
}

func TestRunPreToolUseBlocksDenyWrite(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	payload := `{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"generated/x.go"}}`
	result := RunPreToolUse(repo, []byte(payload))
	if result.ExitCode != 2 {
		t.Errorf("expected exit 2 for deny_write hit, got %d (%s)", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "deny-generated") {
		t.Errorf("stderr should mention rule id: %s", result.Stderr)
	}
}

func TestRunPreToolUseBlocksApplyPatchDenyWrite(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	payload := `{"session_id":"s1","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Update File: generated/x.go\n@@\n-old\n+new\n*** End Patch"}}`
	result := RunPreToolUse(repo, []byte(payload))
	if result.ExitCode != 2 {
		t.Errorf("expected exit 2 for apply_patch deny_write hit, got %d (%s)", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "deny-generated") {
		t.Errorf("stderr should mention rule id: %s", result.Stderr)
	}
}

func TestRunPermissionRequestDeniesBlockedApplyPatch(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	payload := `{"session_id":"s1","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Add File: generated/x.go\n+package generated\n*** End Patch"}}`
	result := RunPermissionRequest(repo, []byte(payload))
	if result.ExitCode != 0 {
		t.Fatalf("PermissionRequest should answer with JSON, got exit %d stderr=%s", result.ExitCode, result.Stderr)
	}
	for _, fragment := range []string{"PermissionRequest", "deny", "deny-generated"} {
		if !strings.Contains(result.Stdout, fragment) {
			t.Errorf("PermissionRequest stdout should contain %q, got %s", fragment, result.Stdout)
		}
	}
}

func TestRunPreToolUseAllowsLegitWrite(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	payload := `{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"src/main.go"}}`
	result := RunPreToolUse(repo, []byte(payload))
	if result.ExitCode != 0 {
		t.Errorf("legit write should not block, got %d (%s)", result.ExitCode, result.Stderr)
	}
}

func TestRunPreToolUseSkipsRequireScriptAudits(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	_, err := InitializeSessionState(repo, "s-pre-fast")
	if err != nil {
		t.Fatal(err)
	}

	result := RunPreToolUse(repo, []byte(`{"session_id":"s-pre-fast","tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`))
	if result.ExitCode != 0 {
		t.Fatalf("pre write should pass without running stop-time scripts, got exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	if got := readCounter(t, counterPath); got != 0 {
		t.Fatalf("PreToolUse must not run require_script audits, got %d runs", got)
	}
}

func TestRunPreToolUseFailsClosedOnMalformedPayload(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	result := RunPreToolUse(repo, []byte(`{not json`))
	if result.ExitCode != 2 {
		t.Errorf("expected fail-closed exit 2 on malformed payload, got %d", result.ExitCode)
	}
}

func TestRunPostToolUseRecordsEvidence(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	payload := `{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"src/main.go"}}`
	result := RunPostToolUse(repo, []byte(payload))
	if result.ExitCode != 0 {
		t.Errorf("PostToolUse should never block, got %d", result.ExitCode)
	}
	// State should now have the write recorded.
	state, _ := LoadSessionState(repo, "s1")
	if len(state.WritePaths) != 1 || state.WritePaths[0] != "src/main.go" {
		t.Errorf("write not recorded: %v", state.WritePaths)
	}
}

func TestRunPostToolUseReadSkipsPolicyAudit(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	_, err := InitializeSessionState(repo, "s-read-fast")
	if err != nil {
		t.Fatal(err)
	}
	_, err = MutateSessionState(repo, "s-read-fast", func(state SessionState) SessionState {
		return AppendWritePath(state, "src/a.go")
	})
	if err != nil {
		t.Fatal(err)
	}

	result := RunPostToolUse(repo, []byte(`{"session_id":"s-read-fast","tool_name":"Read","tool_input":{"file_path":"docs/tasks.md"}}`))
	if result.ExitCode != 0 || result.Stdout != "" {
		t.Fatalf("read post hook should record silently, got exit=%d stdout=%q stderr=%s", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got := readCounter(t, counterPath); got != 0 {
		t.Fatalf("Read PostToolUse must not run policy scripts, got %d runs", got)
	}
	state, err := LoadSessionState(repo, "s-read-fast")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.ReadPaths) != 1 || state.ReadPaths[0] != "docs/tasks.md" {
		t.Fatalf("read evidence not recorded: %v", state.ReadPaths)
	}
}

func TestRunPostToolUseWriteSkipsPolicyAudit(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	_, err := InitializeSessionState(repo, "s-post-cache")
	if err != nil {
		t.Fatal(err)
	}

	post := RunPostToolUse(repo, []byte(`{"session_id":"s-post-cache","tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`))
	if post.ExitCode != 0 || post.Stdout != "" {
		t.Fatalf("write post should pass silently for clean policy, got exit=%d stdout=%q stderr=%s", post.ExitCode, post.Stdout, post.Stderr)
	}
	if got := readCounter(t, counterPath); got != 0 {
		t.Fatalf("write PostToolUse must not run policy scripts, got %d runs", got)
	}

	stop := RunStop(repo, []byte(`{"session_id":"s-post-cache"}`))
	if stop.ExitCode != 0 || stop.Stdout != "" {
		t.Fatalf("stop after warmed cache should pass silently, got exit=%d stdout=%q stderr=%s", stop.ExitCode, stop.Stdout, stop.Stderr)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("expected stop to run script once, got %d script runs", got)
	}
}

func TestRunPostToolUseMergesParallelReadEvidence(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))

	const workers = 30
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := fmt.Sprintf(`{"session_id":"s1","tool_name":"Read","tool_input":{"file_path":"docs/read-%02d.md"}}`, i)
			result := RunPostToolUse(repo, []byte(payload))
			if result.ExitCode != 0 || result.Stderr != "" {
				errs <- fmt.Sprintf("exit=%d stderr=%s", result.ExitCode, result.Stderr)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	state, err := LoadSessionState(repo, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.ReadPaths) != workers {
		t.Fatalf("expected %d read paths, got %d: %v", workers, len(state.ReadPaths), state.ReadPaths)
	}
}

func TestRunPostToolUseFailureRecordsOutcome(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	payload := `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"go test"},"tool_response":{"exit_code":1},"error":"boom"}`
	result := RunPostToolUseFailure(repo, []byte(payload))
	if result.ExitCode != 0 {
		t.Errorf("PostToolUseFailure should not block, got %d", result.ExitCode)
	}
	state, _ := LoadSessionState(repo, "s1")
	if len(state.CommandResults) != 1 {
		t.Fatalf("expected 1 command_result, got %d", len(state.CommandResults))
	}
	if state.CommandResults[0].Outcome != "failure" {
		t.Errorf("expected outcome=failure, got %s", state.CommandResults[0].Outcome)
	}
}

func TestRunPostToolUseIgnoresExternalReadEvidence(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	external := filepath.Join(t.TempDir(), "cursor-terminal.txt")
	if err := os.WriteFile(external, []byte("terminal output"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"session_id":"s1","tool_name":"Read","tool_input":{"file_path":%q}}`, external)
	result := RunPostToolUse(repo, []byte(payload))
	if result.ExitCode != 0 {
		t.Fatalf("PostToolUse should not block, got %d", result.ExitCode)
	}
	state, err := LoadSessionState(repo, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.ReadPaths) != 0 {
		t.Fatalf("external runtime read artifact must not poison read evidence, got %v", state.ReadPaths)
	}
}

func TestRunStopRunLoopToleratesExistingExternalReadEvidence(t *testing.T) {
	repo := setupPolicyRepo(t)
	writeTaskFixture(t, repo)
	_ = RunSessionStart(repo, []byte(`{"session_id":"cursor-run"}`))
	start := RunUserPromptSubmit(repo, []byte(`{"session_id":"cursor-run","runtime":"cursor","prompt":"arbeite weiter /runloop"}`))
	if start.ExitCode != 0 {
		t.Fatalf("user prompt: %s", start.Stderr)
	}
	external := filepath.Join(t.TempDir(), "cursor-terminal.txt")
	if err := os.WriteFile(external, []byte("terminal output"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "cursor-run", func(state SessionState) SessionState {
		return AppendReadPath(state, external)
	}); err != nil {
		t.Fatal(err)
	}
	stop := RunStop(repo, []byte(`{"session_id":"cursor-run","runtime":"cursor"}`))
	if stop.ExitCode != 0 {
		t.Fatalf("stop: exit=%d stderr=%s stdout=%s", stop.ExitCode, stop.Stderr, stop.Stdout)
	}
	if !strings.Contains(stop.Stdout, "LET ME COOK") {
		t.Fatalf("expected runloop continuation despite external read artifact, got stdout=%q stderr=%q", stop.Stdout, stop.Stderr)
	}
}

// TestRunStopContinuationPersistsAwaitingUnderLock guards the RunStop refactor
// that moved both runloop regions into mutateRunLoopState: the continuation
// decision must still persist enabled+awaiting via the locked atomic write, so
// the emitted followup and the on-disk state agree.
func TestRunStopContinuationPersistsAwaitingUnderLock(t *testing.T) {
	repo := setupPolicyRepo(t)
	writeTaskFixture(t, repo)
	_ = RunSessionStart(repo, []byte(`{"session_id":"cursor-run","runtime":"cursor"}`))
	if start := RunUserPromptSubmit(repo, []byte(`{"session_id":"cursor-run","runtime":"cursor","prompt":"/runloop"}`)); start.ExitCode != 0 {
		t.Fatalf("enable runloop: %s", start.Stderr)
	}
	stop := RunStop(repo, []byte(`{"session_id":"cursor-run","runtime":"cursor"}`))
	if stop.ExitCode != 0 {
		t.Fatalf("stop: exit=%d stderr=%s", stop.ExitCode, stop.Stderr)
	}
	if !strings.Contains(stop.Stdout, "LET ME COOK") {
		t.Fatalf("expected continuation prompt, got stdout=%q stderr=%q", stop.Stdout, stop.Stderr)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if !state.Enabled || !state.AwaitingContinuation {
		t.Fatalf("continuation must persist enabled+awaiting through the lock, got %+v", state)
	}
}

func TestRunStopBlocksOnMissingClaim(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	// Record a write so the require_claim rule would gate the session.
	_ = RunPostToolUse(repo, []byte(`{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`))

	result := RunStop(repo, []byte(`{"session_id":"s1"}`))
	if result.ExitCode != 0 {
		t.Errorf("Stop always returns exit 0 (control via JSON), got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, `"decision":"block"`) {
		t.Errorf("Stop should emit decision=block JSON, got: %s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "require-ci-green") {
		t.Errorf("block reason should cite rule id: %s", result.Stdout)
	}
}

func TestRunStopPolicyReportCacheSkipsRepeatedScriptRun(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	_, err := InitializeSessionState(repo, "s-cache")
	if err != nil {
		t.Fatal(err)
	}
	_, err = MutateSessionState(repo, "s-cache", func(state SessionState) SessionState {
		return AppendWritePath(state, "src/a.go")
	})
	if err != nil {
		t.Fatal(err)
	}

	first := RunStop(repo, []byte(`{"session_id":"s-cache"}`))
	if first.ExitCode != 0 || first.Stdout != "" {
		t.Fatalf("first clean stop: exit=%d stdout=%q stderr=%s", first.ExitCode, first.Stdout, first.Stderr)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("expected first stop to run script once, got %d", got)
	}

	second := RunStop(repo, []byte(`{"session_id":"s-cache"}`))
	if second.ExitCode != 0 || second.Stdout != "" {
		t.Fatalf("second clean stop: exit=%d stdout=%q stderr=%s", second.ExitCode, second.Stdout, second.Stderr)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("expected cached second stop to skip script, got %d runs", got)
	}

	_, err = MutateSessionState(repo, "s-cache", func(state SessionState) SessionState {
		state.WritePaths = []string{"src/b.go"}
		return state
	})
	if err != nil {
		t.Fatal(err)
	}
	third := RunStop(repo, []byte(`{"session_id":"s-cache"}`))
	if third.ExitCode != 0 || third.Stdout != "" {
		t.Fatalf("third clean stop: exit=%d stdout=%q stderr=%s", third.ExitCode, third.Stdout, third.Stderr)
	}
	if got := readCounter(t, counterPath); got != 2 {
		t.Fatalf("expected evidence change to invalidate cache, got %d runs", got)
	}
}

func TestRunStopHookActiveCleanCacheSkipsStaleFingerprint(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	if _, err := InitializeSessionState(repo, "s-reentrant-clean-cache"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "s-reentrant-clean-cache", func(state SessionState) SessionState {
		return AppendWritePath(state, "src/a.go")
	}); err != nil {
		t.Fatal(err)
	}

	first := RunStop(repo, []byte(`{"session_id":"s-reentrant-clean-cache"}`))
	if first.ExitCode != 0 || first.Stdout != "" {
		t.Fatalf("first clean stop: exit=%d stdout=%q stderr=%s", first.ExitCode, first.Stdout, first.Stderr)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("expected first stop to run script once, got %d", got)
	}
	state, err := LoadSessionState(repo, "s-reentrant-clean-cache")
	if err != nil {
		t.Fatal(err)
	}
	if state.StopPolicyEvidenceHash == "" {
		t.Fatal("first policy run must persist a stop-policy evidence hash")
	}
	if _, err := MutateSessionState(repo, "s-reentrant-clean-cache", func(state SessionState) SessionState {
		state.StopPolicyFingerprint = "stale-fingerprint"
		return state
	}); err != nil {
		t.Fatal(err)
	}

	second := RunStop(repo, []byte(`{"session_id":"s-reentrant-clean-cache","stop_hook_active":true}`))
	if second.ExitCode != 0 || second.Stdout != "" {
		t.Fatalf("reentrant clean cached stop: exit=%d stdout=%q stderr=%s", second.ExitCode, second.Stdout, second.Stderr)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("reentrant clean cached stop should skip full policy despite stale fingerprint, got %d script runs", got)
	}
}

func TestRunStopHookActiveCleanCacheInvalidatesOnEvidenceChange(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	if _, err := InitializeSessionState(repo, "s-reentrant-cache-invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "s-reentrant-cache-invalid", func(state SessionState) SessionState {
		return AppendWritePath(state, "src/a.go")
	}); err != nil {
		t.Fatal(err)
	}

	first := RunStop(repo, []byte(`{"session_id":"s-reentrant-cache-invalid"}`))
	if first.ExitCode != 0 || first.Stdout != "" {
		t.Fatalf("first clean stop: exit=%d stdout=%q stderr=%s", first.ExitCode, first.Stdout, first.Stderr)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("expected first stop to run script once, got %d", got)
	}
	if _, err := MutateSessionState(repo, "s-reentrant-cache-invalid", func(state SessionState) SessionState {
		state.StopPolicyFingerprint = "stale-fingerprint"
		state.WritePaths = []string{"src/b.go"}
		return state
	}); err != nil {
		t.Fatal(err)
	}

	second := RunStop(repo, []byte(`{"session_id":"s-reentrant-cache-invalid","stop_hook_active":true}`))
	if second.ExitCode != 0 || second.Stdout != "" {
		t.Fatalf("reentrant changed-evidence stop: exit=%d stdout=%q stderr=%s", second.ExitCode, second.Stdout, second.Stderr)
	}
	if got := readCounter(t, counterPath); got != 2 {
		t.Fatalf("changed evidence must force full policy rerun, got %d script runs", got)
	}
}

func TestRunStopCachedPolicyStillEmitsRunLoopPrompt(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	writeTaskFixture(t, repo)
	if start := RunUserPromptSubmit(repo, []byte(`{"session_id":"s-runLoop-cache","prompt":"/runloop"}`)); start.ExitCode != 0 {
		t.Fatalf("enable runloop: %s", start.Stderr)
	}
	_, err := InitializeSessionState(repo, "s-runLoop-cache")
	if err != nil {
		t.Fatal(err)
	}
	_, err = MutateSessionState(repo, "s-runLoop-cache", func(state SessionState) SessionState {
		return AppendWritePath(state, "src/a.go")
	})
	if err != nil {
		t.Fatal(err)
	}

	first := RunStop(repo, []byte(`{"session_id":"s-runLoop-cache"}`))
	if !strings.Contains(first.Stdout, "LET ME COOK") {
		t.Fatalf("expected first runLoop stop to emit prompt, got: %s", first.Stdout)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("expected first stop to run script once, got %d", got)
	}
	dmState, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	dmState.AwaitingContinuation = false
	if err := saveRunLoopState(repo, dmState); err != nil {
		t.Fatal(err)
	}

	second := RunStop(repo, []byte(`{"session_id":"s-runLoop-cache"}`))
	if !strings.Contains(second.Stdout, "LET ME COOK") {
		t.Fatalf("cached policy report must not suppress runLoop prompt, got: %s", second.Stdout)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("expected cached runLoop stop to skip script, got %d runs", got)
	}
}

func TestRunStopPolicyBlockWinsOverRunLoopThenReleasesOnRepeat(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 2, "real audit failure")
	writeTaskFixture(t, repo)
	if start := RunUserPromptSubmit(repo, []byte(`{"session_id":"s-block-cache","prompt":"/runloop"}`)); start.ExitCode != 0 {
		t.Fatalf("enable runloop: %s", start.Stderr)
	}
	_, err := InitializeSessionState(repo, "s-block-cache")
	if err != nil {
		t.Fatal(err)
	}
	_, err = MutateSessionState(repo, "s-block-cache", func(state SessionState) SessionState {
		return AppendWritePath(state, "src/a.go")
	})
	if err != nil {
		t.Fatal(err)
	}

	first := RunStop(repo, []byte(`{"session_id":"s-block-cache"}`))
	for _, want := range []string{`"decision":"block"`, "stop-script-gate", "Report:", "Runloop: enabled", "blocked_by_policy", "real audit failure"} {
		if !strings.Contains(first.Stdout, want) {
			t.Fatalf("first block should contain %q, got: %s", want, first.Stdout)
		}
	}
	if strings.Contains(first.Stdout, "LET ME COOK") {
		t.Fatalf("policy block must win over runLoop prompt, got: %s", first.Stdout)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("expected first policy block to run script once, got %d", got)
	}
	// Second identical stop must NOT re-block: a user stop/interrupt has to win,
	// otherwise Cursor (which never sets stop_hook_active) is trapped in an
	// unbreakable stop loop on an unresolved blocking violation. The block fired
	// once on the first stop; the identical repeat releases the session.
	second := RunStop(repo, []byte(`{"session_id":"s-block-cache"}`))
	if second.ExitCode != 0 {
		t.Fatalf("repeated stop should yield exit 0, got %d", second.ExitCode)
	}
	if second.Stdout != "" {
		t.Fatalf("repeated identical policy block must release the stop (empty stdout), got: %s", second.Stdout)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("expected repeated policy block to use cached report, got %d script runs", got)
	}
}

func TestCursorStopReleasesStopLoopOnRepeatedBlockEndToEnd(t *testing.T) {
	// End-to-end reproduction of the "runloop/Cursor cannot be stopped" bug
	// through the real cursor-stop path (NormalizeCursorPayload -> RunStop ->
	// AdaptCursorResult). Cursor never sets stop_hook_active, so an unresolved
	// blocking violation must NOT trap the session forever: the first stop
	// blocks (followup), the identical repeat releases (continue + allow).
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 2, "unresolved blocker")
	if _, err := InitializeSessionState(repo, "cur-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "cur-1", func(s SessionState) SessionState {
		return AppendWritePath(s, "src/a.go")
	}); err != nil {
		t.Fatal(err)
	}

	stopPayload := []byte(`{"session_id":"cur-1","conversation_id":"cur-1","hook_event_name":"stop","model":"composer-2.5","cursor_version":"3.5.17"}`)
	runCursorStop := func() Result {
		norm, err := NormalizeCursorPayload("cursor-stop", stopPayload)
		if err != nil {
			t.Fatalf("normalize cursor payload: %v", err)
		}
		return AdaptCursorResult("cursor-stop", RunStop(repo, norm))
	}

	first := runCursorStop()
	if !strings.Contains(first.Stdout, "followup_message") {
		t.Fatalf("first cursor stop with an unresolved blocker must keep the agent going (followup), got: %s", first.Stdout)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("first stop must run the gate once, got %d", got)
	}

	second := runCursorStop()
	if !strings.Contains(second.Stdout, `"continue":true`) || !strings.Contains(second.Stdout, `"permission":"allow"`) {
		t.Fatalf("repeated identical cursor stop must release the session (continue+allow), got: %s", second.Stdout)
	}
	if strings.Contains(second.Stdout, "followup_message") {
		t.Fatalf("released stop must not carry a followup, got: %s", second.Stdout)
	}
}

func TestRunStopHappyPath(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	// No writes, no claims required to trigger.
	result := RunStop(repo, []byte(`{"session_id":"s1"}`))
	if result.ExitCode != 0 || result.Stdout != "" {
		t.Errorf("clean session should pass Stop silently, got exit=%d stdout=%q", result.ExitCode, result.Stdout)
	}
	if strings.Contains(result.Stdout, "LET ME COOK") {
		t.Fatalf("normal stop without runloop must not emit runLoop prompt, got: %s", result.Stdout)
	}
}

func TestRunStopSkipsWhenStopHookActive(t *testing.T) {
	// Avoid infinite loops: if Claude already invoked stop once for
	// these violations, we don't keep re-issuing the block.
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	_ = RunPostToolUse(repo, []byte(`{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`))

	result := RunStop(repo, []byte(`{"session_id":"s1","stop_hook_active":true}`))
	if result.ExitCode != 0 {
		t.Errorf("stop_hook_active should yield exit 0, got %d", result.ExitCode)
	}
	if result.Stdout != "" {
		t.Errorf("stop_hook_active should suppress block JSON, got: %s", result.Stdout)
	}
}

func TestRunSessionEndCleansState(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))

	result := RunSessionEnd(repo, []byte(`{"session_id":"s1"}`))
	if result.ExitCode != 0 {
		t.Errorf("expected exit 0, got %d", result.ExitCode)
	}
	root, _ := ResolveRepoRoot(repo)
	if _, err := os.Stat(sessionStatePath(root, "s1")); !os.IsNotExist(err) {
		t.Errorf("state file should be removed after SessionEnd, stat err: %v", err)
	}
}

func TestFullHappyFlow(t *testing.T) {
	// E2E: SessionStart -> PreToolUse(legit) -> PostToolUse -> Stop with
	// ci-green claim asserted -> SessionEnd. Every step exit 0.
	repo := setupPolicyRepo(t)
	steps := []struct {
		name string
		call func() Result
	}{
		{"SessionStart", func() Result { return RunSessionStart(repo, []byte(`{"session_id":"s1"}`)) }},
		{"PreToolUse", func() Result {
			return RunPreToolUse(repo, []byte(`{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"src/x.go"}}`))
		}},
		{"PostToolUse", func() Result {
			return RunPostToolUse(repo, []byte(`{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"src/x.go"}}`))
		}},
	}
	for _, s := range steps {
		r := s.call()
		if r.ExitCode != 0 {
			t.Fatalf("%s exit=%d stderr=%s", s.name, r.ExitCode, r.Stderr)
		}
	}
	// Record the claim so Stop passes.
	rep, err := RecordClaim(repo, "ci-green", "s1")
	if err != nil {
		t.Fatalf("RecordClaim: %v", err)
	}
	if rep.ClaimCount != 1 {
		t.Errorf("expected ClaimCount=1, got %d", rep.ClaimCount)
	}
	stop := RunStop(repo, []byte(`{"session_id":"s1"}`))
	if stop.Stdout != "" {
		t.Errorf("Stop should be silent after claim asserted, got %s", stop.Stdout)
	}
	end := RunSessionEnd(repo, []byte(`{"session_id":"s1"}`))
	if end.ExitCode != 0 {
		t.Errorf("SessionEnd exit=%d", end.ExitCode)
	}
}

func TestRunStopRunLoopEnabledReturnsBlock(t *testing.T) {
	_ = os.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	gitInitHelper(t, repo)
	writeTaskFixture(t, repo)

	// Enable runloop via the authoritative user-prompt switch.
	start := RunUserPromptSubmit(repo, []byte(`{"session_id":"ses_dm","prompt":"/runloop"}`))
	if start.ExitCode != 0 {
		t.Fatalf("user prompt: %s", start.Stderr)
	}
	dmState, _ := loadRunLoopState(repo)
	if !dmState.Enabled {
		t.Fatal("expected runloop enabled after explicit user prompt")
	}

	// Stop with runloop enabled — should return a block with prompt.
	stop := RunStop(repo, []byte(`{"session_id":"ses_dm"}`))
	if stop.ExitCode != 0 {
		t.Fatalf("stop: exit=%d stderr=%s", stop.ExitCode, stop.Stderr)
	}
	if !strings.Contains(stop.Stdout, `"decision":"block"`) {
		t.Fatalf("expected block decision for runloop stop, got: %s", stop.Stdout)
	}
	if !strings.Contains(stop.Stdout, "LET ME COOK") {
		t.Fatalf("expected runloop prompt in block reason, got: %s", stop.Stdout)
	}
	if !strings.Contains(stop.Stdout, "LET ME COOK") {
		t.Fatalf("expected LET ME COOK text, got: %s", stop.Stdout)
	}
}

func TestRunStopRunLoopStopFileAllowsStop(t *testing.T) {
	_ = os.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	gitInitHelper(t, repo)
	writeTaskFixture(t, repo)

	// Enable runloop.
	start := RunUserPromptSubmit(repo, []byte(`{"session_id":"ses_dm2","prompt":"/runloop"}`))
	if start.ExitCode != 0 {
		t.Fatalf("user prompt: %s", start.Stderr)
	}

	// Create stop file (simulating user abort).
	stopDir := filepath.Join(repo, ".reconc", "runloop")
	if err := os.MkdirAll(stopDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stopDir, "stop"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// Stop should allow (exit 0) when stop file exists.
	stop := RunStop(repo, []byte(`{"session_id":"ses_dm2"}`))
	if stop.ExitCode != 0 {
		t.Fatalf("stop: exit=%d stderr=%s", stop.ExitCode, stop.Stderr)
	}
	if stop.Stdout != "" {
		t.Fatalf("expected clean stop when stop file exists, got: %s", stop.Stdout)
	}

	// Runloop should now be disabled.
	dmState, _ := loadRunLoopState(repo)
	if dmState.Enabled {
		t.Fatal("expected runloop disabled after stop with stop file")
	}
}

func TestRunStopRunLoopStopHookActiveReemitsContinuation(t *testing.T) {
	_ = os.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	gitInitHelper(t, repo)
	writeTaskFixture(t, repo)

	// Enable runloop.
	start := RunUserPromptSubmit(repo, []byte(`{"session_id":"ses_dm3","prompt":"/runloop"}`))
	if start.ExitCode != 0 {
		t.Fatalf("user prompt: %s", start.Stderr)
	}

	// Stop with stop_hook_active=true is Claude's reentrant Stop-hook
	// continuation path. It must continue Runloop instead of silently
	// halting the active run.
	stop := RunStop(repo, []byte(`{"session_id":"ses_dm3","stop_hook_active":true}`))
	if stop.ExitCode != 0 {
		t.Fatalf("stop: exit=%d stderr=%s", stop.ExitCode, stop.Stderr)
	}
	if !strings.Contains(stop.Stdout, `"decision":"block"`) || !strings.Contains(stop.Stdout, "LET ME COOK") {
		t.Fatalf("expected reentrant stop to emit runloop continuation, got: %s", stop.Stdout)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || !state.AwaitingContinuation {
		t.Fatalf("expected enabled awaiting continuation after reentrant stop, got %+v", state)
	}
}

func TestRunStopRunLoopFastPathBypassesRoutinePolicyGate(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	writeTaskFixture(t, repo)

	start := RunUserPromptSubmit(repo, []byte(`{"session_id":"ses_fast","prompt":"/runloop"}`))
	if start.ExitCode != 0 {
		t.Fatalf("user prompt: %s", start.Stderr)
	}

	stop := RunStop(repo, []byte(`{"session_id":"ses_fast"}`))
	if stop.ExitCode != 0 {
		t.Fatalf("stop: exit=%d stderr=%s", stop.ExitCode, stop.Stderr)
	}
	if !strings.Contains(stop.Stdout, "LET ME COOK") {
		t.Fatalf("expected runloop continuation prompt, got: %s", stop.Stdout)
	}
	if got := readCounter(t, counterPath); got != 0 {
		t.Fatalf("routine runloop stop without stop-policy evidence must not run Stop policy script, got %d runs", got)
	}
}

func TestRunStopRunLoopStopHookActiveUsesCleanCachedPolicyFastPath(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	writeTaskFixture(t, repo)
	if start := RunUserPromptSubmit(repo, []byte(`{"session_id":"ses_reentrant_cache","prompt":"/runloop"}`)); start.ExitCode != 0 {
		t.Fatalf("enable runloop: %s", start.Stderr)
	}
	_, err := InitializeSessionState(repo, "ses_reentrant_cache")
	if err != nil {
		t.Fatal(err)
	}
	_, err = MutateSessionState(repo, "ses_reentrant_cache", func(state SessionState) SessionState {
		return AppendWritePath(state, "src/a.go")
	})
	if err != nil {
		t.Fatal(err)
	}

	first := RunStop(repo, []byte(`{"session_id":"ses_reentrant_cache"}`))
	if !strings.Contains(first.Stdout, "LET ME COOK") {
		t.Fatalf("expected first runLoop stop to emit prompt, got: %s", first.Stdout)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("expected first evidence-bearing stop to run policy once, got %d runs", got)
	}
	dmState, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	dmState.AwaitingContinuation = false
	if err := saveRunLoopState(repo, dmState); err != nil {
		t.Fatal(err)
	}

	second := RunStop(repo, []byte(`{"session_id":"ses_reentrant_cache","stop_hook_active":true}`))
	if !strings.Contains(second.Stdout, "LET ME COOK") {
		t.Fatalf("reentrant stop should continue from clean cached report, got: %s", second.Stdout)
	}
	if got := readCounter(t, counterPath); got != 1 {
		t.Fatalf("reentrant stop_hook_active path should reuse clean cached report without rerunning script, got %d runs", got)
	}
}

func TestRunStopOpenCodeContinuationDriverSkipsOnlyRunLoopBlock(t *testing.T) {
	_ = os.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	gitInitHelper(t, repo)
	writeTaskFixture(t, repo)

	start := RunUserPromptSubmit(repo, []byte(`{"session_id":"ses_oc","prompt":"/runloop"}`))
	if start.ExitCode != 0 {
		t.Fatalf("user prompt: %s", start.Stderr)
	}

	stop := RunStop(repo, []byte(`{"session_id":"ses_oc","opencode_continuation_driver":true}`))
	if stop.ExitCode != 0 {
		t.Fatalf("stop: exit=%d stderr=%s", stop.ExitCode, stop.Stderr)
	}
	if strings.Contains(stop.Stdout, "LET ME COOK") {
		t.Fatalf("OpenCode plugin must own continuation prompt, got: %s", stop.Stdout)
	}
}

func TestRunStopRunLoopAwaitingContinuationReemitsInsteadOfDisabling(t *testing.T) {
	_ = os.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	gitInitHelper(t, repo)
	writeTaskFixture(t, repo)

	start := RunUserPromptSubmit(repo, []byte(`{"session_id":"ses_wait","prompt":"/runloop"}`))
	if start.ExitCode != 0 {
		t.Fatalf("user prompt: %s", start.Stderr)
	}
	first := RunStop(repo, []byte(`{"session_id":"ses_wait"}`))
	if !strings.Contains(first.Stdout, "LET ME COOK") {
		t.Fatalf("expected first stop to emit continuation prompt, got: %s", first.Stdout)
	}
	state, _ := loadRunLoopState(repo)
	if !state.AwaitingContinuation {
		t.Fatal("expected awaiting_continuation after emitted continuation prompt")
	}

	second := RunStop(repo, []byte(`{"session_id":"ses_wait"}`))
	if second.ExitCode != 0 {
		t.Fatalf("second stop: exit=%d stderr=%s", second.ExitCode, second.Stderr)
	}
	if !strings.Contains(second.Stdout, "LET ME COOK") {
		t.Fatalf("expected second stop without tool use to reemit continuation, got: %s", second.Stdout)
	}
	again, _ := loadRunLoopState(repo)
	if !again.Enabled {
		t.Fatal("expected runloop to remain enabled after awaiting continuation stop")
	}
	if again.DisabledReason != "" {
		t.Fatalf("expected empty disabled_reason, got %q", again.DisabledReason)
	}
	logBody, err := os.ReadFile(filepath.Join(repo, ".reconc", "runloop", "decisions.jsonl"))
	if err != nil {
		t.Fatalf("read decision log: %v", err)
	}
	if strings.Count(string(logBody), `"branch":"runLoop_followup_message"`) < 2 {
		t.Fatalf("expected both stops logged as runLoop followups, got:\n%s", logBody)
	}
}

func TestRunStopOtherSessionAwaitingContinuationDoesNotReemitPrompt(t *testing.T) {
	_ = os.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	gitInitHelper(t, repo)
	writeTaskFixture(t, repo)

	if err := saveRunLoopState(repo, runLoopState{
		Enabled:              true,
		SessionID:            "old-session",
		ActiveRunID:          "old-session",
		AwaitingContinuation: true,
	}); err != nil {
		t.Fatal(err)
	}

	stop := RunStop(repo, []byte(`{"session_id":"new-session"}`))
	if stop.ExitCode != 0 {
		t.Fatalf("stop: exit=%d stderr=%s", stop.ExitCode, stop.Stderr)
	}
	if stop.Stdout != "" {
		t.Fatalf("other session awaiting_continuation must stop silently, got: %s", stop.Stdout)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.ActiveRunID != "old-session" {
		t.Fatalf("expected other session stop to preserve active run, got %+v", state)
	}
}

func TestRunStopOtherSessionNormalPromptDoesNotClearRunLoopState(t *testing.T) {
	_ = os.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	gitInitHelper(t, repo)
	writeTaskFixture(t, repo)

	if err := saveRunLoopState(repo, runLoopState{
		Enabled:     true,
		SessionID:   "old-session",
		ActiveRunID: "old-session",
	}); err != nil {
		t.Fatal(err)
	}
	prompt := RunUserPromptSubmit(repo, []byte(`{"session_id":"new-session","prompt":"normal prompt"}`))
	if prompt.ExitCode != 0 {
		t.Fatalf("normal prompt: exit=%d stderr=%s", prompt.ExitCode, prompt.Stderr)
	}
	stop := RunStop(repo, []byte(`{"session_id":"new-session"}`))
	if stop.ExitCode != 0 {
		t.Fatalf("stop: exit=%d stderr=%s", stop.ExitCode, stop.Stderr)
	}
	if strings.Contains(stop.Stdout, "LET ME COOK") {
		t.Fatalf("normal prompt must clear stale runLoop state before stop, got: %s", stop.Stdout)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.ActiveRunID != "old-session" {
		t.Fatalf("expected other session normal prompt to preserve active run, got %+v", state)
	}
}

func TestRunStopRunLoopToolUseClearsAwaitingContinuation(t *testing.T) {
	_ = os.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	gitInitHelper(t, repo)
	writeTaskFixture(t, repo)

	start := RunUserPromptSubmit(repo, []byte(`{"session_id":"ses_tool","prompt":"/runloop"}`))
	if start.ExitCode != 0 {
		t.Fatalf("user prompt: %s", start.Stderr)
	}
	first := RunStop(repo, []byte(`{"session_id":"ses_tool"}`))
	if !strings.Contains(first.Stdout, "LET ME COOK") {
		t.Fatalf("expected first stop to emit continuation prompt, got: %s", first.Stdout)
	}

	post := RunPostToolUse(repo, []byte(`{"session_id":"ses_tool","tool_name":"Read","tool_input":{"file_path":"docs/tasks.md"}}`))
	if post.ExitCode != 0 {
		t.Fatalf("post tool use: exit=%d stderr=%s", post.ExitCode, post.Stderr)
	}
	state, _ := loadRunLoopState(repo)
	if state.AwaitingContinuation {
		t.Fatal("expected tool use to clear awaiting_continuation")
	}
	second := RunStop(repo, []byte(`{"session_id":"ses_tool"}`))
	if !strings.Contains(second.Stdout, "LET ME COOK") {
		t.Fatalf("expected second stop after tool use to continue, got: %s", second.Stdout)
	}
	afterSecond, _ := loadRunLoopState(repo)
	if afterSecond.NoProgressNudges != 0 {
		t.Fatalf("tool progress must reset no-progress nudges, got %d", afterSecond.NoProgressNudges)
	}
}

func TestRunStopRunLoopIgnoresOtherSession(t *testing.T) {
	_ = os.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	gitInitHelper(t, repo)
	writeTaskFixture(t, repo)

	start := RunUserPromptSubmit(repo, []byte(`{"session_id":"cursor-run","prompt":"mach weiter /runloop mit Kontext"}`))
	if start.ExitCode != 0 {
		t.Fatalf("user prompt: %s", start.Stderr)
	}
	other := RunStop(repo, []byte(`{"session_id":"codex-side-chat"}`))
	if other.ExitCode != 0 {
		t.Fatalf("other stop: exit=%d stderr=%s", other.ExitCode, other.Stderr)
	}
	if strings.Contains(other.Stdout, "LET ME COOK") {
		t.Fatalf("other session stop must not emit active run continuation, got: %s", other.Stdout)
	}
	state, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.ActiveRunID != "cursor-run" {
		t.Fatalf("other session stop must preserve active run, got %+v", state)
	}
}

func TestRunLoopStopBlockJSON(t *testing.T) {
	prompt := "RUNLOOP TEST PROMPT"
	out := runLoopStopBlockJSON(prompt)
	if !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("expected decision=block, got: %s", out)
	}
	if !strings.Contains(out, prompt) {
		t.Fatalf("expected prompt in reason, got: %s", out)
	}
	// Must be valid JSON.
	if !strings.HasPrefix(out, "{") || !strings.HasSuffix(out, "}") {
		t.Fatalf("expected JSON object, got: %s", out)
	}
}

// helpers

func gitInitHelper(t *testing.T, repo string) {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "init", "--quiet")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// Create initial commit so rev-parse HEAD works.
	cmd = exec.Command("git", "-C", repo, "config", "user.email", "test@test")
	_ = cmd.Run()
	cmd = exec.Command("git", "-C", repo, "config", "user.name", "test")
	_ = cmd.Run()
	os.WriteFile(filepath.Join(repo, "initial"), []byte("init\n"), 0o644)
	cmd = exec.Command("git", "-C", repo, "add", "initial")
	_ = cmd.Run()
	cmd = exec.Command("git", "-C", repo, "commit", "-m", "init", "--quiet")
	_ = cmd.Run()
}

func writeTaskFixture(t *testing.T, repo string) {
	t.Helper()
	os.MkdirAll(filepath.Join(repo, "docs", "tasks"), 0o755)
	os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Test Agent Rules\n\nNo special rules.\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "docs", "tasks.md"),
		[]byte("Current: TASK-0017-Test-Task -> tasks/TASK-0017-Test-Task.md\n\n- [ ] TASK-0017-Test-Task - test -> tasks/TASK-0017-Test-Task.md\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "docs", "tasks", "TASK-0017-Test-Task.md"),
		[]byte("# TASK-0017-Test-Task\n\n## Status\n\nState: Active\n\n## Sub-Tasks\n\n- [~] Test sub-task\n- [ ] Next step\n"), 0o644)
}

func setupStopScriptPolicyRepo(t *testing.T, counterPath string, exitCode int, output string) string {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	gitInitHelper(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".reconc", "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf x >> %q\nprintf '%s\\n'\nexit %d\n", counterPath, output, exitCode)
	scriptPath := filepath.Join(repo, ".reconc", "scripts", "stop-gate.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := `rules:
  - id: stop-script-gate
    kind: require_script
    when_paths: ['src/**']
    script: '.reconc/scripts/stop-gate.sh'
    mode: block
    timeout_sec: 10
    message: stop script gate
`
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	return repo
}

func readCounter(t *testing.T, path string) int {
	t.Helper()
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(body)
}

func TestRunStopRunLoopNoProgressGuard(t *testing.T) {
	_ = os.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	gitInitHelper(t, repo)
	writeTaskFixture(t, repo)

	start := RunUserPromptSubmit(repo, []byte(`{"session_id":"ses_npg","prompt":"/runloop"}`))
	if start.ExitCode != 0 {
		t.Fatalf("user prompt: %s", start.Stderr)
	}

	// Deactivate runloop between calls via direct state mutation to
	// ensure RunStop always enters the runloop-enabled branch.
	activateDM := func() {
		_ = saveRunLoopState(repo, runLoopState{
			Enabled:     true,
			SessionID:   "ses_npg",
			ActiveRunID: "ses_npg",
		})
	}

	// Stop 1: first block, nudges=0
	activateDM()
	r1 := RunStop(repo, []byte(`{"session_id":"ses_npg"}`))
	if !strings.Contains(r1.Stdout, `"decision":"block"`) {
		t.Fatal("stop 1: expected block")
	}

	// Stop 2: same HEAD/TASK, nudges=1
	activateDM()
	r2 := RunStop(repo, []byte(`{"session_id":"ses_npg"}`))
	if !strings.Contains(r2.Stdout, `"decision":"block"`) {
		t.Fatal("stop 2: expected block")
	}

	// Stop 3: nudges=2 — still blocks
	activateDM()
	r3 := RunStop(repo, []byte(`{"session_id":"ses_npg"}`))
	if !strings.Contains(r3.Stdout, `"decision":"block"`) {
		t.Fatal("stop 3: expected block")
	}

	// Stop 4: nudges reaches 6 — disables with no_progress_guard
	activateDM()
	s, _ := loadRunLoopState(repo)
	s.NoProgressNudges = 5 // simulate previous stop nudges
	s.LastHead = readCurrentHead(repo)
	s.LastCurrent = readRunLoopProgressFingerprint(repo)
	s.AwaitingContinuation = true
	_ = saveRunLoopState(repo, s)

	r4 := RunStop(repo, []byte(`{"session_id":"ses_npg"}`))
	if r4.ExitCode != 0 {
		t.Fatalf("stop 4: exit=%d stderr=%s", r4.ExitCode, r4.Stderr)
	}
	if r4.Stdout != "" {
		t.Fatalf("stop 4: expected clean stop (no block), got: %s", r4.Stdout)
	}

	final, _ := loadRunLoopState(repo)
	if final.Enabled {
		t.Fatal("expected runloop disabled after no_progress_guard")
	}
	if final.DisabledReason != "no_progress_guard" {
		t.Fatalf("expected disabled_reason=no_progress_guard, got %q", final.DisabledReason)
	}
}
