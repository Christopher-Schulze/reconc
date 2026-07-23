package compiler

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
)

func TestCompileMCPContractIsCanonicalDeterministicAndRedacted(t *testing.T) {
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
	mcp := payload["mcp"].(map[string]interface{})
	tools := mcp["tools"].([]interface{})
	firstTool := tools[0].(map[string]interface{})
	secondTool := tools[1].(map[string]interface{})
	if firstTool["platform"] != "cursor" || secondTool["platform"] != "kilo" {
		t.Fatalf("MCP mappings are not canonical: %#v", tools)
	}
}

func TestValidateEmbeddedRulesBindsMCPContract(t *testing.T) {
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
	delete(payload, "mcp")
	if err := ValidateEmbeddedRules(payload, parsed); err == nil {
		t.Fatal("missing embedded MCP contract was accepted")
	}
}
