package agentsession

import (
	"testing"

	"reconc.dev/reconc/internal/runtime"
)

func TestPolicyDecisionRequiresAnActualEvaluation(t *testing.T) {
	repo := setupPolicyRepo(t)
	for _, test := range []struct {
		name, payload string
		decision      runtime.Decision
		known         bool
	}{
		{"block", `{"session_id":"decision-block","tool_name":"Write","tool_input":{"file_path":"generated/file.txt"}}`, runtime.DecisionBlock, true},
		{"pass", `{"session_id":"decision-pass","tool_name":"Bash","tool_input":{"command":"printf clean"}}`, runtime.DecisionPass, true},
		{"unclassified-read", `{"session_id":"decision-read","tool_name":"Read","tool_input":{"file_path":"AGENTS.md"}}`, "", false},
		{"input-error", `{"tool_name":"Write","tool_input":{"file_path":"generated/file.txt"}}`, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for range 2 {
				result := RunPreToolUse(repo, []byte(test.payload))
				decision, known := result.PolicyDecision()
				if known != test.known || decision != test.decision {
					t.Fatalf("decision=%s known=%t result=%+v", decision, known, result)
				}
				if test.known {
					// Exercise the real serializer's error path after a valid
					// evaluation. Failed response encoding invalidates evidence.
					failed := resultWithHookJSON(result, make(chan int))
					if _, known := failed.PolicyDecision(); known || failed.Err == nil {
						t.Fatal("encoding failure retained policy proof")
					}
				}
			}
		})
	}
}
