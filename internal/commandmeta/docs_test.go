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

func TestDocumentsSurfaceRequiresCommandBoundary(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "exact code span", body: "Use `reconc version`.", want: true},
		{name: "arguments in code span", body: "Use `reconc version --json`.", want: true},
		{name: "hyphenated prefix collision", body: "Use `reconc version-broken`.", want: false},
		{name: "word prefix collision", body: "Use `reconc versioned`.", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := documentsSurface(test.body, "reconc version"); got != test.want {
				t.Errorf("documentsSurface() = %v, want %v", got, test.want)
			}
		})
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
	needle := "`" + surface
	for offset := 0; offset < len(body); {
		index := strings.Index(body[offset:], needle)
		if index < 0 {
			return false
		}
		end := offset + index + len(needle)
		if end < len(body) && (body[end] == '`' || body[end] == ' ' || body[end] == '\t' || body[end] == '\r' || body[end] == '\n') {
			return true
		}
		offset = end
	}
	return false
}
