package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestCodexPermissionUsesClassifiedMCPPolicyAndNativeDenyShape(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	config := "mcp:\n  unclassified: deny\n  tools:\n    - platform: codex\n      tool: mcp__files__write\n      effect: repository_write\n      path_fields: [/path]\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "e2e"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, tool, input string
		deny              bool
	}{
		{"protected MCP write", "mcp__files__write", `{"path":"generated/out.go"}`, true},
		{"allowed MCP write", "mcp__files__write", `{"path":"docs/out.md"}`, false},
		{"unknown MCP tool", "mcp__files__unknown", `{}`, true},
		{"invalid MCP input", "mcp__files__write", `{"path":42}`, true},
		{"malformed MCP namespace", "mcp__files", `{}`, true},
		{"protected local write", "Write", `{"file_path":"generated/out.go"}`, true},
		{"allowed local read", "Read", `{"file_path":"README.md"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"hook_event_name":"PermissionRequest","session_id":"permission","tool_name":%q,"tool_input":%s}`, test.tool, test.input)
			stdout, stderr, code := runWithStdin(t, body, "hook", "runtime", "codex-permission-request", repo)
			if code != 0 {
				t.Fatalf("permission transport: code=%d stderr=%s", code, stderr)
			}
			if !test.deny {
				if stdout != "" {
					t.Fatalf("allowed policy must leave host approval intact: %s", stdout)
				}
				return
			}
			// The pinned native parser requires this nested decision. Reject
			// any extra field instead of silently accepting Claude-only output.
			var output struct {
				Hook struct {
					Event    string `json:"hookEventName"`
					Decision struct {
						Behavior string `json:"behavior"`
						Message  string `json:"message"`
					} `json:"decision"`
				} `json:"hookSpecificOutput"`
			}
			decoder := json.NewDecoder(strings.NewReader(stdout))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&output); err != nil {
				t.Fatal(err)
			}
			if output.Hook.Event != "PermissionRequest" || output.Hook.Decision.Behavior != "deny" || output.Hook.Decision.Message == "" {
				t.Fatalf("policy was not denied using native shape: %s", stdout)
			}
		})
	}
}
