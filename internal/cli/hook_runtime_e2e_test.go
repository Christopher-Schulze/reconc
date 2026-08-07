package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

// runWithStdin invokes cli.Run with a stdin string piped in via
// temporary os.Stdin replacement. Captures stdout + stderr + exit
// code. Covers the full dispatcher path from argv parsing through
// the agent-session handlers down to the evaluator.
//
// End-to-end by construction: if Run() or any inner layer swaps its
// contract, one of the scenarios below will catch it.
func runWithStdin(t *testing.T, stdin string, argv ...string) (stdoutStr, stderrStr string, exitCode int) {
	t.Helper()
	// Replace os.Stdin with a pipe that delivers the given payload.
	// Run() reads from os.Stdin directly for hook-runtime, so this
	// is the minimum viable integration harness.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin; _ = r.Close() }()

	// Write then close the write end so ReadAll returns.
	go func() {
		if stdin != "" {
			_, _ = w.WriteString(stdin)
		}
		_ = w.Close()
	}()

	var stdout, stderr bytes.Buffer
	err = Run(argv, "0.5.0-e2e", &stdout, &stderr)
	code := ExitCode(err)
	return stdout.String(), stderr.String(), code
}

// bootstrapE2ERepo creates a tmp repo with a known ruleset and
// compiles it. Returns the canonicalised repo path so the state
// adapter sees the same value the lockfile was stamped with.
func bootstrapE2ERepo(t *testing.T) string {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	// Isolate agentsession state too so tests don't share session data.
	t.Setenv("RECONC_CLAUDE_STATE_DIR", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())

	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := `rules:
  - id: deny-gen
    kind: deny_write
    paths: ['generated/**']
    mode: block
    message: generated dir is read-only
  - id: need-ci
    kind: require_claim
    when_paths: ['src/**']
    claims: ['ci-green']
    mode: block
    message: need ci-green before src writes
`
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "e2e"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	// Use the symlink-resolved path so both compile and handlers agree,
	// even when a test environment has an unusual temp setup.
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		return resolved
	}
	return repo
}

// --- Scenario 1: happy path ----------------------------------------

func TestHookRuntimeHappyPath(t *testing.T) {
	repo := bootstrapE2ERepo(t)

	// SessionStart.
	_, _, code := runWithStdin(t, `{"session_id":"s1"}`,
		"hook", "runtime", "claude-session-start", repo)
	if code != 0 {
		t.Fatalf("SessionStart should exit 0, got %d", code)
	}

	// PreToolUse: legit write.
	_, _, code = runWithStdin(t, `{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"docs/x.md"}}`,
		"hook", "runtime", "claude-pre-tool-use", repo)
	if code != 0 {
		t.Errorf("PreToolUse legit should exit 0, got %d", code)
	}

	// PostToolUse: record evidence.
	_, _, code = runWithStdin(t, `{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"docs/x.md"}}`,
		"hook", "runtime", "claude-post-tool-use", repo)
	if code != 0 {
		t.Errorf("PostToolUse should exit 0, got %d", code)
	}

	// Stop: nothing blocking (no src/** writes -> require_claim not triggered).
	_, _, code = runWithStdin(t, `{"session_id":"s1"}`,
		"hook", "runtime", "claude-stop", repo)
	if code != 0 {
		t.Errorf("Stop should exit 0, got %d", code)
	}

	// SessionEnd.
	_, _, code = runWithStdin(t, `{"session_id":"s1"}`,
		"hook", "runtime", "claude-session-end", repo)
	if code != 0 {
		t.Errorf("SessionEnd should exit 0, got %d", code)
	}
}

func TestHookRuntimeBlocksDenyWrite(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"session_id":"s2"}`,
		"hook", "runtime", "claude-session-start", repo)

	stdout, stderr, code := runWithStdin(t,
		`{"session_id":"s2","tool_name":"Write","tool_input":{"file_path":"generated/evil.go"}}`,
		"hook", "runtime", "claude-pre-tool-use", repo)
	if code != 2 {
		t.Errorf("expected exit 2 for deny_write hit, got %d (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "deny-gen") {
		t.Errorf("stderr should cite rule id, got: %s", stderr)
	}
	if !strings.Contains(stderr, "reconc blocked") {
		t.Errorf("stderr should announce block, got: %s", stderr)
	}
}

func writeHookRuntimeTaskFixture(t *testing.T, repo string) {
	t.Helper()
	tasksDir := filepath.Join(repo, "docs", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tasks := `# Tasks

Current: TASK-0001-Repository-Run-Test -> tasks/TASK-0001-Repository-Run-Test.md

