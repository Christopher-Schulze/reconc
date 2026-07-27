package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestEveryLeafCommandRendersHelpWithoutRepositorySideEffects(t *testing.T) {
	commands := [][]string{
		{"doctor"}, {"demo"}, {"compile"}, {"refresh"}, {"check"}, {"assert"}, {"init"},
		{"status"}, {"ci"}, {"exec"},
		{"hook", "status"}, {"hook", "generate"}, {"hook", "install"}, {"hook", "uninstall"},
		{"hook", "sync-scaffold"}, {"hook", "evidence-status"}, {"hook", "evidence-resolve"}, {"hook", "claim"},
		{"grok", "pre-tool-guard"},
		{"preset", "list"}, {"preset", "show"},
		{"bootstrap", "profiles"}, {"bootstrap", "inspect"}, {"bootstrap", "plan"}, {"bootstrap", "apply"},
		{"bootstrap", "verify"}, {"bootstrap", "remove"},
		{"install-cli"}, {"fix"}, {"next"}, {"explain"}, {"verify"}, {"why"}, {"can"}, {"adopt"},
		{"changelog", "rotate"}, {"changelog", "list-archives"}, {"agent-intro"},
		{"audit", "tail"}, {"audit", "stats"}, {"audit", "export"},
		{"run", "status"}, {"run", "log"}, {"run", "reset"}, {"run", "on"}, {"run", "off"},
		{"task", "status"}, {"task", "new"}, {"task", "claim"}, {"task", "block"}, {"task", "split"},
		{"task", "promote"}, {"task", "archive"}, {"task", "recover"}, {"task", "check-done"},
		{"prune"}, {"template", "list"}, {"template", "show"}, {"session-briefing"}, {"context", "size"},
		{"start"}, {"post-task-check"}, {"delta"}, {"done"}, {"proof"}, {"spec"}, {"coverage"},
		{"extract"}, {"diff"}, {"watch"}, {"tui"}, {"completion"}, {"manpage"},
	}
	for _, command := range commands {
		name := strings.Join(command, " ")
		t.Run(name, func(t *testing.T) {
			argv := append(append([]string{}, command...), "--help")
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if err := Run(argv, "test-version", &stdout, &stderr); err != nil {
				t.Fatalf("%s --help failed: %v", name, err)
			}
			output := stdout.String() + stderr.String()
			if !strings.Contains(strings.ToLower(output), "usage:") {
				t.Fatalf("%s --help omitted usage text: stdout=%q stderr=%q", name, stdout.String(), stderr.String())
			}
		})
	}
}
