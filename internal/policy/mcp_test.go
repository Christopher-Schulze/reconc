package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/schema"
)

func TestMCPEnumValidationIsClosed(t *testing.T) {
	for _, mode := range []MCPUnclassifiedMode{MCPUnclassifiedHost, MCPUnclassifiedDeny} {
		if !mode.Valid() {
			t.Errorf("canonical unclassified mode %q is invalid", mode)
		}
	}
	if MCPUnclassifiedMode("allow").Valid() {
		t.Error("unknown unclassified mode must be invalid")
	}

	for _, platform := range []MCPPlatform{MCPPlatformCursor, MCPPlatformOpenCode, MCPPlatformKilo, MCPPlatformOMP, MCPPlatformPi, MCPPlatformZCode} {
		if !platform.Valid() {
			t.Errorf("canonical platform %q is invalid", platform)
		}
	}
	if MCPPlatform("claude").Valid() {
		t.Error("unknown MCP platform must be invalid")
	}
	if !MCPPlatform("custom:local-agent").Valid() {
		t.Error("valid custom MCP platform must be accepted")
	}
	for _, invalid := range []MCPPlatform{"custom:", "custom:Local", "custom:-agent", "custom:agent/name"} {
		if invalid.Valid() {
			t.Errorf("invalid custom MCP platform %q must be rejected", invalid)
		}
	}

	for _, effect := range []MCPEffect{MCPEffectRepositoryRead, MCPEffectRepositoryWrite, MCPEffectCommand, MCPEffectExternal} {
		if !effect.Valid() {
			t.Errorf("canonical effect %q is invalid", effect)
		}
	}
	if MCPEffect("network").Valid() {
		t.Error("unknown MCP effect must be invalid")
	}
}

func TestMCPToolPolicyValidateAcceptsEveryEffectContract(t *testing.T) {
	fingerprint := "sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name string
		tool MCPToolPolicy
	}{
		{
			name: "repository read",
			tool: MCPToolPolicy{Platform: MCPPlatformCursor, ServerFingerprint: fingerprint, Tool: "read_file", Effect: MCPEffectRepositoryRead, PathFields: []string{"/path"}},
		},
		{
			name: "repository write",
			tool: MCPToolPolicy{Platform: MCPPlatformOpenCode, Tool: "write_file", Effect: MCPEffectRepositoryWrite, PathFields: []string{"/arguments/path", "/arguments/paths/0"}},
		},
		{
			name: "command",
			tool: MCPToolPolicy{Platform: MCPPlatformKilo, Tool: "execute", Effect: MCPEffectCommand, CommandField: "/command"},
		},
		{
			name: "external",
			tool: MCPToolPolicy{Platform: MCPPlatformPi, Tool: "search", Effect: MCPEffectExternal},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.tool.Validate(); err != nil {
				t.Fatalf("valid MCP tool rejected: %v", err)
			}
		})
	}
}