- [ ] TASK-0001-Repository-Run-Test - Exercise repository continuation -> tasks/TASK-0001-Repository-Run-Test.md
`
	if err := os.WriteFile(filepath.Join(repo, "docs", "tasks.md"), []byte(tasks), 0o644); err != nil {
		t.Fatal(err)
	}
	detail := `# TASK-0001-Repository-Run-Test

## Why

Exercise the real runtime continuation path.

## Status

State: Active

## Scheduling

- Depends On: none

## Technical Plan

Drive the installed hook through the public CLI.

## Acceptance

- The active TASK continues.

## Sub-Tasks

- [~] Continue the active task

## Notes

None.

## Deviations

None.
`
	if err := os.WriteFile(filepath.Join(tasksDir, "TASK-0001-Repository-Run-Test.md"), []byte(detail), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- Scenario 3: Stop blocks on missing claim ----------------------

func TestHookRuntimeStopBlocksOnMissingClaim(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"session_id":"s3"}`,
		"hook", "runtime", "claude-session-start", repo)
	_, _, _ = runWithStdin(t,
		`{"session_id":"s3","tool_name":"Write","tool_input":{"file_path":"src/app.go"}}`,
		"hook", "runtime", "claude-post-tool-use", repo)

	stdout, _, code := runWithStdin(t, `{"session_id":"s3"}`,
		"hook", "runtime", "claude-stop", repo)
	// Stop always exit 0; the block is communicated via JSON stdout.
	if code != 0 {
		t.Errorf("Stop exit code must be 0 even when blocking, got %d", code)
	}
	if !strings.Contains(stdout, `"decision":"block"`) {
		t.Errorf("Stop should emit decision=block JSON, got: %s", stdout)
	}
	if !strings.Contains(stdout, "need-ci") {
		t.Errorf("Stop JSON should cite rule id 'need-ci', got: %s", stdout)
	}
}

// --- Scenario 4: claim satisfies Stop ------------------------------

func TestHookRuntimeClaimSatisfiesStop(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"session_id":"s4"}`,
		"hook", "runtime", "claude-session-start", repo)
	_, _, _ = runWithStdin(t,
		`{"session_id":"s4","tool_name":"Write","tool_input":{"file_path":"src/app.go"}}`,
		"hook", "runtime", "claude-post-tool-use", repo)

	// `reconc hook claim` asserts the ci-green claim.
	stdout, _, code := runWithStdin(t, "",
		"hook", "claim", repo, "ci-green")
	if code != 0 {
		t.Fatalf("hook claim should succeed, got %d / %s", code, stdout)
	}
	if !strings.Contains(stdout, "ci-green") {
		t.Errorf("claim confirmation should echo name, got: %s", stdout)
	}

	// Stop now passes silently.
	stdout, _, code = runWithStdin(t, `{"session_id":"s4"}`,
		"hook", "runtime", "claude-stop", repo)
	if code != 0 {
		t.Errorf("Stop should pass after claim, got %d", code)
	}
	if stdout != "" {
		t.Errorf("clean Stop should produce no stdout, got: %s", stdout)
	}
}

// --- Scenario 5: malformed PreToolUse payload is fail-closed --------

func TestHookRuntimeFailClosedOnMalformedPre(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"session_id":"s5"}`,
		"hook", "runtime", "claude-session-start", repo)

	_, stderr, code := runWithStdin(t, `{not json`,
		"hook", "runtime", "claude-pre-tool-use", repo)
	if code != 2 {
		t.Errorf("malformed PreToolUse payload should fail-closed (exit 2), got %d", code)
	}
	if !strings.Contains(stderr, "reconc hook (pre)") {
		t.Errorf("stderr should identify the event handler, got: %s", stderr)
	}
}

// --- Scenario 6: malformed PostToolUse payload is fail-open --------

