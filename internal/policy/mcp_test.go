package policy

import (
	"strings"
	"testing"
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

	for _, platform := range []MCPPlatform{MCPPlatformCursor, MCPPlatformOpenCode, MCPPlatformKilo, MCPPlatformOMP} {
		if !platform.Valid() {
			t.Errorf("canonical platform %q is invalid", platform)
		}
	}
	if MCPPlatform("claude").Valid() {
		t.Error("unknown MCP platform must be invalid")
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
			tool: MCPToolPolicy{Platform: MCPPlatformOMP, Tool: "search", Effect: MCPEffectExternal},
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
