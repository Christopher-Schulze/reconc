package agentsession

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/policy"
)

func TestNormalizedMCPToolAcceptsOnlyCompleteIdentities(t *testing.T) {
	cases := []struct {
		name string
		tool string
		want bool
	}{
		{"server and tool", "mcp__memory__create_entities", true},
		{"hyphenated server", "mcp__brave-search__web_search", true},
		{"plugin bundled server", "mcp__plugin_my-plugin_db__query", true},
		{"tool segment with separator", "mcp__files__write__file", true},
		{"no tool segment", "mcp__memory", false},
		{"empty tool segment", "mcp__memory__", false},
		{"empty server segment", "mcp____query", false},
		{"prefix only", "mcp__", false},
		{"builtin tool", "Write", false},
		{"lookalike prefix", "mcp_memory__query", false},
		{"empty", "", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizedMCPTool(test.tool); got != test.want {
				t.Fatalf("NormalizedMCPTool(%q) = %v, want %v", test.tool, got, test.want)
			}
		})
	}
}

// TestNormalizeNamespacedMCPPayloadKeepsOnlyTheRedactedContract proves the
// normalizer forwards the exact identity and the arguments, and nothing else:
// MCP payloads carry external server credentials and complete remote results.
func TestNormalizeNamespacedMCPPayloadKeepsOnlyTheRedactedContract(t *testing.T) {
	raw := mustJSON(t, map[string]interface{}{
		"session_id":      "session-1",
		"tool_name":       "mcp__filesystem__write_file",
		"tool_input":      map[string]interface{}{"path": "docs/out.md"},
		"transcript_path": "transcript-fixture.jsonl",
		"authorization":   "Bearer super-secret",
		"cwd":             "/repo",
	})
	normalized, err := NormalizeNamespacedMCPPayload(policy.MCPPlatformClaudeCode, true, []byte(raw))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(normalized, &out); err != nil {
		t.Fatalf("normalized payload is not JSON: %v", err)
	}
	for _, forbidden := range []string{"transcript_path", "authorization", "cwd", "tool_name"} {
		if _, present := out[forbidden]; present {
			t.Errorf("normalized MCP payload leaked %q", forbidden)
		}
	}
	envelope, ok := out["reconc_mcp"].(map[string]interface{})
	if !ok {
		t.Fatal("normalized MCP payload has no reconc_mcp envelope")
	}
	if envelope["platform"] != "claude-code" {
		t.Errorf("platform = %v, want claude-code", envelope["platform"])
	}
	if envelope["tool"] != "mcp__filesystem__write_file" {
		t.Errorf("tool = %v, want the exact host identity", envelope["tool"])
	}
	if envelope["blocking_pre_hook"] != true {
		t.Errorf("a pre-execution route must report a blocking pre hook")
	}
	if _, present := envelope["outcome"]; present {
		t.Errorf("a pre-execution route must not claim an outcome")
	}
	if envelope["input_valid"] != true {
		t.Errorf("object arguments must be reported as valid input")
	}

	payload, err := ParsePayload(normalized)
	if err != nil {
		t.Fatalf("normalized payload is not a valid hook payload: %v", err)
	}
	if payload.MCP == nil || payload.MCP.Tool != "mcp__filesystem__write_file" {
		t.Fatalf("normalized payload does not carry the MCP identity")
	}
}

func TestNormalizeNamespacedMCPPayloadRefusesDrift(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "builtin tool on the MCP route",
			payload: `{"session_id":"s","tool_name":"Write","tool_input":{}}`,
			want:    "is not an mcp__",
		},
		{
			name:    "incomplete namespace",
			payload: `{"session_id":"s","tool_name":"mcp__memory","tool_input":{}}`,
			want:    "is not an mcp__",
		},
		{
			name:    "no session identity",
			payload: `{"tool_name":"mcp__memory__query","tool_input":{}}`,
			want:    "no session identity",
		},
		{
			name:    "not an object",
			payload: `["mcp__memory__query"]`,
			want:    "not valid JSON",
		},
		{
			name:    "empty",
			payload: "   ",
			want:    "is empty",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeNamespacedMCPPayload(policy.MCPPlatformCodex, true, []byte(test.payload))
			if err == nil {
				t.Fatal("drift must be refused")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want it to mention %q", err, test.want)
			}
		})
	}
}