func TestHookRuntimeFailOpenOnMalformedPost(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"session_id":"s6"}`,
		"hook", "runtime", "claude-session-start", repo)

	_, stderr, code := runWithStdin(t, `{not json`,
		"hook", "runtime", "claude-post-tool-use", repo)
	if code != 0 {
		t.Errorf("malformed PostToolUse should fail-open (exit 0), got %d", code)
	}
	if !strings.Contains(stderr, "warn") {
		t.Errorf("stderr should announce warn-level failure, got: %s", stderr)
	}
}

func TestHookRuntimeSessionStartHonorsRegistryFailOpenPolicy(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, stderr, code := runWithStdin(t, `{}`,
		"hook", "runtime", "claude-session-start", repo)
	if code != 0 {
		t.Fatalf("SessionStart handler errors must fail open, got %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "session_id") {
		t.Fatalf("SessionStart diagnostic missing: %s", stderr)
	}
}

// --- Scenario 7: PostToolUseFailure records failure + warns --------

func TestHookRuntimePostToolUseFailure(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"session_id":"s7"}`,
		"hook", "runtime", "claude-session-start", repo)

	payload := `{"session_id":"s7","tool_name":"Bash","tool_input":{"command":"go test"},"tool_response":{"exit_code":1},"error":"test failed"}`
	stdout, _, code := runWithStdin(t, payload,
		"hook", "runtime", "claude-post-tool-use-failure", repo)
	if code != 0 {
		t.Errorf("PostToolUseFailure should exit 0, got %d", code)
	}
	if !strings.Contains(stdout, "failed command") {
		t.Errorf("stdout should mention failed command, got: %s", stdout)
	}
	if !strings.Contains(stdout, "require_command_success") {
		t.Errorf("stdout should mention rule kind that's affected, got: %s", stdout)
	}
}

// --- Scenario 8: Codex dispatch parity (same handlers) -------------

func TestHookRuntimeCodexDispatch(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, code := runWithStdin(t, `{"session_id":"cx1"}`,
		"hook", "runtime", "codex-session-start", repo)
	if code != 0 {
		t.Errorf("codex-session-start should exit 0, got %d", code)
	}

	// Codex PreToolUse allows safe Bash commands while still applying
	// command-level destructive-operation guards.
	_, _, code = runWithStdin(t,
		`{"session_id":"cx1","tool_name":"Bash","tool_input":{"command":"ls"}}`,
		"hook", "runtime", "codex-pre-tool-use", repo)
	if code != 0 {
		t.Errorf("codex-pre-tool-use on Bash should exit 0, got %d", code)
	}

	// Codex accepts SessionEnd in its hook config, so the route runs and
	// releases the session like every other host that publishes the event.
	_, _, code = runWithStdin(t, `{"session_id":"cx1"}`,
		"hook", "runtime", "codex-session-end", repo)
	if code != 0 {
		t.Errorf("native codex-session-end should be accepted, got exit %d", code)
	}
}

