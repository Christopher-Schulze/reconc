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
	if envelope["phase"] != "before" {
		t.Errorf("phase = %v, want before", envelope["phase"])
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
			if envelope["blocking_pre_hook"] != true {
				t.Fatalf("a post route must retain its paired blocking capability")
			}
			if envelope["phase"] != "after" {
				t.Fatalf("phase = %v, want after", envelope["phase"])
			}
		})
	}
}

func TestNamespacedMCPAuditSeparatesPhaseFromStrictCapability(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := setupNamespacedMCPAuditRepo(t)

	run := func(platform policy.MCPPlatform, before bool, tool, path string, response map[string]interface{}, wantExit int) {
		t.Helper()
		body := map[string]interface{}{
			"session_id": "session-" + string(platform),
			"tool_name":  tool,
			"tool_input": map[string]interface{}{"path": path},
		}
		if response != nil {
			body["tool_response"] = response
		}
		normalized, err := NormalizeNamespacedMCPPayload(platform, before, []byte(mustJSON(t, body)))
		if err != nil {
			t.Fatalf("normalize %s: %v", platform, err)
		}
		var result Result
		if before {
			result = RunMCPBefore(repo, normalized)
		} else {
			result = RunMCPAfter(repo, normalized)
		}
		if result.ExitCode != wantExit {
			t.Fatalf("%s before=%t exit=%d stderr=%q, want %d", platform, before, result.ExitCode, result.Stderr, wantExit)
		}
	}

	writeTool := "mcp__filesystem__write_file"
	run(policy.MCPPlatformClaudeCode, true, writeTool, "docs/claude.md", nil, 0)
	run(policy.MCPPlatformClaudeCode, false, writeTool, "docs/claude.md", map[string]interface{}{"isError": false}, 0)
	run(policy.MCPPlatformCodex, true, writeTool, "docs/codex.md", nil, 0)
	run(policy.MCPPlatformCodex, false, writeTool, "docs/codex.md", map[string]interface{}{"isError": true}, 0)
	run(policy.MCPPlatformCodex, false, writeTool, "docs/post-only.md", map[string]interface{}{"isError": false}, 0)
	run(policy.MCPPlatformClaudeCode, true, writeTool, "vendor/locked.txt", nil, 2)
	run(policy.MCPPlatformClaudeCode, true, "mcp__memory__query", "docs/unknown.md", nil, 2)
	run(policy.MCPPlatformClaudeCode, false, "mcp__memory__query", "docs/unknown.md", map[string]interface{}{"isError": true}, 0)

	summary, err := ReadMCPAudit(repo)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Classified["claude-code/repository_write"] != 3 || summary.Classified["codex/repository_write"] != 3 {
		t.Fatalf("classified counters = %#v", summary.Classified)
	}
	if summary.Unclassified["claude-code"] != 2 || summary.Denied["claude-code"] != 2 {
		t.Fatalf("unclassified/denied counters = %#v / %#v", summary.Unclassified, summary.Denied)
	}
	if summary.Failures["codex"] != 1 || summary.Failures["claude-code"] != 0 {
		t.Fatalf("failure counters = %#v", summary.Failures)
	}
	if len(summary.StrictUnavailable) != 0 {
		t.Fatalf("namespaced blocking routes reported strict-unavailable: %#v", summary.StrictUnavailable)
	}
	if len(summary.Events) != 8 {
		t.Fatalf("events = %d, want 8", len(summary.Events))
	}
	for index, entry := range summary.Events {
		wantPhase := MCPPhaseBefore
		if index == 1 || index == 3 || index == 4 || index == 7 {
			wantPhase = MCPPhaseAfter
		}
		if entry.Phase != wantPhase || !entry.StrictAvailable {
			t.Fatalf("event %d phase/capability = %q/%t, want %q/true", index, entry.Phase, entry.StrictAvailable, wantPhase)
		}
	}
	for _, pair := range [][2]int{{0, 1}, {2, 3}, {6, 7}} {
		if summary.Events[pair[0]].SelectorHash != summary.Events[pair[1]].SelectorHash {
			t.Fatalf("paired events %v changed capability identity", pair)
		}
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

func setupNamespacedMCPAuditRepo(t *testing.T) string {
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
    - platform: codex
      tool: mcp__filesystem__write_file
      effect: repository_write
      path_fields: [/path]
`
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile namespaced MCP audit repo: %v", err)
	}
	return repo
}
