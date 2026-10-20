package agentsession

import (
	"encoding/json"
	"testing"
)

func TestPreDecisionCacheHitRefreshesActiveSession(t *testing.T) {
	for _, class := range []preDecisionResultClass{preDecisionResultPass, preDecisionResultBlock} {
		t.Run(string(class), func(t *testing.T) {
			repo := setupStopBenchmarkRepo(t)
			state, err := EnsureSessionState(repo, "cached-session")
			if err != nil {
				t.Fatal(err)
			}
			payload := &HookPayload{SessionID: state.SessionID, ToolUseID: "same-call", ToolName: "Write",
				ToolInput: map[string]interface{}{"file_path": "src/a.go"}}
			inputs, ok := preDecisionInputsForPayload(state.RepoRoot, payload)
			if !ok {
				t.Fatal("fixture is not cacheable")
			}
			decision := Result{decisionClass: class, Stderr: "cached decision"}
			if class == preDecisionResultBlock {
				decision.ExitCode = 2
			}
			if err := writePreDecisionCacheForPayload(state.RepoRoot, payload, inputs.key, decision); err != nil {
				t.Fatal(err)
			}
			if _, err := EnsureSessionState(repo, "other-session"); err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(map[string]interface{}{
				"session_id": payload.SessionID, "tool_use_id": payload.ToolUseID,
				"tool_name": payload.ToolName, "tool_input": payload.ToolInput,
			})
			if err != nil {
				t.Fatal(err)
			}
			result := runPreDecisionResolvedWithEvaluatorAndStopCache(state.RepoRoot, body, false, true, nil, nil)
			if result.Stderr != decision.Stderr || result.ExitCode != decision.ExitCode {
				t.Fatalf("fixture did not reuse its decision: %+v", result)
			}
			active, err := ResolveActiveSessionID(repo)
			if err != nil || active != state.SessionID {
				t.Fatalf("cache hit left active session %q: %v", active, err)
			}
		})
	}
}