func TestHookRuntimeCursorPreToolUseBlocksDenyWrite(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, code := runWithStdin(t, `{"conversation_id":"cur1"}`,
		"hook", "runtime", "cursor-session-start", repo)
	if code != 0 {
		t.Fatalf("cursor-session-start should exit 0, got %d", code)
	}

	stdout, stderr, code := runWithStdin(t,
		`{"conversation_id":"cur1","tool_name":"Write","tool_input":{"filePath":"generated/evil.go"}}`,
		"hook", "runtime", "cursor-pre-tool-use", repo)
	if code != 0 {
		t.Fatalf("Cursor pre-tool denial must be returned as JSON exit 0, got %d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"permission":"deny"`) || !strings.Contains(stdout, "deny-gen") {
		t.Fatalf("expected Cursor permission deny with rule id, got stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestHookRuntimeCursorStopUsesFollowupMessage(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"conversation_id":"cur2"}`,
		"hook", "runtime", "cursor-session-start", repo)
	_, _, _ = runWithStdin(t,
		`{"conversation_id":"cur2","filePath":"src/app.go"}`,
		"hook", "runtime", "cursor-after-file-edit", repo)

	stdout, _, code := runWithStdin(t, `{"conversation_id":"cur2"}`,
		"hook", "runtime", "cursor-stop", repo)
	if code != 0 {
		t.Fatalf("Cursor stop should exit 0, got %d", code)
	}
	if !strings.Contains(stdout, `"followup_message"`) || !strings.Contains(stdout, "need-ci") {
		t.Fatalf("expected Cursor followup block with rule id, got %q", stdout)
	}
	if strings.Contains(stdout, `"decision":"block"`) {
		t.Fatalf("Cursor stop must not emit Claude/Codex decision schema, got %q", stdout)
	}
}

func TestHookRuntimeCursorUnsupportedBeforeReadDoesNotSatisfyReadEvidence(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"conversation_id":"cur-read"}`,
		"hook", "runtime", "cursor-session-start", repo)

	_, _, code := runWithStdin(t,
		`{"conversation_id":"cur-read","filePath":"docs/tasks.md"}`,
		"hook", "runtime", "cursor-before-read-file", repo)
	if code != 1 {
		t.Fatalf("unsupported Cursor before-read route should be rejected, got %d", code)
	}
	state, err := agentsession.LoadSessionState(repo, "cur-read")
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if len(state.ReadPaths) != 0 {
		t.Fatalf("before-read must not record successful read evidence, got %#v", state.ReadPaths)
	}

	_, _, code = runWithStdin(t,
		`{"conversation_id":"cur-read","tool_name":"Read","tool_input":{"filePath":"docs/tasks.md"}}`,
		"hook", "runtime", "cursor-post-tool-use", repo)
	if code != 0 {
		t.Fatalf("Cursor post-tool read should pass, got %d", code)
	}
	state, err = agentsession.LoadSessionState(repo, "cur-read")
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if len(state.ReadPaths) != 1 || state.ReadPaths[0] != "docs/tasks.md" {
		t.Fatalf("post-tool read should record evidence, got %#v", state.ReadPaths)
	}
}

func TestHookRuntimeCursorSuccessFailureAndPassiveShellAreSeparated(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"conversation_id":"cur3"}`,
		"hook", "runtime", "cursor-session-start", repo)

	_, _, code := runWithStdin(t,
		`{"conversation_id":"cur3","tool_name":"Shell","tool_input":{"command":"go test ./..."}}`,
		"hook", "runtime", "cursor-post-tool-use", repo)
	if code != 0 {
		t.Fatalf("Cursor post-tool success should pass, got %d", code)
	}

	stdout, _, code := runWithStdin(t,
		`{"conversation_id":"cur3","tool_name":"Shell","tool_input":{"command":"go test ./..."},"tool_use_id":"tool-3","error_message":"failed","failure_type":"error","is_interrupt":false}`,
		"hook", "runtime", "cursor-post-tool-use-failure", repo)
	if code != 0 || strings.TrimSpace(stdout) != "{}" {
		t.Fatalf("Cursor native failure should be outputless and fail-open, code=%d stdout=%q", code, stdout)
	}

	stdout, _, code = runWithStdin(t,
		`{"conversation_id":"cur3","command":"go test ./...","output":"failed","duration":1200,"sandbox":{"enabled":true}}`,
		"hook", "runtime", "cursor-after-shell-execution", repo)
	if code != 0 || strings.TrimSpace(stdout) != "{}" {
		t.Fatalf("Cursor after-shell observation should be outputless and fail-open, code=%d stdout=%q", code, stdout)
	}

	state, err := agentsession.LoadSessionState(repo, "cur3")
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if len(state.CommandResults) != 2 ||
		state.CommandResults[0].Outcome != "success" ||
		state.CommandResults[1].Outcome != "failure" ||
		state.CommandResults[1].ToolUseID != "tool-3" {
		t.Fatalf("expected one native success and one native failure, got %#v", state.CommandResults)
	}
}

func TestHookRuntimeCursorGenericAndSpecializedWritesDeduplicate(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"conversation_id":"cur-dedup"}`,
		"hook", "runtime", "cursor-session-start", repo)

	_, _, code := runWithStdin(t,
		`{"conversation_id":"cur-dedup","tool_name":"Write","tool_input":{"filePath":"src/app.go"}}`,
		"hook", "runtime", "cursor-post-tool-use", repo)
	if code != 0 {
		t.Fatalf("Cursor generic post-tool write should pass, got %d", code)
	}
	_, _, code = runWithStdin(t,
		`{"conversation_id":"cur-dedup","file_path":"src/app.go"}`,
		"hook", "runtime", "cursor-after-file-edit", repo)
	if code != 0 {
		t.Fatalf("Cursor specialized file-edit write should pass, got %d", code)
	}

	state, err := agentsession.LoadSessionState(repo, "cur-dedup")
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if len(state.WritePaths) != 1 || state.EvidenceEpoch != 1 || state.WriteEpochs["src/app.go"] != 1 {
		t.Fatalf("duplicate write changed evidence more than once: %#v", state)
	}
}

func TestHookRuntimeCursorSubagentCompactionAndMCPRoutes(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	mcpConfig := `mcp:
  unclassified: deny
  tools:
    - platform: cursor
      server_fingerprint: sha256:03b6bdb2ea9df5d8c3323186b7057404da8faff057c885f12a56271255d5448f
      tool: write_repo
      effect: repository_write
      path_fields: [/path]
