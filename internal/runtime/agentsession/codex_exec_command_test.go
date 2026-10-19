package agentsession

import (
	"strings"
	"testing"
)

func TestCodexExecCommandIsGatedAsShellCommand(t *testing.T) {
	repo := setupPolicyRepo(t)
	for _, name := range []string{"exec_command", "functions.exec_command"} {
		payload, err := ParsePayload([]byte(`{"session_id":"codex","tool_name":"` + name + `","tool_input":{"cmd":"git clean -fd"}}`))
		if err != nil {
			t.Fatal(err)
		}
		if !payload.IsCommandTool() || payload.Command() != "git clean -fd" {
			t.Fatalf("%s is not a command tool: command=%q", name, payload.Command())
		}
		result := RunPreToolUse(repo, []byte(`{"session_id":"codex","tool_name":"`+name+`","tool_input":{"cmd":"git clean -fd"}}`))
		if result.ExitCode != 2 || !strings.Contains(result.Stderr, "git clean") {
			t.Fatalf("%s ran destructive git clean ungated: %+v", name, result)
		}
	}
}