// TestNormalizeNamespacedMCPPayloadRequiresExplicitSuccess pins the post route
// to the same rule the hosts with native MCP events follow: a completed call is
// not a successful one, so positive evidence needs an explicit host result.
func TestNormalizeNamespacedMCPPayloadRequiresExplicitSuccess(t *testing.T) {
	cases := []struct {
		name     string
		response interface{}
		want     string
	}{
		{"explicit success", map[string]interface{}{"isError": false}, "success"},
		{"explicit error", map[string]interface{}{"isError": true}, "failure"},
		{"unknown shape", map[string]interface{}{"content": "text"}, "failure"},
		{"missing response", nil, "failure"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			body := map[string]interface{}{
				"session_id": "session-1",
				"tool_name":  "mcp__memory__query",
				"tool_input": map[string]interface{}{},
			}
			if test.response != nil {
				body["tool_response"] = test.response
			}
			normalized, err := NormalizeNamespacedMCPPayload(policy.MCPPlatformCodex, false, []byte(mustJSON(t, body)))
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			var out map[string]interface{}
			if err := json.Unmarshal(normalized, &out); err != nil {
				t.Fatal(err)
			}
			envelope := out["reconc_mcp"].(map[string]interface{})
			if envelope["outcome"] != test.want {
				t.Fatalf("outcome = %v, want %v", envelope["outcome"], test.want)
			}
			if envelope["blocking_pre_hook"] != false {
				t.Fatalf("a post route must not claim a blocking pre hook")
			}
		})
	}
}

// TestNamespacedMCPEnforcesRepositoryPolicy runs the complete path a Claude
// Code or Codex MCP call now takes: host payload, normalization, classification
// against the compiled policy, and the write decision.
func TestNamespacedMCPEnforcesRepositoryPolicy(t *testing.T) {
	repo := setupNamespacedMCPPolicyRepo(t)
	denied := mustJSON(t, map[string]interface{}{
		"session_id": "session-1",
		"tool_name":  "mcp__filesystem__write_file",
		"tool_input": map[string]interface{}{"path": "vendor/locked.txt"},
	})
	normalized, err := NormalizeNamespacedMCPPayload(policy.MCPPlatformClaudeCode, true, []byte(denied))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	result := RunMCPBefore(repo, normalized)
	if result.ExitCode != 2 {
		t.Fatalf("a denied MCP write must fail closed, got exit %d stderr=%q", result.ExitCode, result.Stderr)
	}

	allowed := mustJSON(t, map[string]interface{}{
		"session_id": "session-1",
		"tool_name":  "mcp__filesystem__write_file",
		"tool_input": map[string]interface{}{"path": "docs/notes.md"},
	})
	normalized, err = NormalizeNamespacedMCPPayload(policy.MCPPlatformClaudeCode, true, []byte(allowed))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if result := RunMCPBefore(repo, normalized); result.ExitCode != 0 {
		t.Fatalf("an allowed MCP write must pass, got exit %d stderr=%q", result.ExitCode, result.Stderr)
	}
}

// TestNamespacedMCPPlatformIsPartOfTheIdentity proves the platform is not
// cosmetic: the same tool name declared for one host does not classify a call
// from another.
func TestNamespacedMCPPlatformIsPartOfTheIdentity(t *testing.T) {
	repo := setupNamespacedMCPPolicyRepo(t)
	body := mustJSON(t, map[string]interface{}{
		"session_id": "session-1",
		"tool_name":  "mcp__filesystem__write_file",
		"tool_input": map[string]interface{}{"path": "vendor/locked.txt"},
	})
	normalized, err := NormalizeNamespacedMCPPayload(policy.MCPPlatformCodex, true, []byte(body))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	result := RunMCPBefore(repo, normalized)
	if result.ExitCode != 2 {
		t.Fatalf("the repository denies unclassified calls, so a foreign platform must fail closed, got exit %d", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "unclassified") {
		t.Fatalf("stderr = %q, want the unclassified deny reason", result.Stderr)
	}
}

func setupNamespacedMCPPolicyRepo(t *testing.T) string {
	t.Helper()
	repo := setupPolicyRepo(t)
	config := `rules:
  - id: locked-vendor
    kind: deny_write
    paths: ['vendor/**']
    mode: block
    message: vendor is locked
mcp:
  unclassified: deny
  tools:
    - platform: claude-code
      tool: mcp__filesystem__write_file
      effect: repository_write
      path_fields: [/path]
`
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile namespaced MCP repo: %v", err)
	}
	return repo
}
