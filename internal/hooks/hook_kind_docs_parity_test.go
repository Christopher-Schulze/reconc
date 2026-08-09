package hooks

import (
	"strings"
	"testing"
)

func TestCommandReferenceHookKindListsMatchRegistry(t *testing.T) {
	document := readRepoDoc(t, "docs/commands.md")
	kinds := "<" + strings.Join(SupportedKinds(), "|") + ">"
	for _, command := range []string{"generate", "install", "uninstall"} {
		want := "reconc hook " + command + " " + kinds
		if strings.Count(document, want) != 1 {
			t.Errorf("docs/commands.md must contain exactly one registry-exact %q heading", want)
		}
	}
}