`
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(mcpConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "e2e"); err != nil {
		t.Fatal(err)
	}

	stdout, _, code := runWithStdin(t,
		`{"conversation_id":"cur-parent","subagent_id":"cur-child","hook_event_name":"subagentStart"}`,
		"hook", "runtime", "cursor-subagent-start", repo)
	if code != 0 || !strings.Contains(stdout, `"permission":"allow"`) {
		t.Fatalf("Cursor subagent start code=%d stdout=%q", code, stdout)
	}
	if _, err := agentsession.MutateSessionState(repo, "cur-child", func(state agentsession.SessionState) agentsession.SessionState {
		return agentsession.AppendWritePath(state, "src/subagent.go")
	}); err != nil {
		t.Fatal(err)
	}
	stdout, _, code = runWithStdin(t,
		`{"conversation_id":"cur-parent","subagent_id":"cur-child","hook_event_name":"subagentStop","loop_count":0}`,
		"hook", "runtime", "cursor-subagent-stop", repo)
	if code != 0 || !strings.Contains(stdout, "followup_message") {
		t.Fatalf("Cursor subagent stop code=%d stdout=%q", code, stdout)
	}
	stdout, _, code = runWithStdin(t,
		`{"conversation_id":"cur-parent","hook_event_name":"preCompact"}`,
		"hook", "runtime", "cursor-pre-compaction", repo)
	if code != 0 || strings.TrimSpace(stdout) != "{}" {
		t.Fatalf("Cursor pre-compaction code=%d stdout=%q", code, stdout)
	}

	stdout, _, code = runWithStdin(t,
		`{"conversation_id":"cur-parent","tool_name":"write_repo","tool_input":"{\"path\":\"generated/out.go\"}","url":"https://example.invalid/mcp"}`,
		"hook", "runtime", "cursor-before-mcp-execution", repo)
	if code != 0 || !strings.Contains(stdout, `"permission":"deny"`) {
		t.Fatalf("Cursor MCP protected write code=%d stdout=%q", code, stdout)
	}
	stdout, _, code = runWithStdin(t,
		`{"conversation_id":"cur-parent","tool_name":"write_repo","tool_input":"{\"path\":\"src/mcp.go\"}","url":"https://example.invalid/mcp"}`,
		"hook", "runtime", "cursor-before-mcp-execution", repo)
	if code != 0 || !strings.Contains(stdout, `"permission":"allow"`) {
		t.Fatalf("Cursor MCP safe write pre code=%d stdout=%q", code, stdout)
	}
	stdout, _, code = runWithStdin(t,
		`{"conversation_id":"cur-parent","tool_name":"write_repo","tool_input":"{\"path\":\"src/mcp.go\"}","tool_response":"{\"isError\":false}","url":"https://example.invalid/mcp"}`,
		"hook", "runtime", "cursor-after-mcp-execution", repo)
	if code != 0 || strings.TrimSpace(stdout) != "{}" {
		t.Fatalf("Cursor MCP safe write post code=%d stdout=%q", code, stdout)
	}
	state, err := agentsession.LoadSessionState(repo, "cur-parent")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(state.WritePaths, "src/mcp.go") {
		t.Fatalf("Cursor MCP write evidence = %#v", state.WritePaths)
	}
}

func TestHookRuntimeCursorPromptAndWorkspaceUseNativeContracts(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	stdout, stderr, code := runWithStdin(t,
		`{"conversation_id":"cur-prompt","hook_event_name":"beforeSubmitPrompt","prompt":"continue"}`,
		"hook", "runtime", "cursor-user-prompt-submit", repo)
	if code != 0 || stderr != "" || strings.TrimSpace(stdout) != `{"continue":true}` {
		t.Fatalf("Cursor prompt contract code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	stdout, stderr, code = runWithStdin(t,
		fmt.Sprintf(`{"hook_event_name":"workspaceOpen","cursor_version":"3.13.21","workspace_roots":[%q],"user_email":"user@example.invalid"}`, repo),
		"hook", "runtime", "cursor-workspace-open", repo)
	if code != 0 || stderr != "" || strings.TrimSpace(stdout) != `{}` {
		t.Fatalf("Cursor workspace contract code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	liveness, err := agentsession.ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, observed := liveness["cursor"].Routes["cursor-workspace-open"]; !observed {
		t.Fatalf("Cursor workspace liveness = %#v", liveness["cursor"])
	}
	state, err := agentsession.LoadSessionState(repo, "cursor-workspace")
	if err != nil {
		t.Fatal(err)
	}
	if state.EvidenceEpoch != 0 || len(state.ReadPaths) != 0 || len(state.WritePaths) != 0 {
		t.Fatalf("Cursor workspace liveness mutated repository evidence: %#v", state)
	}
}

func TestHookRuntimeCursorMalformedObservationAddsNoEvidence(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, stderr, code := runWithStdin(t,
		`{"tool_name":"Write","tool_input":{"filePath":"src/app.go"}}`,
		"hook", "runtime", "cursor-post-tool-use", repo)
	if code != 0 || !strings.Contains(stderr, "session identity") {
		t.Fatalf("malformed Cursor observation must warn and fail open, code=%d stderr=%q", code, stderr)
	}
	state, err := agentsession.LoadSessionState(repo, "cursor-workspace")
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if len(state.WritePaths) != 0 || len(state.CommandResults) != 0 || state.EvidenceEpoch != 0 {
		t.Fatalf("malformed Cursor observation created fallback session evidence: %#v", state)
	}
}

func TestHookRuntimeOpenCodeAndKiloRequireAuthoritativeShellOutcome(t *testing.T) {
	for _, prefix := range []string{"opencode", "kilo"} {
		t.Run(prefix, func(t *testing.T) {
			repo := bootstrapE2ERepo(t)
			sessionID := prefix + "-shell"
			_, _, code := runWithStdin(t, `{"session_id":"`+sessionID+`"}`,
				"hook", "runtime", prefix+"-session-start", repo)
			if code != 0 {
				t.Fatalf("%s session start = %d", prefix, code)
			}

			payload := `{"session_id":"` + sessionID + `","tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":2,"success":false}}`
			_, _, code = runWithStdin(t, payload,
				"hook", "runtime", prefix+"-post-tool-use", repo)
			if code != 0 {
				t.Fatalf("%s failed command observation = %d", prefix, code)
			}

			unknown := `{"session_id":"` + sessionID + `","tool_name":"Bash","tool_input":{"command":"make lint"},"tool_response":{"success":true}}`
			_, _, code = runWithStdin(t, unknown,
				"hook", "runtime", prefix+"-post-tool-use", repo)
			if code != 0 {
				t.Fatalf("%s unknown command observation = %d", prefix, code)
			}

			state, err := agentsession.LoadSessionState(repo, sessionID)
			if err != nil {
				t.Fatalf("LoadSessionState: %v", err)
			}
			if len(state.Commands) != 0 {
				t.Fatalf("%s unsuccessful commands became positive evidence: %#v", prefix, state.Commands)
			}
			if len(state.CommandResults) != 2 ||
				state.CommandResults[0].Outcome != "failure" ||
				state.CommandResults[1].Outcome != "failure" ||
				!strings.Contains(state.CommandResults[1].Error, "missing authoritative") {
				t.Fatalf("%s command outcomes = %#v", prefix, state.CommandResults)
			}
		})
	}
}

func TestHookRuntimeAntigravityPreToolUseBlocksDenyWrite(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	stdout, stderr, code := runWithStdin(t,
		`{"conversationId":"ag-e2e","stepIdx":1,"toolCall":{"name":"write_to_file","args":{"TargetFile":"generated/evil.go"}}}`,
		"hook", "runtime", "antigravity-pre-tool-use", repo)
	if code != 0 {
		t.Fatalf("Antigravity pre-tool denial must be returned as JSON exit 0, got %d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"decision":"deny"`) || !strings.Contains(stdout, "deny-gen") {
		t.Fatalf("expected Antigravity deny with rule id, got stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestHookRuntimeAntigravityPostToolUseRecordsPendingEvidence(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, stderr, code := runWithStdin(t,
		`{"conversationId":"ag-read","stepIdx":2,"toolCall":{"name":"view_file","args":{"AbsolutePath":"docs/tasks.md"}}}`,
		"hook", "runtime", "antigravity-pre-tool-use", repo)
	if code != 0 {
		t.Fatalf("Antigravity pre read should pass, got %d stderr=%q", code, stderr)
	}
	_, stderr, code = runWithStdin(t,
		`{"conversationId":"ag-read","stepIdx":2,"error":""}`,
		"hook", "runtime", "antigravity-post-tool-use", repo)
	if code != 0 {
		t.Fatalf("Antigravity post read should pass, got %d stderr=%q", code, stderr)
	}
	state, err := agentsession.LoadSessionState(repo, "ag-read")
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if len(state.ReadPaths) != 1 || state.ReadPaths[0] != "docs/tasks.md" {
		t.Fatalf("expected read evidence, got %#v", state.ReadPaths)
	}
}

