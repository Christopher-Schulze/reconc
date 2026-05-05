package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
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
	err = Run(argv, "0.4.0-e2e", &stdout, &stderr)
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

// --- Scenario 2: PreToolUse blocks deny_write ----------------------

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

	// Codex PreToolUse on a Bash command is a no-op (not a write tool).
	_, _, code = runWithStdin(t,
		`{"session_id":"cx1","tool_name":"Bash","tool_input":{"command":"ls"}}`,
		"hook", "runtime", "codex-pre-tool-use", repo)
	if code != 0 {
		t.Errorf("codex-pre-tool-use on Bash should exit 0, got %d", code)
	}

	// Codex doesn't have SessionEnd; verify the router rejects it.
	_, _, code = runWithStdin(t, `{"session_id":"cx1"}`,
		"hook", "runtime", "codex-session-end", repo)
	if code != 1 {
		t.Errorf("unknown event should return exit 1, got %d", code)
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

	// 2 MiB of noise; cap is 1 MiB.
	big := strings.Repeat("x", 2*1024*1024)
	_, _, code := runWithStdin(t, big,
		"hook", "runtime", "claude-pre-tool-use", repo)
	if code != 2 {
		t.Errorf("payload > 1 MiB should fail-closed exit 2 for PreToolUse, got %d", code)
	}
}
