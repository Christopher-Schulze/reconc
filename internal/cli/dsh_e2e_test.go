package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestDSHRejectsUninspectableExecutionRoutes(t *testing.T) {
	repo := newTask499ScenarioRepo(t, "rules:\n  - id: deny-generated\n    template: no-generated-writes\n", nil)
	for _, test := range []struct {
		name, input string
	}{
		{"run_code", `{"code":"const fs=await import('node:fs/promises'); await fs.writeFile('generated/blocked.go','x')","description":"write file"}`},
		{"terminal_send", `{"sessionId":"s","text":"printf x > generated/blocked.go","submit":true}`},
		{"terminal_open", `{"type":"shell","cwd":"/tmp"}`},
		{"terminal_signal", `{"sessionId":"s","signal":"SIGCONT"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]interface{}{
				"hook_event_name": "tools/pre-execute", "session_id": "dsh-unsafe", "cwd": repo,
				"agent_id": "agent-1", "tool_name": test.name, "tool_input": json.RawMessage(test.input),
				"tool_call_id": "call-1", "root_call_id": "call-1",
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := agentsession.NormalizeDSHPayload("dsh-pre-tool-use", payload, repo); err == nil || !strings.Contains(err.Error(), "command contract") {
				t.Fatalf("unsafe route diagnostic = %v", err)
			}
			out, diagnostic, code := runWithStdin(t, string(payload), "hook", "runtime", "dsh-pre-tool-use", repo)
			if code != 2 || out != "" {
				t.Fatalf("unsafe route: code=%d stdout=%q stderr=%q", code, out, diagnostic)
			}
		})
	}
}

func TestDSHPreDecisionAndFinalResultObservation(t *testing.T) {
	repo := newTask499ScenarioRepo(t, "rules:\n  - id: deny-generated\n    template: no-generated-writes\n", nil)
	start := fmt.Sprintf(`{"hook_event_name":"session_start","session_id":"dsh-observed","cwd":%q}`, repo)
	if out, diagnostic, code := runWithStdin(t, start, "hook", "runtime", "dsh-session-start", repo); code != 0 || out != "" || diagnostic != "" {
		t.Fatalf("DSH session start: code=%d stdout=%q stderr=%q", code, out, diagnostic)
	}
	pre := func(path string) string {
		return fmt.Sprintf(`{"hook_event_name":"tools/pre-execute","session_id":"dsh-observed","cwd":%q,"agent_id":"agent-1","tool_name":"write","tool_input":{"file_path":%q,"content":"candidate"},"tool_call_id":"call-1","root_call_id":"call-1"}`, repo, path)
	}
	if out, diagnostic, code := runWithStdin(t, pre("generated/blocked.go"), "hook", "runtime", "dsh-pre-tool-use", repo); code != 2 || out != "" || !strings.Contains(diagnostic, "deny-generated") {
		t.Fatalf("DSH protected write: code=%d stdout=%q stderr=%q", code, out, diagnostic)
	}
	if out, diagnostic, code := runWithStdin(t, pre("docs/allowed.md"), "hook", "runtime", "dsh-pre-tool-use", repo); code != 0 || out != "" || diagnostic != "" {
		t.Fatalf("DSH ordinary write: code=%d stdout=%q stderr=%q", code, out, diagnostic)
	}
	post := fmt.Sprintf(`{"hook_event_name":"tools/result","session_id":"dsh-observed","cwd":%q,"agent_id":"agent-1","tool_name":"write","tool_input":{"file_path":"docs/allowed.md","content":"candidate"},"tool_call_id":"call-1","root_call_id":"call-1","is_error":false,"result_observed":true}`, repo)
	if out, diagnostic, code := runWithStdin(t, post, "hook", "runtime", "dsh-post-tool-use", repo); code != 0 || out != "" || diagnostic != "" {
		t.Fatalf("DSH transformed final result: code=%d stdout=%q stderr=%q", code, out, diagnostic)
	}
	state, err := agentsession.LoadSessionState(repo, "dsh-observed")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.WritePaths) != 0 || len(state.CommandResults) != 0 {
		t.Fatalf("DSH final host observation claimed material success: writes=%v commands=%v", state.WritePaths, state.CommandResults)
	}
}