func TestHookRuntimeAntigravityStopUsesContinueSchema(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t,
		`{"conversationId":"ag-stop","stepIdx":3,"toolCall":{"name":"write_to_file","args":{"TargetFile":"src/app.go"}}}`,
		"hook", "runtime", "antigravity-pre-tool-use", repo)
	_, _, _ = runWithStdin(t,
		`{"conversationId":"ag-stop","stepIdx":3,"error":""}`,
		"hook", "runtime", "antigravity-post-tool-use", repo)

	stdout, _, code := runWithStdin(t, `{"conversationId":"ag-stop","executionNum":1,"terminationReason":"model_stop","fullyIdle":true}`,
		"hook", "runtime", "antigravity-stop", repo)
	if code != 0 {
		t.Fatalf("Antigravity stop should exit 0, got %d", code)
	}
	if !strings.Contains(stdout, `"decision":"continue"`) || !strings.Contains(stdout, "need-ci") {
		t.Fatalf("expected Antigravity continue stop with rule id, got %q", stdout)
	}
	if strings.Contains(stdout, `"decision":"block"`) {
		t.Fatalf("Antigravity stop must not emit Claude/Codex block schema, got %q", stdout)
	}
}

// --- Scenario 9: Stop with stop_hook_active avoids loops ----------

