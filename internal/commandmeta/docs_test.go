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

func TestArchitectureDocumentsCanonicalMetadataWithoutFragileCounts(t *testing.T) {
	architecture := readRepositoryDoc(t, "architecture.md")
	for _, want := range []string{"internal/commandmeta/catalog.go", "commandmeta"} {
		if !strings.Contains(architecture, want) {
			t.Fatalf("architecture documentation omits %q", want)
		}
	}
	for _, stale := range []string{"completion.Subcommands", "subcommand table", "Subcommands table"} {
		if strings.Contains(architecture, stale) {
			t.Fatalf("architecture documentation still contains stale command owner %q", stale)
		}
	}
	for _, doc := range []struct {
		name string
		body string
	}{
		{name: "commands.md", body: readRepositoryDoc(t, "commands.md")},
		{name: "architecture.md", body: architecture},
	} {
		for _, stale := range []string{"41 subcommands", "42 subcommands", "43 subcommands", "212-line", "~20 lines"} {
			if strings.Contains(doc.body, stale) {
				t.Fatalf("%s contains fragile manual count %q", doc.name, stale)
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
