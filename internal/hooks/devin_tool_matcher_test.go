package hooks

import (
	"encoding/json"
	"testing"
)

func TestDevinNativeToolMatchersCoverGatedAndObservedSurface(t *testing.T) {
	artifact, err := Generate(KindDevinCLI)
	if err != nil {
		t.Fatal(err)
	}
	var groups map[string][]struct {
		Matcher string `json:"matcher"`
	}
	if err := json.Unmarshal([]byte(artifact.Content), &groups); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"PreToolUse", "PermissionRequest", "PostToolUse"} {
		if len(groups[event]) != 1 {
			t.Fatalf("%s must have one Devin tool route, got %d", event, len(groups[event]))
		}
		matcher := groups[event][0].Matcher
		for _, tool := range []string{
			"exec", "read", "write", "edit", "apply_patch", "notebook_edit",
			"grep", "glob", "get_output", "write_to_process", "kill_shell",
			"mcp_call_tool", "mcp__files__write_file",
		} {
			if !hostMatcherFires(t, matcher, tool) {
				t.Errorf("%s misses native Devin tool %s", event, tool)
			}
		}
		for _, other := range []string{"mcp_call_tool_suffix", "not_mcp__files__write_file", "write_backup"} {
			if hostMatcherFires(t, matcher, other) {
				t.Errorf("%s spuriously matches %s", event, other)
			}
		}
	}
}