func TestHookRuntimeStopLoopGuard(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"session_id":"s9"}`,
		"hook", "runtime", "claude-session-start", repo)
	_, _, _ = runWithStdin(t,
		`{"session_id":"s9","tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`,
		"hook", "runtime", "claude-post-tool-use", repo)

	// With stop_hook_active=true, Stop must not emit block JSON
	// (prevents Claude from ping-ponging the same violation forever).
	stdout, _, code := runWithStdin(t,
		`{"session_id":"s9","stop_hook_active":true}`,
		"hook", "runtime", "claude-stop", repo)
	if code != 0 {
		t.Errorf("Stop with stop_hook_active must exit 0, got %d", code)
	}
	if stdout != "" {
		t.Errorf("Stop with stop_hook_active must suppress block JSON, got: %s", stdout)
	}
}

// --- Scenario 10: unknown event -----------------------------------

func TestHookRuntimeUnknownEventRejected(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, code := runWithStdin(t, `{"session_id":"s10"}`,
		"hook", "runtime", "not-a-real-event", repo)
	if code != 1 {
		t.Errorf("unknown event should exit 1, got %d", code)
	}
}

// --- Scenario 11: payload cap enforced in CLI path ----------------

func TestHookRuntimePayloadSizeLimit(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"session_id":"s11"}`,
		"hook", "runtime", "claude-session-start", repo)

	big := strings.Repeat("x", agentsession.MaxPayloadBytes+1)
	_, _, code := runWithStdin(t, big,
		"hook", "runtime", "claude-pre-tool-use", repo)
	if code != 2 {
		t.Errorf("payload above the %d-byte cap should fail-closed exit 2 for PreToolUse, got %d", agentsession.MaxPayloadBytes, code)
	}
}

// A stray DEVIN_PROJECT_DIR must not silently no-op non-Devin routes
// when no first-class Devin hooks are installed in the repository.
func TestHookRuntimeDevinEnvDoesNotDisableClaudeRouteWithoutDevinHooks(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	t.Setenv("DEVIN_PROJECT_DIR", "/somewhere/else")

	payload := `{"session_id":"ses_devenv1","tool_name":"Write","tool_input":{"file_path":"generated/out.txt"}}`
	_, stderrStr, code := runWithStdin(t, payload, "hook", "runtime", "claude-pre-tool-use", repo)
	if code != 2 {
		t.Fatalf("deny_write must still block with stray Devin env and no .devin hooks; code=%d stderr=%s", code, stderrStr)
	}
}

// With first-class Devin hooks installed, the duplicate Claude route is
// deduplicated and says so on stderr instead of silently exiting.
func TestHookRuntimeDevinDedupIsVisibleWhenDevinHooksInstalled(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	t.Setenv("DEVIN_PROJECT_DIR", repo)
	if err := os.MkdirAll(filepath.Join(repo, ".devin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".devin", "hooks.v1.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := `{"session_id":"ses_devenv2","tool_name":"Write","tool_input":{"file_path":"generated/out.txt"}}`
	_, stderrStr, code := runWithStdin(t, payload, "hook", "runtime", "claude-pre-tool-use", repo)
	if code != 0 {
		t.Fatalf("dedup to first-class Devin route should exit 0, got %d", code)
	}
	if !strings.Contains(stderrStr, "deduplicated") {
		t.Fatalf("dedup must be visible on stderr, got: %q", stderrStr)
	}
}

// An apply_patch payload whose patch parses to zero file operations
// must fail closed instead of silently ungating the write.
func TestHookRuntimeApplyPatchWithoutParseablePathsFailsClosed(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	payload := `{"session_id":"ses_patch1","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Renamed File: a.txt\n*** End Patch"}}`
	_, stderrStr, code := runWithStdin(t, payload, "hook", "runtime", "codex-pre-tool-use", repo)
	if code != 2 {
		t.Fatalf("unparseable apply_patch must fail closed; code=%d stderr=%s", code, stderrStr)
	}
	if !strings.Contains(stderrStr, "no parseable file operations") {
		t.Fatalf("expected parse-failure explanation, got: %q", stderrStr)
	}
}

// TestHookRuntimeClaudeAndCodexNamespacedMCPRoutes drives the complete path an
// MCP call takes on the two hosts that publish no dedicated MCP event: the host
// fires its generic tool event under the `mcp__<server>__<tool>` namespace, the
// runtime derives the exact identity, and the repository policy decides.
func TestHookRuntimeClaudeAndCodexNamespacedMCPRoutes(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	mcpConfig := `mcp:
  unclassified: deny
  tools:
    - platform: claude-code
      tool: mcp__filesystem__write_file
      effect: repository_write
      path_fields: [/path]
    - platform: codex
      tool: mcp__filesystem__write_file
      effect: repository_write
      path_fields: [/path]
