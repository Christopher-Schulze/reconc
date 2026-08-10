package parser

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
)

func TestParseActionPolicyProducesCompilableTypedPlan(t *testing.T) {
	t.Parallel()
	parsed, err := ParseRuleDocuments(actionBundle(`
actions:
  tools:
    - id: production-query
      transport: mcp_stdio
      server_label: warehouse
      tool: query
      effect:
        kind: external
  defaults:
    declared_tool: warn
    cache: never
  rules:
    - id: block-production
      selector:
        tool_ids: [production-query]
        phases: [pre_call]
      when:
        predicate:
          source: arguments
          pointer: /database
          op: eq
          value: production
      decision: block
      message: Production access is forbidden.
`))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Actions == nil || len(parsed.Actions.Tools) != 1 || len(parsed.Actions.Rules) != 1 {
		t.Fatalf("parsed actions = %#v", parsed.Actions)
	}
	compiled, err := action.CompilePlan(*parsed.Actions)
	if err != nil {
		t.Fatal(err)
	}
	plan := compiled.Plan()
	if plan.Defaults.DeclaredTool != action.DecisionWarn || plan.Defaults.Cache != action.CacheNever {
		t.Fatalf("compiled defaults = %#v", plan.Defaults)
	}
}

func TestParseActionPolicyRejectsStrictSurfaceViolations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "later-owned budget",
			body: "actions:\n  budgets: []\n",
			want: "unknown field",
		},
		{
			name: "unknown selector field",
			body: "actions:\n  rules:\n    - id: r\n      decision: block\n      selector:\n        server: x\n",
			want: "unknown field",
		},
		{
			name: "typed optional default",
			body: "actions:\n  defaults:\n    cache: true\n",
			want: "must be a string",
		},
		{
			name: "ambiguous YAML number",
			body: "actions:\n  rules:\n    - id: r\n      decision: block\n      when:\n        predicate:\n          source: context\n          pointer: /n\n          op: eq\n          value: 01\n",
			want: "ambiguous or invalid number",
		},
		{
			name: "nested duplicate JSON key",
			body: `{"actions":{"rules":[{"id":"r","decision":"block","when":{"predicate":{"source":"context","pointer":"/x","op":"eq","value":{"x":1,"x":2}}}}]}}`,
			want: "already defined",
		},
		{
			name: "YAML alias operand",
			body: "actions:\n  rules:\n    - id: r\n      decision: block\n      message: &x value\n      when:\n        predicate:\n          source: context\n          pointer: /x\n          op: eq\n          value: *x\n",
			want: "alias or unsupported",
		},
		{
			name: "custom YAML tag",
			body: "actions:\n  rules:\n    - id: r\n      decision: block\n      when:\n        predicate:\n          source: context\n          pointer: /x\n          op: eq\n          value: !secret value\n",
			want: "non-JSON YAML scalar",
		},
		{
			name: "multiple YAML documents",
			body: "actions: {}\n---\nactions: {}\n",
			want: "one document",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseRuleDocuments(actionBundle(test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestParseActionPolicyPreservesPresentEmptySelectorList(t *testing.T) {
	t.Parallel()
	parsed, err := ParseRuleDocuments(actionBundle("actions:\n  rules:\n    - id: r\n      decision: block\n      selector:\n        tools: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Actions.Rules[0].Selector.Tools == nil {
		t.Fatal("present empty list was collapsed to absent")
	}
	if _, err := action.CompilePlan(*parsed.Actions); err == nil || !strings.Contains(err.Error(), "empty present list") {
		t.Fatalf("compile error = %v", err)
	}
}

func actionBundle(content string) *ingest.SourceBundle {
	return &ingest.SourceBundle{Sources: []policy.PolicySource{{
		Kind: policy.SourceCompilerConfig, Path: ".reconc.yml", Content: content,
	}}}
}
