package hooks

import "testing"

func TestFirstClassRouteCoversOnlyCallsItsOwnMatchersEvaluate(t *testing.T) {
	route := func(name string) RuntimeRoute {
		t.Helper()
		resolved, ok := RuntimeEvent(name)
		if !ok {
			t.Fatalf("unknown runtime event %s", name)
		}
		return resolved
	}
	tests := []struct {
		name, kind, event, tool string
		want                    bool
	}{
		{name: "Copilot guarded tool", kind: KindGitHubCopilot, event: "claude-pre-tool-use", tool: "Write", want: true},
		{name: "Copilot tool casing", kind: KindGitHubCopilot, event: "claude-pre-tool-use", tool: "bash", want: true},
		{name: "Copilot unguarded tool", kind: KindGitHubCopilot, event: "claude-pre-tool-use", tool: "MultiEdit"},
		{name: "Copilot MCP tool", kind: KindGitHubCopilot, event: "claude-mcp-before", tool: "mcp__fs__write_file"},
		{name: "Copilot evidence tool", kind: KindGitHubCopilot, event: "claude-post-tool-use", tool: "powershell", want: true},
		{name: "Copilot lifecycle event", kind: KindGitHubCopilot, event: "claude-stop", want: true},
		{name: "Copilot event it lacks", kind: KindGitHubCopilot, event: "claude-config-change"},
		{name: "Copilot event in other casing", kind: KindGitHubCopilot, event: "claude-subagent-start", want: true},
		{name: "Cursor write tool", kind: KindCursor, event: "claude-pre-tool-use", tool: "MultiEdit", want: true},
		{name: "Cursor shell on the tool event", kind: KindCursor, event: "claude-pre-tool-use", tool: "Bash"},
		{name: "Cursor prompt alias", kind: KindCursor, event: "claude-user-prompt-submit", want: true},
		{name: "Grok anchored matcher", kind: KindGrok, event: "claude-pre-tool-use", tool: "search_replace", want: true},
		{name: "Grok partial name", kind: KindGrok, event: "claude-pre-tool-use", tool: "search_replace_all"},
		{name: "Devin tool", kind: KindDevinCLI, event: "claude-post-tool-use", tool: "Write", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := FirstClassRouteCovers(test.kind, route(test.event), test.tool); got != test.want {
				t.Fatalf("FirstClassRouteCovers(%s, %s, %q) = %v, want %v", test.kind, test.event, test.tool, got, test.want)
			}
		})
	}
	if matcherCoversTool(ptr("("), "x") {
		t.Fatal("an invalid matcher covered a tool")
	}
}

func ptr(value string) *string { return &value }
