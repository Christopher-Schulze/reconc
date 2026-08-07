package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// documentedRuntimeExemptions are registered platforms the runtime tables do
// not list as agent runtimes. The Git pre-commit hook is a Git boundary, not an
// agent, and is documented in its own sections.
var documentedRuntimeExemptions = map[string]bool{KindGitPreCommit: true}

// documentedArtifactAliases map a registry target to the form the contract
// uses where the two are the same file written differently. Kimi Code is
// user-global, so the contract names the variable that resolves the home
// rather than one expanded default.
var documentedArtifactAliases = map[string]string{
	KindKimiCode:     "$KIMI_CODE_HOME/config.toml",
	KindGitPreCommit: ".git/hooks/pre-commit",
}

func readRepoDoc(t *testing.T, relative string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", relative))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(body)
}

// TestEveryRegisteredRuntimeIsDocumented ties the hand-maintained runtime
// tables to the registry. Those tables are what a reader trusts to learn what
// the product supports, and nothing else notices when a platform is added,
// renamed, or removed and a table is left behind.
func TestEveryRegisteredRuntimeIsDocumented(t *testing.T) {
	docs := map[string]string{
		"README.md":             readRepoDoc(t, "README.md"),
		"docs/documentation.md": readRepoDoc(t, "docs/documentation.md"),
	}
	// RECONC-0006 is the frozen hook contract: it must list every installable
	// kind with the artifact it owns.
	contract := readRepoDoc(t, "docs/rfcs/RECONC-0006-hooks-and-agent-sessions.md")
	for _, platform := range Platforms() {
		if !documentedRuntimeExemptions[platform.Kind] {
			for name, body := range docs {
				if !strings.Contains(body, platform.DisplayName) {
					t.Fatalf("%s does not document the registered runtime %q (%s)", name, platform.DisplayName, platform.Kind)
				}
			}
		}
		if !strings.Contains(contract, "`"+platform.Kind+"`") {
			t.Fatalf("RECONC-0006 does not list the installable kind %q", platform.Kind)
		}
		artifact := platform.TargetPath
		if documented, ok := documentedArtifactAliases[platform.Kind]; ok {
			artifact = documented
		}
		if !strings.Contains(contract, artifact) {
			t.Fatalf("RECONC-0006 does not name the project artifact %q for %s", artifact, platform.DisplayName)
		}
	}
}

// TestDocumentedRuntimesAreRegistered is the other direction: a table must not
// promise a runtime the binary cannot install.
func TestDocumentedRuntimesAreRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, platform := range Platforms() {
		registered[platform.DisplayName] = true
	}
	readme := readRepoDoc(t, "README.md")
	start := strings.Index(readme, "## Supported Agent Runtimes")
	if start < 0 {
		t.Fatal("README no longer carries a Supported Agent Runtimes section")
	}
	section := readme[start:]
	if end := strings.Index(section[1:], "\n## "); end >= 0 {
		section = section[:end+1]
	}
	claimed := 0
	started := false
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "| Runtime") {
			started = true
			continue
		}
		if !started {
			continue
		}
		// The runtime table ends at the first line that is not one of its rows.
		if !strings.HasPrefix(line, "| ") {
			if strings.TrimSpace(line) == "" && claimed == 0 {
				continue
			}
			break
		}
		if strings.HasPrefix(line, "| ---") {
			continue
		}
		name := strings.TrimSpace(strings.Split(strings.TrimPrefix(line, "| "), "|")[0])
		if name == "" || name == "Declarative custom runtimes" {
			continue
		}
		if !registered[name] {
			t.Fatalf("README claims runtime %q, which the platform registry does not provide", name)
		}
		claimed++
	}
	expected := 0
	for _, platform := range Platforms() {
		if !documentedRuntimeExemptions[platform.Kind] {
			expected++
		}
	}
	if claimed != expected {
		t.Fatalf("README lists %d agent runtimes, the registry provides %d", claimed, expected)
	}
}