func TestMCPToolPolicyValidateRejectsEveryInvalidCrossFieldShape(t *testing.T) {
	validRead := MCPToolPolicy{Platform: MCPPlatformCursor, Tool: "read", Effect: MCPEffectRepositoryRead, PathFields: []string{"/path"}}
	tests := []struct {
		name      string
		mutate    func(*MCPToolPolicy)
		wantError string
	}{
		{"platform", func(tool *MCPToolPolicy) { tool.Platform = "other" }, "platform"},
		{"empty tool", func(tool *MCPToolPolicy) { tool.Tool = "" }, "tool"},
		{"tool whitespace", func(tool *MCPToolPolicy) { tool.Tool = " read" }, "tool"},
		{"fingerprint prefix", func(tool *MCPToolPolicy) { tool.ServerFingerprint = strings.Repeat("a", 64) }, "server_fingerprint"},
		{"fingerprint uppercase", func(tool *MCPToolPolicy) { tool.ServerFingerprint = "sha256:" + strings.Repeat("A", 64) }, "server_fingerprint"},
		{"effect", func(tool *MCPToolPolicy) { tool.Effect = "other" }, "effect"},
		{"empty path pointer", func(tool *MCPToolPolicy) { tool.PathFields = []string{""} }, "path_fields[0]"},
		{"invalid path pointer", func(tool *MCPToolPolicy) { tool.PathFields = []string{"path"} }, "path_fields[0]"},
		{"duplicate path pointer", func(tool *MCPToolPolicy) { tool.PathFields = []string{"/path", "/path"} }, "duplicates"},
		{"invalid command pointer", func(tool *MCPToolPolicy) { tool.CommandField = "/bad~2escape" }, "command_field"},
		{"repository path missing", func(tool *MCPToolPolicy) { tool.PathFields = nil }, "path_fields must be non-empty"},
		{"repository command field", func(tool *MCPToolPolicy) { tool.CommandField = "/command" }, "command_field is forbidden"},
		{"command field missing", func(tool *MCPToolPolicy) { tool.Effect = MCPEffectCommand; tool.PathFields = nil }, "command_field is required"},
		{"command path field", func(tool *MCPToolPolicy) { tool.Effect = MCPEffectCommand; tool.CommandField = "/command" }, "path_fields is forbidden"},
		{"external path field", func(tool *MCPToolPolicy) { tool.Effect = MCPEffectExternal }, "path_fields and command_field are forbidden"},
		{"external command field", func(tool *MCPToolPolicy) {
			tool.Effect = MCPEffectExternal
			tool.PathFields = nil
			tool.CommandField = "/command"
		}, "path_fields and command_field are forbidden"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tool := validRead
			tool.PathFields = append([]string(nil), validRead.PathFields...)
			test.mutate(&tool)
			err := tool.Validate()
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestMCPPolicyValidateRejectsInvalidModeToolAndDuplicateSelector(t *testing.T) {
	valid := MCPToolPolicy{Platform: MCPPlatformCursor, Tool: "read", Effect: MCPEffectRepositoryRead, PathFields: []string{"/path"}}
	tests := []struct {
		name      string
		policy    MCPPolicy
		wantError string
	}{
		{"mode", MCPPolicy{Unclassified: "allow"}, "mcp.unclassified"},
		{"tool", MCPPolicy{Unclassified: MCPUnclassifiedDeny, Tools: []MCPToolPolicy{{}}}, "mcp.tools[0]"},
		{"duplicate", MCPPolicy{Unclassified: MCPUnclassifiedHost, Tools: []MCPToolPolicy{valid, valid}}, "duplicate"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.policy.Validate()
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.wantError)
			}
		})
	}
	if err := (MCPPolicy{Unclassified: MCPUnclassifiedDeny, Tools: []MCPToolPolicy{valid}}).Validate(); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
}

func TestSortedMCPToolsIsCanonicalAndDefensive(t *testing.T) {
	input := []MCPToolPolicy{
		{Platform: MCPPlatformOpenCode, Tool: "z", PathFields: []string{"/z"}},
		{Platform: MCPPlatformCursor, Tool: "a", PathFields: []string{"/a"}},
	}
	got := SortedMCPTools(input)
	if got[0].Platform != MCPPlatformCursor || got[1].Platform != MCPPlatformOpenCode {
		t.Fatalf("canonical order = %+v", got)
	}
	got[0].PathFields[0] = "/mutated"
	if input[1].PathFields[0] != "/a" {
		t.Fatal("SortedMCPTools aliased the caller's nested PathFields slice")
	}
	if key := input[0].StableKey(); key != "opencode\x00\x00z" {
		t.Fatalf("StableKey() = %q", key)
	}
}

func TestValidJSONPointerCoversEscapesAndMalformedInputs(t *testing.T) {
	for _, pointer := range []string{"", "/", "/plain", "/a~0b", "/a~1b", "/0"} {
		if !ValidJSONPointer(pointer) {
			t.Errorf("valid pointer %q rejected", pointer)
		}
	}
	for _, pointer := range []string{"plain", "~", "/bad~", "/bad~2", "/bad~01~"} {
		if ValidJSONPointer(pointer) {
			t.Errorf("invalid pointer %q accepted", pointer)
		}
	}
}

