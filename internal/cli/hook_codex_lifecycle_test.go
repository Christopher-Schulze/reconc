package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestCodexCompactRecoveryPreservesEvidenceAndReachesModelContext(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	for _, input := range []struct{ route, payload string }{
		{"codex-session-start", `{"session_id":"compact-native","source":"startup"}`},
		{"codex-post-tool-use", `{"session_id":"compact-native","tool_use_id":"read-1","tool_name":"Read","tool_input":{"file_path":"README.md"}}`},
	} {
		if _, stderr, code := runWithStdin(t, input.payload, "hook", "runtime", input.route, repo); code != 0 || stderr != "" {
			t.Fatalf("setup %s: code=%d stderr=%s", input.route, code, stderr)
		}
	}
	stdout, stderr, code := runWithStdin(t, `{"hook_event_name":"SessionStart","session_id":"compact-native","source":"compact"}`, "hook", "runtime", "codex-compaction-recovery", repo)
	if code != 0 || stderr != "" {
		t.Fatalf("recovery: code=%d stderr=%s", code, stderr)
	}
	var output struct {
		Hook struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatal(err)
	}
	if output.Hook.Event != "SessionStart" || !strings.Contains(output.Hook.Context, "reads=1") || !strings.Contains(output.Hook.Context, "reconc-context-v1") {
		t.Fatalf("missing model-visible evidence recovery: %s", stdout)
	}
	state, err := agentsession.LoadSessionState(repo, "compact-native")
	if err != nil || len(state.ReadPaths) != 1 || state.ReadPaths[0] != "README.md" {
		t.Fatalf("compaction reset session evidence: %+v error=%v", state, err)
	}
}

func TestCodexInterruptCannotApproveCompletionOrDisableRunMode(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	if _, err := agentsession.SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{
		`{"hook_event_name":"Interrupt","session_id":"interrupt-native","turn_id":"turn-1","permission_mode":"default"}`,
		`{"hook_event_name":"Stop","session_id":"interrupt-native","turn_id":"turn-1"}`,
		`{"hook_event_name":"Interrupt","session_id":"interrupt-native"}`,
		`not-json`,
	} {
		stdout, _, code := runWithStdin(t, payload, "hook", "runtime", "codex-interrupt", repo)
		if code != 0 || stdout != "" {
			t.Fatalf("interrupt tried to control the host: code=%d stdout=%s", code, stdout)
		}
		status, err := agentsession.ReadRepositoryRunStatus(repo)
		if err != nil || !status.Enabled {
			t.Fatalf("interrupt disabled durable run mode: %+v error=%v", status, err)
		}
	}
}

func TestCodexRecoveryRejectsForeignLifecycleEnvelopes(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	for _, payload := range []string{
		`{"hook_event_name":"SessionStart","session_id":"s","source":"startup"}`,
		`{"hook_event_name":"PostCompact","session_id":"s","source":"compact"}`,
		`{"Hook_Event_Name":"SessionStart","session_id":"s","source":"compact"}`,
		`{"hook_event_name":"SessionStart","session_id":"s","source":"compact","source":"startup"}`,
	} {
		stdout, stderr, code := runWithStdin(t, payload, "hook", "runtime", "codex-compaction-recovery", repo)
		if code != 0 || stdout != "" || stderr == "" {
			t.Fatalf("foreign lifecycle was accepted: code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
	}
}
