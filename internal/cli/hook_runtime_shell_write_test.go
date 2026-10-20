package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func shellWritePayload(t *testing.T, cwd, command string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]interface{}{
		"session_id": "shell-writes", "tool_name": "Bash", "tool_use_id": "b1", "cwd": cwd,
		"tool_input": map[string]string{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

// Literal shell write targets meet write policy before the command runs, as
// Edit and Write calls do; dynamic targets keep after-execution enforcement.
func TestHookRuntimeJudgesLiteralShellWritesBeforeExecution(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	blocked := []struct{ cwd, command string }{
		{repo, `printf '' >> generated/out.go`},
		{repo, `echo x > generated/out.go`},
		{repo, `echo x | tee generated/out.go`},
		{repo, `cp README.md generated/out.go`},
		{repo, `sed -i 's/a/b/' generated/out.go`},
		{repo, `sh -c 'touch generated/out.go'`},
		{filepath.Join(repo, "generated"), `echo x > out.go`},
		{repo, `echo x > ` + filepath.Join(repo, "generated", "out.go")},
	}
	for _, call := range blocked {
		_, stderr, code := runWithStdin(t, shellWritePayload(t, call.cwd, call.command), "hook", "runtime", "claude-pre-tool-use", repo)
		if code != 2 || !containsAll(stderr, "deny-gen", "before execution") {
			t.Fatalf("%q in %s was not judged before execution: code=%d stderr=%q", call.command, call.cwd, code, stderr)
		}
	}
	// A declaration names additional effects; it cannot hide a literal target.
	declared, err := json.Marshal(map[string]interface{}{
		"session_id": "shell-writes", "tool_name": "Bash", "tool_use_id": "b2", "cwd": repo,
		"tool_input":         map[string]string{"command": "printf x > generated/out.go"},
		"reconc_write_paths": []string{"docs/notes.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runWithStdin(t, string(declared), "hook", "runtime", "claude-pre-tool-use", repo); code != 2 || !strings.Contains(stderr, "deny-gen") {
		t.Fatalf("a declaration hid a literal write target: code=%d stderr=%q", code, stderr)
	}
	admitted := []string{
		`cat generated/out.go`,
		`ls generated 2>&1`,
		`echo x > /dev/null`,
		`echo x > ` + filepath.Join(t.TempDir(), "outside.txt"),
		`echo x > docs/notes.md`,
		`echo x > "$TARGET"`,
		`cd generated && echo x > ../docs/notes.md`,
	}
	for _, command := range admitted {
		if _, stderr, code := runWithStdin(t, shellWritePayload(t, repo, command), "hook", "runtime", "claude-pre-tool-use", repo); code != 0 {
			t.Fatalf("%q was blocked: code=%d stderr=%q", command, code, stderr)
		}
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
