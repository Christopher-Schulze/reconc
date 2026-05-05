package agentsession

import (
	"os"
	"path/filepath"
	"strings"
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

func TestRunPreToolUseAllowsNonWriteTool(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	result := RunPreToolUse(repo, []byte(`{"session_id":"s1","tool_name":"Read"}`))
	if result.ExitCode != 0 {
		t.Errorf("non-write tool should not block, got %d (%s)", result.ExitCode, result.Stderr)
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

func TestRunPreToolUseAllowsLegitWrite(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	payload := `{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"src/main.go"}}`
	result := RunPreToolUse(repo, []byte(payload))
	if result.ExitCode != 0 {
		t.Errorf("legit write should not block, got %d (%s)", result.ExitCode, result.Stderr)
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

func TestRunStopHappyPath(t *testing.T) {
	repo := setupPolicyRepo(t)
	_ = RunSessionStart(repo, []byte(`{"session_id":"s1"}`))
	// No writes, no claims required to trigger.
	result := RunStop(repo, []byte(`{"session_id":"s1"}`))
	if result.ExitCode != 0 || result.Stdout != "" {
		t.Errorf("clean session should pass Stop silently, got exit=%d stdout=%q", result.ExitCode, result.Stdout)
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
