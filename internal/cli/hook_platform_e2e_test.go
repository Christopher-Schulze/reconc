package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestHookRuntimeDevinNativeShapeBlocksDeniedWrite(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"session_id":"devin-1"}`,
		"hook", "runtime", "devin-session-start", repo)

	_, stderr, code := runWithStdin(t,
		`{"session_id":"devin-1","tool_name":"edit","tool_input":{"file_path":"generated/blocked.go"}}`,
		"hook", "runtime", "devin-pre-tool-use", repo)
	if code != 2 || !strings.Contains(stderr, "deny-gen") {
		t.Fatalf("Devin native payload must block denied write, code=%d stderr=%q", code, stderr)
	}
}

func TestHookRuntimeCopilotVSCodeShapeReturnsDenyDecision(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	payload := `{"hook_event_name":"PreToolUse","session_id":"copilot-1","tool_name":"Edit","tool_input":{"file_path":"generated/blocked.go"}}`
	stdout, stderr, code := runWithStdin(t, payload,
		"hook", "runtime", "copilot-pre-tool-use", repo)
	if code != 0 {
		t.Fatalf("Copilot decision must use output JSON instead of process failure, code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"permissionDecision":"deny"`) || !strings.Contains(stdout, "deny-gen") {
		t.Fatalf("Copilot deny response missing exact decision context: %q", stdout)
	}
}

func TestHookRuntimeKiloAdapterShapeBlocksDeniedWrite(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	payload := `{"session_id":"kilo-1","reconc_runtime":"kilo","tool_name":"Write","tool_input":{"file_path":"generated/blocked.go"}}`
	_, stderr, code := runWithStdin(t, payload,
		"hook", "runtime", "kilo-pre-tool-use", repo)
	if code != 2 || !strings.Contains(stderr, "deny-gen") {
		t.Fatalf("Kilo adapter payload must block denied write, code=%d stderr=%q", code, stderr)
	}
}

