package templates

import (
	"reflect"
	"strings"
	"testing"
)

func TestVariablesCanonicalGrammar(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantNames []string
		wantErr   string
	}{
		{name: "single", input: "docs/{task_id}.md", wantNames: []string{"task_id"}},
		{name: "repeated", input: "{x}/{x}", wantNames: []string{"x", "x"}},
		{name: "alternation", input: "src/**/*.{js,ts}", wantNames: nil},
		{name: "nested alternation", input: "{src,{pkg,lib}}/{module}.go", wantNames: []string{"module"}},
		{name: "unicode literal", input: "über/{模块}.txt", wantErr: "invalid brace expression"},
		{name: "escaped braces", input: `\{literal\}`, wantNames: nil},
		{name: "hyphenated token", input: "docs/{task-id}.md", wantErr: "invalid brace expression"},
		{name: "unmatched opening", input: "docs/{task_id.md", wantErr: "unterminated brace expression"},
		{name: "unmatched closing", input: "docs/task}.md", wantErr: "unmatched closing brace"},
		{name: "dangling escape", input: `docs/task\`, wantErr: "dangling escape"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			variables, err := Variables(test.input)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("Variables(%q) error = %v, want %q", test.input, err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Variables(%q): %v", test.input, err)
			}
			gotNames := make([]string, len(variables))
			for index, variable := range variables {
				gotNames[index] = variable.Name
			}
			if len(gotNames) == 0 {
				gotNames = nil
			}
			if !reflect.DeepEqual(gotNames, test.wantNames) {
				t.Fatalf("Variables(%q) names = %v, want %v", test.input, gotNames, test.wantNames)
			}
		})
	}
}

func TestMaskVariablesAndSubstitute(t *testing.T) {
	masked, err := MaskVariables("docs/{task_id}/{name}.md", "*")
	if err != nil || masked != "docs/*/*.md" {
		t.Fatalf("MaskVariables() = %q, %v", masked, err)
	}
	substituted, err := Substitute("docs/{task_id}/{name}.md", map[string]string{"task_id": "TODO-1", "name": "report"})
	if err != nil || substituted != "docs/TODO-1/report.md" {
		t.Fatalf("Substitute() = %q, %v", substituted, err)
	}
	partial, err := Substitute("{z}/{a}", map[string]string{"z": "value"})
	if err == nil || partial != "value/{a}" || !strings.Contains(err.Error(), "a") {
		t.Fatalf("missing binding result = %q, %v", partial, err)
	}
}
