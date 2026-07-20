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
	"reconc.dev/reconc/internal/runtime"
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
	t.Setenv("TMPDIR", t.TempDir())
	return repo
}

func TestRunStopFailsClosedOnEvidenceOverflow(t *testing.T) {
	_, repo := withStateRoot(t)
	if _, err := InitializeSessionState(repo, "overflow"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "overflow", func(state SessionState) SessionState {
		markEvidenceOverflow(&state, "write_paths")
		return state
	}); err != nil {
		t.Fatal(err)
	}
	result := RunStop(repo, []byte(`{"session_id":"overflow"}`))
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, `"decision":"block"`) || !strings.Contains(result.Stdout, "write_paths") {
		t.Fatalf("overflow stop did not fail closed: %+v", result)
	}
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
		{
			name:    "line continuation git clean",
			command: "git \\\nclean -fd",
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

func TestRunPreToolUseEnforcesPolicyForbidCommandBeforeExecution(t *testing.T) {
	repo := setupPolicyRepo(t)
	policyPath := filepath.Join(repo, "policies", "rules.yml")
	file, err := os.OpenFile(policyPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString("  - id: forbid-protected-rm\n    kind: forbid_command\n    command_match: prefix\n    commands: ['rm -f protected.txt']\n    mode: block\n    message: protected command\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append policy: write=%v close=%v", writeErr, closeErr)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile policy: %v", err)
	}

	blocked := `{"session_id":"s-policy-command","tool_name":"Bash","tool_input":{"command":"rm -f protected.txt --verbose"}}`
	if result := RunPreToolUse(repo, []byte(blocked)); result.ExitCode != 2 || !strings.Contains(result.Stderr, "forbid-protected-rm") {
		t.Fatalf("policy-forbidden command was not blocked before execution: %+v", result)
	}
	allowed := `{"session_id":"s-policy-command","tool_name":"Bash","tool_input":{"command":"rm -f other.txt"}}`
	if result := RunPreToolUse(repo, []byte(allowed)); result.ExitCode != 0 {
		t.Fatalf("unmatched command was blocked: %+v", result)
	}
}

func TestRunPreToolUseEnforcesConditionalForbidAfterMatchingWrite(t *testing.T) {
	repo := setupPolicyRepo(t)
	policyPath := filepath.Join(repo, "policies", "rules.yml")
	file, err := os.OpenFile(policyPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString("  - id: forbid-raw-pip-after-manifest\n    kind: forbid_command\n    command_match: prefix\n    when_paths: ['requirements.txt']\n    commands: ['pip install']\n    mode: block\n    message: use the canonical installer\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append policy: write=%v close=%v", writeErr, closeErr)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile policy: %v", err)
	}
	writePayload := `{"session_id":"s-conditional-command","tool_name":"Write","tool_input":{"file_path":"requirements.txt","content":"requests"}}`
	if result := RunPreToolUse(repo, []byte(writePayload)); result.ExitCode != 0 {
		t.Fatalf("manifest write pre-hook failed: %+v", result)
	}
	if result := RunPostToolUse(repo, []byte(writePayload)); result.ExitCode != 0 {
		t.Fatalf("manifest write post-hook failed: %+v", result)
	}
	commandPayload := `{"session_id":"s-conditional-command","tool_name":"Bash","tool_input":{"command":"echo ready && pip install requests"}}`
	if result := RunPreToolUse(repo, []byte(commandPayload)); result.ExitCode != 2 || !strings.Contains(result.Stderr, "forbid-raw-pip-after-manifest") {
		t.Fatalf("conditional forbidden command was not blocked before execution: %+v", result)
	}
}

func TestRunPreToolUseEnforcesCompositeForbidBeforeExecution(t *testing.T) {
	repo := setupPolicyRepo(t)
	policyPath := filepath.Join(repo, "policies", "rules.yml")
	file, err := os.OpenFile(policyPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString("  - id: composite-forbid\n    kind: all_of\n    when_paths: ['requirements.txt']\n    checks:\n      - kind: forbid_command\n        command_match: prefix\n        commands: ['pip install']\n    mode: block\n    message: use the canonical installer\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append policy: write=%v close=%v", writeErr, closeErr)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile policy: %v", err)
	}
	writePayload := `{"session_id":"s-composite-command","tool_name":"Write","tool_input":{"file_path":"requirements.txt","content":"requests"}}`
	if result := RunPostToolUse(repo, []byte(writePayload)); result.ExitCode != 0 {
		t.Fatalf("manifest write post-hook failed: %+v", result)
	}
	commandPayload := `{"session_id":"s-composite-command","tool_name":"Bash","tool_input":{"command":"echo ready && pip install requests"}}`
	if result := RunPreToolUse(repo, []byte(commandPayload)); result.ExitCode != 2 || !strings.Contains(result.Stderr, "composite-forbid") {
		t.Fatalf("composite forbidden command was not blocked before execution: %+v", result)
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
		`echo git clean -fd`,
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
		`printf '%s\n' '$(git clean -fd)'`,
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
		`printf '%s\n' x | xargs -n 1 sh -c 'git clean -fd'`,
		`echo "$(git reset --hard HEAD)"`,
		"echo `git clean -fd`",
	}
	for _, command := range blocked {
		if reason := forbiddenShellCommandReason(command); reason == "" {
			t.Fatalf("executable nested shell command %q should block", command)
		}
	}
	deep := "git clean -fd"
	for range maxShellGuardDepth + 2 {
		deep = "echo $(" + deep + ")"
	}
	if reason := forbiddenShellCommandReason(deep); reason == "" {
		t.Fatal("over-deep shell nesting must fail closed")
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

func TestRunPostToolUseCompleteClassifiesCommandOutcome(t *testing.T) {
	for _, test := range []struct {
		name        string
		response    string
		wantOutcome string
	}{
		{name: "success", response: `"tool_response":{"success":true}`, wantOutcome: "success"},
		{name: "top-level error", response: `"error":"command failed"`, wantOutcome: "failure"},
		{name: "non-zero exit", response: `"tool_response":{"exit_code":2}`, wantOutcome: "failure"},
		{name: "explicit unsuccessful response", response: `"tool_response":{"success":false}`, wantOutcome: "failure"},
		{name: "nested response error", response: `"tool_response":{"error":"command failed"}`, wantOutcome: "failure"},
		{name: "structured response error", response: `"tool_response":{"error":{"message":"command failed"}}`, wantOutcome: "failure"},
		{name: "null response error", response: `"tool_response":{"success":true,"error":null}`, wantOutcome: "success"},
		{name: "successful stderr", response: `"tool_response":{"success":true,"stderr":"warning"}`, wantOutcome: "success"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := setupPolicyRepo(t)
			_ = RunSessionStart(repo, []byte(`{"session_id":"complete"}`))
			payload := `{"session_id":"complete","tool_name":"Bash","tool_input":{"command":"go test ./..."},` + test.response + `}`
			result := RunPostToolUseComplete(repo, []byte(payload))
			if result.ExitCode != 0 {
				t.Fatalf("completed command observation must fail open: %+v", result)
			}
			state, err := LoadSessionState(repo, "complete")
			if err != nil {
				t.Fatal(err)
			}
			if len(state.CommandResults) != 1 || state.CommandResults[0].Outcome != test.wantOutcome {
				t.Fatalf("command results = %+v, want outcome %q", state.CommandResults, test.wantOutcome)
			}
		})
	}
}

func TestRunPostToolUseCompleteDoesNotRecordFailedWriteEvidence(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"failed-write"}`))
	payload := `{"session_id":"failed-write","tool_name":"Write","tool_input":{"file_path":"src/not-written.go"},"tool_response":{"success":false,"error":"write rejected"}}`
	result := RunPostToolUseComplete(repo, []byte(payload))
	if result.ExitCode != 0 {
		t.Fatalf("failed write observation must fail open: %+v", result)
	}
	state, err := LoadSessionState(repo, "failed-write")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.WritePaths) != 0 {
		t.Fatalf("failed write became successful evidence: %+v", state.WritePaths)
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

func TestRunStopHookActiveCleanCacheRejectsStaleFingerprint(t *testing.T) {
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
		t.Fatalf("reentrant stale-cache stop: exit=%d stdout=%q stderr=%s", second.ExitCode, second.Stdout, second.Stderr)
	}
	if got := readCounter(t, counterPath); got != 2 {
		t.Fatalf("reentrant stop must rerun policy when the fingerprint is stale, got %d script runs", got)
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

func TestRepeatedStopReasonCollapsesToStableFeedbackAndReport(t *testing.T) {
	violations := []runtime.Violation{{RuleID: "gate-one", RecommendedAction: "run the exact gate"}}
	first := stopReasonForViolations(violations, "/tmp/report.json", "RB-123456789abc", false, "")
	repeated := stopReasonForViolations(violations, "/tmp/report.json", "RB-123456789abc", true, "")
	for _, output := range []string{first, repeated} {
		if !strings.Contains(output, "Feedback: RB-123456789abc") || !strings.Contains(output, "Report: /tmp/report.json") {
			t.Fatalf("feedback is not stable and report-backed: %q", output)
		}
	}
	if len(repeated) >= len(first)+80 {
		t.Fatalf("repeated feedback unexpectedly expanded: first=%d repeated=%d", len(first), len(repeated))
	}
}

func TestCursorStopReleasesStopLoopOnRepeatedBlockEndToEnd(t *testing.T) {
	// End-to-end reproduction of the "repository run/Cursor cannot be stopped" bug
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

func TestStrictContinuationDoesNotReleaseRepeatedBlockingStop(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 2, "unresolved blocker")
	if _, err := InitializeSessionState(repo, "grok-strict"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "grok-strict", func(state SessionState) SessionState {
		return AppendWritePath(state, "src/a.go")
	}); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"session_id":"grok-strict","reconc_runtime":"grok-acp","strict_continuation":true}`)
	first := RunStop(repo, payload)
	second := RunStop(repo, payload)
	for index, result := range []Result{first, second} {
		if !strings.Contains(result.Stdout, `"decision":"block"`) {
			t.Fatalf("strict stop %d was released: %+v", index+1, result)
		}
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
	if strings.Contains(result.Stdout, "Reconc run is ON") {
		t.Fatalf("normal stop without repository run must not emit a continuation prompt, got: %s", result.Stdout)
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

func TestRepositoryRunBlockJSON(t *testing.T) {
	prompt := "REPOSITORY RUN TEST PROMPT"
	out := repositoryRunBlockJSON(prompt)
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
		[]byte("# TASK-0017-Test-Task\n\n## Why\n\nExercise run control.\n\n## Status\n\nState: Active\n\n## Scheduling\n\n- Depends On: none\n\n## Technical Plan\n\nExercise the real hook path.\n\n## Acceptance\n\n- The hook continues.\n\n## Sub-Tasks\n\n- [~] Test sub-task\n- [ ] Next step\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n"), 0o644)
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("refresh task fixture policy: %v", err)
	}
}

func setupStopScriptPolicyRepo(t *testing.T, counterPath string, exitCode int, output string) string {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv(StateRootEnv, t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	repo := t.TempDir()
	gitInitHelper(t, repo)
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(repo, "src", name), []byte("package src\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
