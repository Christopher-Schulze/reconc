package agentsession

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestCompositeWritePreventionBeforeSideEffect(t *testing.T) {
	const denyProtected = "      - kind: deny_write\n        paths: ['protected.txt']\n"
	const denyOther = "      - kind: deny_write\n        paths: ['other.txt']\n"
	const requireClaim = "      - kind: require_claim\n        claims: ['finished']\n"
	routes := []struct {
		name, payload string
		permission    bool
	}{
		{"write pre-tool", `{"session_id":"composite-write","tool_name":"Write","tool_input":{"file_path":"protected.txt","content":"changed"}}`, false},
		{"write permission", `{"session_id":"composite-write","tool_name":"Write","tool_input":{"file_path":"protected.txt","content":"changed"}}`, true},
		{"patch pre-tool", `{"session_id":"composite-write","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Update File: protected.txt\n@@\n-original\n+changed\n*** End Patch"}}`, false},
		{"patch permission", `{"session_id":"composite-write","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Update File: protected.txt\n@@\n-original\n+changed\n*** End Patch"}}`, true},
	}
	cases := []struct {
		name, kind, checks, mode, trigger string
		blocked                           bool
	}{
		{"all denies", "all_of", denyProtected + denyOther, "block", "**", true},
		{"all allows", "all_of", denyOther, "block", "**", false},
		{"any denies", "any_of", denyProtected + denyProtected, "block", "**", true},
		{"any allows", "any_of", denyProtected + denyOther, "block", "**", false},
		{"not denies", "not", denyOther, "block", "**", true},
		{"not allows", "not", denyProtected, "block", "**", false},
		{"mixed all denies", "all_of", requireClaim + denyProtected, "block", "**", true},
		{"mixed all defers claim", "all_of", requireClaim + denyOther, "block", "**", false},
		{"mixed all defers command", "all_of", denyOther + "      - kind: require_command_success\n        commands: ['go test ./...']\n", "block", "**", false},
		{"mixed all defers file", "all_of", denyOther + "      - kind: require_fresh_file\n        path: missing.txt\n", "block", "**", false},
		{"mixed all defers evidence", "all_of", denyOther + "      - kind: require_evidence\n        file: missing.txt\n        must_exist: true\n", "block", "**", false},
		{"mixed all defers script", "all_of", denyOther + "      - kind: require_script\n        script: scripts/missing.sh\n", "block", "**", false},
		{"completion only defers", "all_of", requireClaim, "block", "**", false},
		{"warning permits", "all_of", denyProtected, "warn", "**", false},
		{"unmatched parent permits", "all_of", denyProtected, "block", "src/**", false},
	}
	for _, test := range cases {
		for _, route := range routes {
			t.Run(test.name+"/"+route.name, func(t *testing.T) {
				t.Setenv("RECONC_HOME", t.TempDir())
				t.Setenv(StateRootEnv, t.TempDir())
				repo := t.TempDir()
				policy := fmt.Sprintf("rules:\n  - id: composite-write\n    kind: %s\n    mode: %s\n    message: composite write boundary\n    when_paths: ['%s']\n    checks:\n%s", test.kind, test.mode, test.trigger, test.checks)
				if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(policy), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(repo, "protected.txt")
				if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
					t.Fatal(err)
				}
				payload := []byte(route.payload)
				var result Result
				var blocked bool
				if route.permission {
					result = RunPermissionRequest(repo, payload)
					if result.ExitCode != 0 || result.Err != nil {
						t.Fatalf("permission route failed: %+v", result)
					}
					if result.Stdout != "" {
						var response struct {
							Output struct {
								Event    string `json:"hookEventName"`
								Decision struct {
									Behavior string `json:"behavior"`
									Message  string `json:"message"`
								} `json:"decision"`
							} `json:"hookSpecificOutput"`
						}
						if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
							t.Fatal(err)
						}
						blocked = response.Output.Event == "PermissionRequest" && response.Output.Decision.Behavior == "deny" && strings.Contains(response.Output.Decision.Message, "composite-write")
						if !blocked {
							t.Fatalf("unexpected permission response: %+v", result)
						}
					}
				} else {
					result = RunPreToolUse(repo, payload)
					blocked = result.ExitCode == 2 && strings.Contains(result.Stderr, "composite-write")
					if !blocked && (result.ExitCode != 0 || result.Err != nil) {
						t.Fatalf("pre-write route failed: %+v", result)
					}
				}
				if !blocked {
					if err := os.WriteFile(target, []byte("changed"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				contents, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				want := "changed"
				if test.blocked {
					want = "original"
				}
				if blocked != test.blocked || string(contents) != want {
					t.Fatalf("blocked=%t want=%t, target=%q want=%q, result=%+v", blocked, test.blocked, contents, want, result)
				}
			})
		}
	}
}
