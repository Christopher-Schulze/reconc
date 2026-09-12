package hooks

import (
	"regexp"
	"strings"
	"testing"
)

func TestCodexGeneratedToolsPartitionLocalAndMCPNames(t *testing.T) {
	artifact, err := Generate(KindCodex)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"PreToolUse", "PostToolUse"} {
		matchers := matchersForEvent(t, artifact.Content, "hooks", event)
		if len(matchers) != 2 {
			t.Fatalf("%s has %d matcher groups", event, len(matchers))
		}
		patterns := make([]*regexp.Regexp, len(matchers))
		for i, matcher := range matchers {
			patterns[i], err = regexp.Compile(matcher)
			if err != nil {
				t.Fatal(err)
			}
		}
		check := func(name string) {
			t.Helper()
			local, mcp := patterns[0].MatchString(name), patterns[1].MatchString(name)
			if local == mcp || mcp != strings.HasPrefix(name, "mcp__") {
				t.Fatalf("%s %q: local=%t MCP=%t", event, name, local, mcp)
			}
		}
		for _, name := range []string{"Read", "Write", "NotebookEdit", "Delete", "apply_patch", "Bash", "spawn_agent", "Agent", "functions.custom", "not_mcp__server__tool", "mcp__", "mcp____", "mcp__server__tool", "mcp_", "mc", "m", "mcp"} {
			check(name)
		}
		// Exhaust every prefix boundary and its lookalikes independently of
		// the regular expression, including names shorter than the namespace.
		var visit func(string, int)
		visit = func(prefix string, left int) {
			if prefix != "" {
				check(prefix)
			}
			if left == 0 {
				return
			}
			for _, char := range "mcp_x" {
				visit(prefix+string(char), left-1)
			}
		}
		visit("", 6)
	}
}
