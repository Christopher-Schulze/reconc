package compiler

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
)

func TestCompileLegacyMCPIntoCanonicalActionsIsDeterministicAndRedacted(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, ".reconc.yml", `mcp:
  unclassified: deny
  tools:
    - platform: kilo
      tool: z_external
      effect: external
    - platform: cursor
      server_fingerprint: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
      tool: a_write
      effect: repository_write
      path_fields: [/path]
`)
	compiled, err := CompileRepoPolicy(repo, "test")
	if err != nil {
		t.Fatalf("compile 1: %v", err)
	}
	if compiled.MCPToolCount != 2 {
		t.Fatalf("MCP tool count=%d", compiled.MCPToolCount)
	}
	if compiled.ActionToolCount != 2 || compiled.ActionRuleCount != 0 {
		t.Fatalf("action counts=(%d,%d)", compiled.ActionToolCount, compiled.ActionRuleCount)
	}
	path := filepath.Join(repo, LockfileRelativePath)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(first, []byte("token=")) || bytes.Contains(first, []byte("Authorization")) {
		t.Fatal("lockfile contains raw credential material")
	}
	if _, err := CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile 2: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("identical MCP config did not compile byte-identically")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(first, &payload); err != nil {
		t.Fatal(err)
	}
	if _, parallel := payload["mcp"]; parallel {
		t.Fatal("format 5 retained a parallel MCP runtime plan")
	}
	actions := payload["actions"].(map[string]interface{})
	tools := actions["tools"].([]interface{})
	firstTool := tools[0].(map[string]interface{})
	secondTool := tools[1].(map[string]interface{})
	if firstTool["id"].(string) >= secondTool["id"].(string) || firstTool["origin"] != string(action.OriginLegacyMCP) || secondTool["origin"] != string(action.OriginLegacyMCP) {
		t.Fatalf("lowered action mappings are not canonical: %#v", tools)
	}
	seen := map[string]bool{firstTool["platform"].(string): true, secondTool["platform"].(string): true}
	if !seen["cursor"] || !seen["kilo"] {
		t.Fatalf("lowered action mappings changed identities: %#v", tools)
	}
}

func TestValidateEmbeddedRulesBindsCanonicalActionPlan(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, ".reconc.yml", "mcp:\n  tools:\n    - platform: cursor\n      tool: x\n      effect: external\n")
	if _, err := CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(repo, LockfileRelativePath)
	body, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	bundle, err := ingest.LoadPolicySources(repo)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseRuleDocuments(bundle)
	if err != nil {
		t.Fatal(err)
	}
	delete(payload, "actions")
	if err := ValidateEmbeddedRules(payload, parsed); err == nil {
		t.Fatal("missing embedded action plan was accepted")
	}
}
