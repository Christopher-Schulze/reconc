package agentsession

import "testing"

func TestPowerShellToolCommandIsAnalyzedAsPowerShell(t *testing.T) {
	payload, err := ParsePayload([]byte(`{"session_id":"s","tool_name":"PowerShell","tool_input":{"command":"npm.cmd test"}}`))
	if err != nil {
		t.Fatal(err)
	}
	// Recorded evidence uses the normalized form, so `npm.cmd test` satisfies
	// a rule that requires `npm test`.
	if got := payload.Command(); got != "npm test" {
		t.Fatalf("PowerShell command = %q, want %q", got, "npm test")
	}
	bash, err := ParsePayload([]byte(`{"session_id":"s","tool_name":"Bash","tool_input":{"command":"npm.cmd test"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := bash.Command(); got != "npm.cmd test" {
		t.Fatalf("Bash command changed: %q", got)
	}
}

func TestCopilotShellDialectFollowsThePlatform(t *testing.T) {
	copilot := []byte(`{"session_id":"s","timestamp":"2026-10-19T12:00:00.000Z","tool_name":"Bash","tool_input":{"command":"iex 'npm publish'"}}`)
	claude := []byte(`{"session_id":"s","transcript_path":"/tmp/t.jsonl","tool_name":"Bash","tool_input":{"command":"iex 'npm publish'"}}`)
	previous := copilotShellRunsPowerShell
	t.Cleanup(func() { copilotShellRunsPowerShell = previous })
	for _, test := range []struct {
		name     string
		windows  bool
		payload  []byte
		wantTool string
	}{
		{name: "Copilot on Windows", windows: true, payload: copilot, wantTool: "PowerShell"},
		{name: "Copilot elsewhere", windows: false, payload: copilot, wantTool: "Bash"},
		{name: "Claude Code on Windows", windows: true, payload: claude, wantTool: "Bash"},
	} {
		t.Run(test.name, func(t *testing.T) {
			copilotShellRunsPowerShell = test.windows
			payload, err := ParsePayload(test.payload)
			if err != nil {
				t.Fatal(err)
			}
			if payload.ToolName != test.wantTool {
				t.Fatalf("tool = %q, want %q", payload.ToolName, test.wantTool)
			}
		})
	}
	if got := normalizeGitHubCopilotToolName("powershell"); got != "PowerShell" {
		t.Fatalf("Copilot runtime powershell = %q", got)
	}
}