func TestResolveJSONPointerTraversesObjectsAndArrays(t *testing.T) {
	root := map[string]interface{}{
		"nested": map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{"path/name": "src/app.go"},
			},
		},
	}
	value, ok := ResolveJSONPointer(root, "/nested/items/0/path~1name")
	if !ok || value != "src/app.go" {
		t.Fatalf("resolved value = %#v, ok = %v", value, ok)
	}

	for _, pointer := range []string{
		"not-a-pointer",
		"/missing",
		"/nested/items/-1",
		"/nested/items/00",
		"/nested/items/1",
		"/nested/items/-",
		"/nested/items/not-an-index",
	} {
		if value, ok := ResolveJSONPointer(root, pointer); ok {
			t.Fatalf("%s unexpectedly resolved to %#v", pointer, value)
		}
	}
	if value, ok := ResolveJSONPointer(root, ""); !ok || value == nil {
		t.Fatalf("root pointer resolved to %#v, ok = %v", value, ok)
	}
	if value, ok := ResolveJSONPointer(map[string]interface{}{"scalar": "value"}, "/scalar/child"); ok {
		t.Fatalf("pointer unexpectedly traversed scalar to %#v", value)
	}
}

// TestPublishedSchemasAcceptExactlyTheBuiltinMCPPlatforms keeps the two live
// JSON contracts and the Go vocabulary from drifting apart. A platform Go
// accepts but a schema rejects makes a valid policy fail external validation; a
// platform a schema accepts but Go rejects promises enforcement that never runs.
func TestPublishedSchemasAcceptExactlyTheBuiltinMCPPlatforms(t *testing.T) {
	policyConfig, ok := schema.CurrentContract(schema.PolicyConfig)
	if !ok {
		t.Fatal("schema registry has no current policy-config contract")
	}
	policyLock, ok := schema.CurrentContract(schema.PolicyLock)
	if !ok {
		t.Fatal("schema registry has no current policy-lock contract")
	}
	contracts := []struct {
		path    string
		pointer []string
	}{
		{filepath.Join("..", "..", filepath.FromSlash(policyConfig.LocalPath)), []string{"$defs", "mcpTool", "properties", "platform"}},
		{filepath.Join("..", "..", filepath.FromSlash(policyLock.LocalPath)), []string{"$defs", "mcpTool", "properties", "platform"}},
	}
	want := make([]string, 0, len(BuiltinMCPPlatforms()))
	for _, platform := range BuiltinMCPPlatforms() {
		want = append(want, string(platform))
	}
	sort.Strings(want)

	for _, contract := range contracts {
		t.Run(filepath.Base(filepath.Dir(contract.path)), func(t *testing.T) {
			data, err := os.ReadFile(contract.path)
			if err != nil {
				t.Fatalf("read schema: %v", err)
			}
			var document map[string]interface{}
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatalf("parse schema: %v", err)
			}
			node := interface{}(document)
			for _, key := range contract.pointer {
				object, ok := node.(map[string]interface{})
				if !ok {
					t.Fatalf("schema has no object at %q", key)
				}
				node, ok = object[key]
				if !ok {
					t.Fatalf("schema has no %q member", key)
				}
			}
			got := schemaPlatformEnum(t, node)
			sort.Strings(got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("schema platform enum = %v, want %v", got, want)
			}
		})
	}
}

// schemaPlatformEnum collects the enumerated platform values from the
// `anyOf` branch of a platform property, ignoring the `custom:` pattern branch.
func schemaPlatformEnum(t *testing.T, node interface{}) []string {
	t.Helper()
	property, ok := node.(map[string]interface{})
	if !ok {
		t.Fatal("platform property is not an object")
	}
	branches, ok := property["anyOf"].([]interface{})
	if !ok {
		t.Fatal("platform property has no anyOf branches")
	}
	var values []string
	enumBranches := 0
	for _, branch := range branches {
		object, ok := branch.(map[string]interface{})
		if !ok {
			continue
		}
		entries, ok := object["enum"].([]interface{})
		if !ok {
			continue
		}
		enumBranches++
		for _, entry := range entries {
			text, ok := entry.(string)
			if !ok {
				t.Fatalf("platform enum holds a non-string value %#v", entry)
			}
			values = append(values, text)
		}
	}
	if enumBranches != 1 {
		t.Fatalf("platform property has %d enum branches, want exactly one", enumBranches)
	}
	return values
}
