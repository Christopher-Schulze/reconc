package compiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/policy"
)

func TestCompileCanonicalActionsCoversPureLegacyMixedAndConflictingInputs(t *testing.T) {
	legacyTool := policy.MCPToolPolicy{
		Platform: policy.MCPPlatformCursor, Tool: "write", Effect: policy.MCPEffectRepositoryWrite,
		PathFields: []string{"/path"}, SourcePath: ".reconc.yml",
	}
	legacyIDMaterial := string(legacyTool.Platform) + "\x00" + legacyTool.ServerFingerprint + "\x00" + legacyTool.Tool
	legacyDigest := sha256.Sum256([]byte(legacyIDMaterial))
	legacyID := legacyMCPIDPrefix + hex.EncodeToString(legacyDigest[:])[:48]
	pureTool := action.Tool{
		ID: "gateway-query", Transport: action.TransportMCPStdio, ServerLabel: "warehouse", Tool: "query",
		Effect: action.Effect{Kind: action.EffectExternal}, Origin: action.OriginActions, SourceIdentity: ".reconc.yml",
	}
	tests := []struct {
		name    string
		parsed  parser.ParsedPolicy
		wantIDs []string
		wantErr string
	}{
		{
			name: "pure actions",
			parsed: parser.ParsedPolicy{Actions: &action.Plan{
				FormatVersion: action.PlanFormatVersion, Tools: []action.Tool{pureTool},
			}},
			wantIDs: []string{"gateway-query"},
		},
		{
			name: "legacy lowering",
			parsed: parser.ParsedPolicy{MCP: &policy.MCPPolicy{
				Unclassified: policy.MCPUnclassifiedDeny, Tools: []policy.MCPToolPolicy{legacyTool},
			}},
			wantIDs: []string{legacyID},
		},
		{
			name: "mixed disjoint with rule targeting lowered id",
			parsed: parser.ParsedPolicy{
				Actions: &action.Plan{FormatVersion: action.PlanFormatVersion, Tools: []action.Tool{pureTool}, Rules: []action.Rule{{
					ID: "warn-legacy", Selector: action.Selector{ToolIDs: []string{legacyID}},
					Decision: action.DecisionWarn, SourceIdentity: ".reconc.yml",
				}}},
				MCP: &policy.MCPPolicy{Unclassified: policy.MCPUnclassifiedHost, Tools: []policy.MCPToolPolicy{legacyTool}},
			},
			wantIDs: []string{"gateway-query", legacyID},
		},
		{
			name: "overlap",
			parsed: parser.ParsedPolicy{
				Actions: &action.Plan{FormatVersion: action.PlanFormatVersion, Tools: []action.Tool{{
					ID: "action-write", Transport: action.TransportHostMCP, Platform: action.PlatformCursor,
					Tool: "write", Effect: action.Effect{Kind: action.EffectRepositoryWrite, PathFields: []string{"/path"}},
					Origin: action.OriginActions, SourceIdentity: ".reconc.yml",
				}}},
				MCP: &policy.MCPPolicy{Unclassified: policy.MCPUnclassifiedHost, Tools: []policy.MCPToolPolicy{legacyTool}},
			},
			wantErr: "same exact tool declaration",
		},
		{
			name: "default conflict",
			parsed: parser.ParsedPolicy{
				Actions: &action.Plan{FormatVersion: action.PlanFormatVersion, Defaults: action.Defaults{HostUnmatched: action.DecisionAllow}},
				MCP:     &policy.MCPPolicy{Unclassified: policy.MCPUnclassifiedDeny},
			},
			wantErr: "conflicts",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := compileCanonicalActions(&test.parsed)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			plan := compiled.Plan()
			if len(plan.Tools) != len(test.wantIDs) {
				t.Fatalf("tools = %#v", plan.Tools)
			}
			for index, want := range test.wantIDs {
				if plan.Tools[index].ID != want {
					t.Fatalf("tool ids = %#v, want %#v", plan.Tools, test.wantIDs)
				}
			}
		})
	}
}

func TestLegacyMCPIDUsesTheFrozenIdentityMaterial(t *testing.T) {
	tool := policy.MCPToolPolicy{
		Platform:          policy.MCPPlatformCodex,
		ServerFingerprint: "sha256:" + strings.Repeat("a", 64),
		Tool:              "mcp__db__query", Effect: policy.MCPEffectExternal, SourcePath: ".reconc.yml",
	}
	material := "codex\x00" + tool.ServerFingerprint + "\x00mcp__db__query"
	digest := sha256.Sum256([]byte(material))
	want := legacyMCPIDPrefix + hex.EncodeToString(digest[:])[:48]
	if got := lowerLegacyMCPTool(tool).ID; got != want {
		t.Fatalf("legacy id = %s, want %s", got, want)
	}
}

func TestCanonicalActionPlanGolden(t *testing.T) {
	config := readActionGolden(t, "testdata/action_config.yml")
	parsed, err := parser.ParseRuleDocuments(&ingest.SourceBundle{Sources: []policy.PolicySource{{
		Kind: policy.SourceCompilerConfig, Path: ".reconc.yml", Content: string(config),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compileCanonicalActions(parsed)
	if err != nil {
		t.Fatal(err)
	}
	assertActionGolden(t, compiled.Plan(), "testdata/action_plan.golden.json")
}

func TestLegacyActionMigrationGolden(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "")
	body := readActionGolden(t, "testdata/action_migration_v4.json")
	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	digest, err := ComputeLockDigest(payload)
	if err != nil {
		t.Fatal(err)
	}
	payload["lock_digest"] = digest
	migrated, _, err := MigrateLockfile(payload)
	if err != nil {
		t.Fatal(err)
	}
	assertActionGolden(t, migrated["actions"], "testdata/action_migration_plan.golden.json")
}

func assertActionGolden(t *testing.T, value interface{}, path string) {
	t.Helper()
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, '\n')
	want := readActionGolden(t, path)
	if !bytes.Equal(body, want) {
		t.Fatalf("action golden mismatch\ngot:\n%s\nwant:\n%s", body, want)
	}
}

func readActionGolden(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
