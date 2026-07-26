package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestPolicyPassAndCICannotBypassEvidenceTaint(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	if result := agentsession.RunSessionStart(repo, []byte(`{"session_id":"tainted-cli"}`)); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}
	payload, err := json.Marshal(map[string]interface{}{
		"session_id": "tainted-cli",
		"tool_name":  "Bash",
		"tool_input": map[string]interface{}{"command": strings.Repeat("x", 33*1024)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result := agentsession.RunPostToolUse(repo, payload); result.ExitCode != 0 {
		t.Fatalf("overflow fixture: %+v", result)
	}

	var stdout, stderr bytes.Buffer
	err = Run([]string{"check", repo}, "test", &stdout, &stderr)
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "persisted evidence is uncertified at commands/item_bytes") {
		t.Fatalf("check manufactured a policy pass: err=%v stdout=%s", err, stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = Run([]string{"ci", repo}, "test", &stdout, &stderr)
	if ExitCode(err) != 2 || !strings.Contains(err.Error(), "persisted evidence is uncertified at commands/item_bytes") {
		t.Fatalf("ci bypassed evidence taint: err=%v stdout=%s", err, stdout.String())
	}
}
