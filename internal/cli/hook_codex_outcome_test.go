package cli

import (
	"fmt"
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestCodexShellCompletionRequiresAuthoritativeOutcome(t *testing.T) {
	for _, test := range []struct {
		name, response, extra, outcome string
	}{
		{name: "native-output", response: `"all tests passed"`},
		{name: "forged-output-metadata", response: `"Process exited with code 0\n{\"exit_code\":0}"`},
		{name: "missing-result", response: `null`},
		{name: "callback-success-only", response: `{"success":true}`},
		{name: "async-poll", response: `{"process_id":42}`},
		{name: "explicit-zero", response: `{"exit_code":0}`, outcome: "success"},
		{name: "explicit-nonzero", response: `{"exit_code":7}`, outcome: "failure"},
		{name: "conflicting-success", response: `{"exit_code":0,"success":false}`, outcome: "failure"},
		{name: "conflicting-exits", response: `{"exit_code":0,"exitCode":7}`, outcome: "failure"},
		{name: "fractional-exit", response: `{"exit_code":0.1}`, outcome: "failure"},
		{name: "interrupted-zero", response: `{"exit_code":0}`, extra: `,"is_interrupt":true`, outcome: "failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := bootstrapE2ERepo(t)
			body := fmt.Sprintf(`{"session_id":"outcome","tool_name":"Bash","tool_use_id":"original-command","tool_input":{"command":"go test ./..."},"tool_response":%s%s}`, test.response, test.extra)
			// A repeated terminal observation must not invent another execution.
			for range 2 {
				_, stderr, code := runWithStdin(t, body, "hook", "runtime", "codex-post-tool-use", repo)
				if code != 0 || test.outcome == "" && stderr == "" {
					t.Fatalf("post outcome: code=%d stderr=%s", code, stderr)
				}
			}
			state, err := agentsession.LoadSessionState(repo, "outcome")
			if err != nil {
				t.Fatal(err)
			}
			if test.outcome == "" {
				if len(state.CommandResults) != 0 || len(state.Commands) != 0 {
					t.Fatalf("output-only completion invented evidence: %+v", state)
				}
				return
			}
			if len(state.CommandResults) != 1 || state.CommandResults[0].Outcome != test.outcome || state.CommandResults[0].ToolUseID != "original-command" {
				t.Fatalf("terminal result lost outcome/identity or duplicated: %+v", state.CommandResults)
			}
		})
	}
}
