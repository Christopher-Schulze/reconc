package commandmeta

import (
	"os"
	"strings"
	"testing"
)

func TestCommandDocumentationCoversCanonicalInventory(t *testing.T) {
	commandsDoc := readRepositoryDoc(t, "commands.md")
	for _, command := range All() {
		assertDocumentedCommand(t, commandsDoc, "reconc "+command.Name)
		for _, nested := range command.Subcommands {
			if !documentsSurface(commandsDoc, "reconc "+command.Name+" "+nested.Name) &&
				!documentsSurface(commandsDoc, command.Name+" "+nested.Name) {
				t.Errorf("docs/commands.md omits canonical surface %q", command.Name+" "+nested.Name)
			}
		}
	}
}

func readRepositoryDoc(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile("../../docs/" + name)
	if err != nil {
		t.Fatalf("read docs/%s: %v", name, err)
	}
	return string(body)
}

func assertDocumentedCommand(t *testing.T, body, surface string) {
	t.Helper()
	if !documentsSurface(body, surface) {
		t.Errorf("docs/commands.md omits canonical surface %q", surface)
	}
}

func documentsSurface(body, surface string) bool {
	return strings.Contains(body, "`"+surface)
}
