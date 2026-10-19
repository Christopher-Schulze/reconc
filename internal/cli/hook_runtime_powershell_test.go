package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func powerShellToolPayload(t *testing.T, tool, command string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]interface{}{
		"session_id": "pwsh-analysis", "tool_name": tool, "tool_use_id": "p1",
		"tool_input": map[string]string{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestHookRuntimePowerShellCommandsUsePowerShellAnalysis(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	rule := "rules:\n  - id: no-publish\n    kind: forbid_command\n    commands: ['npm publish']\n    mode: block\n    message: publication is reserved for releases\n"
	if err := os.WriteFile(filepath.Join(repo, "policies", "commands.yml"), []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "e2e"); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		`npm.cmd publish`,
		`& npm publish`,
		`& 'C:\Program Files\nodejs\npm.cmd' publish`,
		`Start-Process npm -ArgumentList publish`,
		`Get-ChildItem | ForEach-Object { npm publish }`,
		`iex "npm publish"`,
		`Invoke-Expression 'npm publish'`,
		`& $tool publish`,
		`Invoke-Command -ScriptBlock { npm publish }`,
		`powershell -EncodedCommand bgBwAG0AIABwAHUAYgBsAGkAcwBoAA==`,
		`pwsh -Command $text`,
	} {
		t.Run(command, func(t *testing.T) {
			_, stderr, code := runWithStdin(t, powerShellToolPayload(t, "PowerShell", command), "hook", "runtime", "claude-pre-tool-use", repo)
			if code != 2 {
				t.Fatalf("PowerShell %q was not blocked: code=%d stderr=%q", command, code, stderr)
			}
		})
	}
	for _, command := range []string{
		`Get-ChildItem -Recurse | Where-Object { $_.Length -gt 1kb }`,
		`npm.cmd test`,
	} {
		if _, stderr, code := runWithStdin(t, powerShellToolPayload(t, "PowerShell", command), "hook", "runtime", "claude-pre-tool-use", repo); code != 0 {
			t.Fatalf("an ordinary PowerShell command %q was blocked: code=%d stderr=%q", command, code, stderr)
		}
	}
	// Bash reaches PowerShell through its host executable.
	if _, stderr, code := runWithStdin(t, powerShellToolPayload(t, "Bash", `pwsh -NoProfile -c "npm publish"`), "hook", "runtime", "claude-pre-tool-use", repo); code != 2 {
		t.Fatalf("a Bash command hid npm publish inside pwsh: code=%d stderr=%q", code, stderr)
	}
}