`
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(mcpConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "e2e"); err != nil {
		t.Fatal(err)
	}

	_, _, code := runWithStdin(t, `{"session_id":"mcp1"}`, "hook", "runtime", "claude-session-start", repo)
	if code != 0 {
		t.Fatalf("claude-session-start exit %d", code)
	}

	// A protected path is refused before the MCP server ever runs.
	_, stderr, code := runWithStdin(t,
		`{"session_id":"mcp1","tool_name":"mcp__filesystem__write_file","tool_input":{"path":"generated/out.go"}}`,
		"hook", "runtime", "claude-mcp-before", repo)
	if code != 2 {
		t.Fatalf("protected MCP write must fail closed, got exit %d stderr=%q", code, stderr)
	}

	// An allowed path passes and its post event records the write.
	_, stderr, code = runWithStdin(t,
		`{"session_id":"mcp1","tool_name":"mcp__filesystem__write_file","tool_input":{"path":"docs/mcp.md"}}`,
		"hook", "runtime", "claude-mcp-before", repo)
	if code != 0 {
		t.Fatalf("allowed MCP write exit %d stderr=%q", code, stderr)
	}
	_, stderr, code = runWithStdin(t,
		`{"session_id":"mcp1","tool_name":"mcp__filesystem__write_file","tool_input":{"path":"docs/mcp.md"},"tool_response":{"isError":false}}`,
		"hook", "runtime", "claude-mcp-after", repo)
	if code != 0 {
		t.Fatalf("allowed MCP write post exit %d stderr=%q", code, stderr)
	}
	state, err := agentsession.LoadSessionState(repo, "mcp1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(state.WritePaths, "docs/mcp.md") {
		t.Fatalf("MCP write evidence = %#v", state.WritePaths)
	}

	// A completed call without an explicit host success records no write.
	_, _, code = runWithStdin(t,
		`{"session_id":"mcp1","tool_name":"mcp__filesystem__write_file","tool_input":{"path":"docs/unproven.md"},"tool_response":{"content":"done"}}`,
		"hook", "runtime", "claude-mcp-after", repo)
	if code != 0 {
		t.Fatalf("unproven MCP post must stay fail-open, got exit %d", code)
	}
	state, err = agentsession.LoadSessionState(repo, "mcp1")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(state.WritePaths, "docs/unproven.md") {
		t.Fatalf("a completed call without an explicit success must not create write evidence: %#v", state.WritePaths)
	}

	// A built-in tool identity on the MCP route is envelope drift.
	_, stderr, code = runWithStdin(t,
		`{"session_id":"mcp1","tool_name":"Write","tool_input":{"file_path":"docs/mcp.md"}}`,
		"hook", "runtime", "claude-mcp-before", repo)
	if code != 2 {
		t.Fatalf("a built-in tool on the MCP route must fail closed, got exit %d stderr=%q", code, stderr)
	}

	// The same contract on Codex, whose identity is a separate selector.
	_, _, code = runWithStdin(t, `{"session_id":"mcp2"}`, "hook", "runtime", "codex-session-start", repo)
	if code != 0 {
		t.Fatalf("codex-session-start exit %d", code)
	}
	_, stderr, code = runWithStdin(t,
		`{"session_id":"mcp2","tool_name":"mcp__filesystem__write_file","tool_input":{"path":"generated/out.go"}}`,
		"hook", "runtime", "codex-mcp-before", repo)
	if code != 2 {
		t.Fatalf("protected Codex MCP write must fail closed, got exit %d stderr=%q", code, stderr)
	}
}
