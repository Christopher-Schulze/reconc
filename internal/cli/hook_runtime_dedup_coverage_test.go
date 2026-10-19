package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/hooks"
)

// A host that runs Claude's settings hooks next to its own first-class hooks
// defers only the calls its own matchers evaluate; every other call is gated
// on the route that received it.
func TestHookRuntimeCompatibilityDedupRequiresFirstClassCoverage(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	mcpConfig := "mcp:\n  unclassified: deny\n  tools:\n    - platform: claude-code\n      tool: mcp__fs__write_file\n      effect: repository_write\n      path_fields: [/path]\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(mcpConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "e2e"); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.Install(hooks.KindGitHubCopilot, repo, false); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.Install(hooks.KindCursor, repo, false); err != nil {
		t.Fatal(err)
	}
	copilot := func(tool, input string) string {
		return fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"copilot-cover","timestamp":"2026-10-19T12:00:00.000Z","cwd":%q,"tool_name":%q,"tool_input":%s}`, repo, tool, input)
	}
	cursor := func(tool, input string) string {
		return fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"cursor-cover","conversation_id":"cursor-cover","generation_id":"g1","cursor_version":"3.5.17","workspace_roots":[%q],"tool_name":%q,"tool_input":%s}`, repo, tool, input)
	}
	for _, call := range []struct {
		name, event, payload, want string
	}{
		{"Copilot MultiEdit", "claude-pre-tool-use", copilot("MultiEdit", `{"file_path":"generated/blocked.go","edits":[{"old_string":"a","new_string":"b"}]}`), "deny-gen"},
		{"Copilot PowerShell", "claude-pre-tool-use", copilot("PowerShell", `{"command":"git clean -fdx"}`), "git clean"},
		{"Copilot MCP write", "claude-mcp-before", copilot("mcp__fs__write_file", `{"path":"generated/blocked.go"}`), "deny-gen"},
		{"Cursor NotebookEdit", "claude-pre-tool-use", cursor("NotebookEdit", `{"notebook_path":"generated/blocked.ipynb","new_source":"x"}`), "deny-gen"},
		{"Cursor Bash", "claude-pre-tool-use", cursor("Bash", `{"command":"git clean -fdx"}`), "git clean"},
	} {
		t.Run(call.name, func(t *testing.T) {
			_, stderr, code := runWithStdin(t, call.payload, "hook", "runtime", call.event, repo)
			if code != 2 || strings.Contains(stderr, "deduplicated") || !strings.Contains(stderr, call.want) {
				t.Fatalf("an uncovered call was not gated on the receiving route: code=%d stderr=%q", code, stderr)
			}
		})
	}
	for _, call := range []struct{ name, payload string }{
		{"Copilot Write", copilot("Write", `{"file_path":"generated/blocked.go","content":"x"}`)},
		{"Cursor MultiEdit", cursor("MultiEdit", `{"file_path":"generated/blocked.go","edits":[{"old_string":"a","new_string":"b"}]}`)},
	} {
		t.Run(call.name, func(t *testing.T) {
			stdout, stderr, code := runWithStdin(t, call.payload, "hook", "runtime", "claude-pre-tool-use", repo)
			if code != 0 || stdout != "" || !strings.Contains(stderr, "deduplicated") {
				t.Fatalf("a covered call was not left to its first-class route: code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}