func TestRepositoryRunControlContinuesEveryAgentPlatform(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	writeHookRuntimeTaskFixture(t, repo)
	tests := []struct {
		name    string
		event   string
		payload string
		want    string
	}{
		{name: "Claude Code", event: "claude-stop", payload: `{"session_id":"claude-run"}`, want: `"decision":"block"`},
		{name: "Codex", event: "codex-stop", payload: `{"session_id":"codex-run"}`, want: `"decision":"block"`},
		{name: "Cursor", event: "cursor-stop", payload: `{"sessionId":"cursor-run","cursor_version":"3.5.17","hook_event_name":"stop","workspace_roots":["` + repo + `"]}`, want: `"followup_message"`},
		{name: "OpenCode", event: "opencode-stop", payload: `{"session_id":"opencode-run","reconc_runtime":"opencode"}`, want: `"decision":"block"`},
		{name: "Devin CLI", event: "devin-stop", payload: `{"session_id":"devin-run"}`, want: `"decision":"block"`},
		{name: "Antigravity CLI", event: "antigravity-stop", payload: `{"session_id":"antigravity-run"}`, want: `"decision":"continue"`},
		{name: "GitHub Copilot", event: "copilot-stop", payload: `{"session_id":"copilot-run","hook_event_name":"Stop"}`, want: `"decision":"block"`},
		{name: "Kilo", event: "kilo-stop", payload: `{"session_id":"kilo-run","reconc_runtime":"kilo"}`, want: `"decision":"block"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := agentsession.SetRunLoopRepoMode(repo, false); err != nil {
				t.Fatal(err)
			}
			if _, err := agentsession.SetRunLoopRepoMode(repo, true); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, code := runWithStdin(t, test.payload, "hook", "runtime", test.event, repo)
			if code != 0 || stderr != "" {
				t.Fatalf("native Stop failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if !strings.Contains(stdout, test.want) || !strings.Contains(stdout, "Reconc run is ON") {
				t.Fatalf("native Stop did not continue: want=%q stdout=%q", test.want, stdout)
			}
		})
	}
}

func TestHookRuntimeDevinPostCompactionReturnsRecoveryPacket(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"session_id":"devin-compact"}`,
		"hook", "runtime", "devin-session-start", repo)

	stdout, stderr, code := runWithStdin(t, `{"session_id":"devin-compact","summary":"provider summary"}`,
		"hook", "runtime", "devin-post-compaction", repo)
	if code != 0 || stderr != "" {
		t.Fatalf("Devin compaction should fail open with clean input, code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "reconc-context-v1") || !strings.Contains(stdout, "additionalContext") {
		t.Fatalf("Devin compaction recovery packet missing: %q", stdout)
	}
}

func TestHookRuntimeClaudeCompactSessionReturnsRecoveryPacket(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"session_id":"claude-compact","source":"startup"}`,
		"hook", "runtime", "claude-session-start", repo)

	stdout, stderr, code := runWithStdin(t, `{"session_id":"claude-compact","source":"compact","compact_summary":"provider summary"}`,
		"hook", "runtime", "claude-post-compaction", repo)
	if code != 0 || stderr != "" {
		t.Fatalf("Claude compact SessionStart should fail open with clean input, code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "reconc-context-v1") || !strings.Contains(stdout, `"hookEventName":"SessionStart"`) {
		t.Fatalf("Claude compaction recovery packet missing or uses the wrong native event: %q", stdout)
	}
}

func TestBoundHookResultKeepsUTF8Valid(t *testing.T) {
	const limit = 64
	result := boundHookResult(
		agentsession.Result{Stderr: strings.Repeat("ä", limit)},
		hooks.RuntimeRoute{MaxOutputBytes: limit, ErrorPolicy: hooks.FailureAllow},
	)
	if !utf8.ValidString(result.Stderr) || !strings.Contains(result.Stderr, "truncated") || len(result.Stderr) > limit/2 {
		t.Fatalf("bounded stderr must remain valid UTF-8: %q", result.Stderr)
	}
}

func TestBoundHookResultCapsCombinedOutput(t *testing.T) {
	const limit = 8 * 1024
	result := boundHookResult(
		agentsession.Result{Stdout: strings.Repeat("o", limit/2), Stderr: strings.Repeat("e", limit)},
		hooks.RuntimeRoute{MaxOutputBytes: limit, ErrorPolicy: hooks.FailureAllow},
	)
	if len(result.Stdout)+len(result.Stderr) > limit {
		t.Fatalf("combined hook output escaped byte budget: stdout=%d stderr=%d", len(result.Stdout), len(result.Stderr))
	}
}

func TestBoundHookResultCapsAdaptedCopilotOutput(t *testing.T) {
	adapted := agentsession.AdaptCopilotResult(
		"copilot-pre-tool-use",
		agentsession.Result{ExitCode: 2, Stderr: strings.Repeat("x", 6*1024)},
	)
	result := boundHookResult(adapted, hooks.RuntimeRoute{MaxOutputBytes: 8 * 1024, ErrorPolicy: hooks.FailureBlock})
	if result.ExitCode != 2 || result.Stdout != "" || len(result.Stderr) > 8*1024 {
		t.Fatalf("adapted Copilot output escaped bounds: %#v", result)
	}
}

func TestRunHookStatusJSONReportsActivePlugin(t *testing.T) {
	repo := t.TempDir()
	if _, err := hooks.Install(hooks.KindKilo, repo, false); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "status", repo, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var reports []hooks.PlatformStatus
	if err := json.Unmarshal(stdout.Bytes(), &reports); err != nil {
		t.Fatalf("decode hook status: %v\n%s", err, stdout.String())
	}
	for _, report := range reports {
		if report.Kind == hooks.KindKilo {
			if report.State != hooks.StateActive {
				t.Fatalf("Kilo status = %s, want active: %+v", report.State, report)
			}
			return
		}
	}
	t.Fatal("Kilo status missing")
}

func TestRunBootstrapJSONIncludesActivationTruth(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".devin"), 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(repo, "tools", "reconc", "bin", "hook")
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"bootstrap", repo, "--skip-git-hook", "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("bootstrap: %v stderr=%s", err, stderr.String())
	}
	var payload struct {
		Healthy      bool                   `json:"healthy"`
		HookStatuses []hooks.PlatformStatus `json:"hook_statuses"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode bootstrap JSON: %v\n%s", err, stdout.String())
	}
	if !payload.Healthy {
		t.Fatalf("bootstrap should be healthy: %s", stdout.String())
	}
	for _, report := range payload.HookStatuses {
		if report.Kind == hooks.KindDevinCLI && report.State == hooks.StateActive {
			return
		}
	}
	t.Fatalf("bootstrap did not report Devin active: %s", stdout.String())
}
