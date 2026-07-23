package parser

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestParseMCPPolicyNormalizesEveryEffectAndOrder(t *testing.T) {
	source := policy.PolicySource{
		Kind: policy.SourceCompilerConfig,
		Path: ".reconc.yml",
		Content: `mcp:
  tools:
    - platform: kilo
      tool: z_external
      effect: external
    - platform: cursor
      server_fingerprint: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
      tool: write_repo
      effect: repository_write
      path_fields: [/path, /more~1paths]
    - platform: opencode
      tool: run_check
      effect: command
      command_field: /command
    - platform: cursor
      tool: read_repo
      effect: repository_read
      path_fields: [/paths]
`,
	}
	parsed, err := ParseRuleDocuments(makeBundle(source))
	if err != nil {
		t.Fatalf("parse MCP policy: %v", err)
	}
	if parsed.MCP == nil {
		t.Fatal("MCP contract is nil")
	}
	if parsed.MCP.Unclassified != policy.MCPUnclassifiedHost {
		t.Fatalf("default unclassified=%q", parsed.MCP.Unclassified)
	}
	if len(parsed.MCP.Tools) != 4 {
		t.Fatalf("tool count=%d", len(parsed.MCP.Tools))
	}
	for index := 1; index < len(parsed.MCP.Tools); index++ {
		if parsed.MCP.Tools[index-1].StableKey() >= parsed.MCP.Tools[index].StableKey() {
			t.Fatalf("tools are not in canonical selector order: %#v", parsed.MCP.Tools)
		}
	}
	if parsed.MCP.Tools[0].SourcePath != ".reconc.yml" {
		t.Fatalf("source provenance missing: %#v", parsed.MCP.Tools[0])
	}
}

func TestParseMCPPolicyRejectsInvalidContracts(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"invalid unclassified", "mcp:\n  unclassified: maybe\n", "unclassified"},
		{"invalid platform", "mcp:\n  tools:\n    - platform: other\n      tool: x\n      effect: external\n", "platform"},
		{"invalid effect", "mcp:\n  tools:\n    - platform: cursor\n      tool: x\n      effect: mutate\n", "effect"},
		{"invalid fingerprint", "mcp:\n  tools:\n    - platform: cursor\n      server_fingerprint: sha256:ABC\n      tool: x\n      effect: external\n", "server_fingerprint"},
		{"invalid pointer", "mcp:\n  tools:\n    - platform: cursor\n      tool: x\n      effect: repository_write\n      path_fields: [path]\n", "JSON Pointer"},
		{"root path pointer", "mcp:\n  tools:\n    - platform: cursor\n      tool: x\n      effect: repository_write\n      path_fields: ['']\n", "non-empty"},
		{"duplicate path pointer", "mcp:\n  tools:\n    - platform: cursor\n      tool: x\n      effect: repository_write\n      path_fields: [/path, /path]\n", "duplicates"},
		{"missing path selector", "mcp:\n  tools:\n    - platform: cursor\n      tool: x\n      effect: repository_write\n", "path_fields"},
		{"forbidden command selector", "mcp:\n  tools:\n    - platform: cursor\n      tool: x\n      effect: external\n      command_field: /command\n", "forbidden"},
		{"duplicate identity", "mcp:\n  tools:\n    - platform: cursor\n      tool: x\n      effect: external\n    - platform: cursor\n      tool: x\n      effect: external\n", "duplicate"},
		{"surrounding tool whitespace", "mcp:\n  tools:\n    - platform: cursor\n      tool: ' x'\n      effect: external\n", "whitespace"},
		{"unknown field", "mcp:\n  catalog: true\n", "unknown field"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
				Kind:    policy.SourceCompilerConfig,
				Path:    ".reconc.yml",
				Content: test.body,
			}))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v; want substring %q", err, test.want)
			}
		})
	}
}

func TestParseMCPPolicyIsCompilerConfigOnly(t *testing.T) {
	_, err := ParseRuleDocuments(makeBundle(policy.PolicySource{
		Kind:    policy.SourcePolicyFile,
		Path:    "policies/mcp.yml",
		Content: "mcp:\n  unclassified: deny\n",
	}))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("policy-file MCP section error=%v", err)
	}
}
