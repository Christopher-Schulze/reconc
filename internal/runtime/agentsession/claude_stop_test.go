package agentsession

import (
	"encoding/json"
	"testing"
)

func TestClaudeStopBlockSurvivesMissingFileDiagnostics(t *testing.T) {
	blocked := AdaptClaudeStopResult(Result{ExitCode: 2, Stderr: "reconc hook (stop): open .reconc/policy.lock.json: no such file or directory"})
	var decision map[string]string
	if err := json.Unmarshal([]byte(blocked.Stdout), &decision); err != nil {
		t.Fatalf("blocking Stop must carry a JSON decision: %q, %v", blocked.Stdout, err)
	}
	if blocked.ExitCode != 2 || decision["decision"] != "block" || decision["reason"] != "reconc hook (stop): open .reconc/policy.lock.json: no such file or directory" {
		t.Fatalf("blocked = %+v decision = %v", blocked, decision)
	}
	if passed := AdaptClaudeStopResult(Result{ExitCode: 0, Stderr: "note"}); passed.Stdout != "" || passed.ExitCode != 0 {
		t.Fatalf("a passing Stop must stay silent: %+v", passed)
	}
	if existing := AdaptClaudeStopResult(Result{ExitCode: 2, Stdout: `{"decision":"block","reason":"x"}`}); existing.Stdout != `{"decision":"block","reason":"x"}` {
		t.Fatalf("an existing decision must be kept: %+v", existing)
	}
}
