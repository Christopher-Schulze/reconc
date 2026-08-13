package parser

import (
	"fmt"
	"math"
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
      cost_units: 3
      max_result_bytes: 4096
      ledger_name_safe: true
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
  budgets:
    - id: query-run-cap
      selector:
        tool_ids: [production-query]
      limits:
        call_count: 10
        result_bytes: 40960
        cost_units: 30
      reset: operator_run
      on_exhaustion: block
  approvals:
    - id: production-summary
      selector:
        tool_ids: [production-query]
        phases: [pre_call]
      selected_arguments: [/database]
  ledger:
    mode: best_effort
    tool_identity: exact_name
    selected_fields:
      - source: result
        pointer: /rows
      - source: arguments
        pointer: /database
`))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Actions == nil || len(parsed.Actions.Tools) != 1 ||
		len(parsed.Actions.Rules) != 1 || len(parsed.Actions.Budgets) != 1 ||
		len(parsed.Actions.Approvals) != 1 || parsed.Actions.Ledger == nil ||
		len(parsed.Actions.Ledger.SelectedFields) != 2 {
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
	if plan.Ledger.Mode != action.LedgerBestEffort || plan.Ledger.ToolIdentity != action.LedgerExactName ||
		plan.Ledger.SelectedFields[0].Source != action.SourceArguments {
		t.Fatalf("compiled ledger = %#v", plan.Ledger)
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
			name: "non-boolean ledger name safety",
			body: "actions:\n  tools:\n    - id: tool\n      transport: mcp_stdio\n      server_label: server\n      tool: query\n      effect: {kind: external}\n      ledger_name_safe: yes\n",
			want: "canonical boolean",
		},
		{
			name: "unknown ledger field",
			body: "actions:\n  ledger:\n    raw_results: true\n",
			want: "unknown field",
		},
		{
			name: "repository-owned approval authority",
			body: "actions:\n  approvals:\n    - id: disclosure\n      selector: {tool_ids: [tool]}\n      selected_arguments: [/value]\n      authority_key_id: repository-key\n",
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

func TestParseActionBudgetLimitsRejectEveryNonPositiveOrNonCanonicalValue(t *testing.T) {
	t.Parallel()
	for _, field := range []string{
		"call_count", "denied_count", "approval_count", "argument_bytes",
		"result_bytes", "cost_units", "concurrent", "rate_window",
	} {
		field := field
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			body := fmt.Sprintf(`actions:
  budgets:
    - id: invalid
      selector: {tool_ids: [tool]}
      limits: {%s: 0}
      reset: never
      on_exhaustion: block
`, field)
			if _, err := ParseRuleDocuments(actionBundle(body)); err == nil ||
				!strings.Contains(err.Error(), "must be between 1") {
				t.Fatalf("zero %s error = %v", field, err)
			}
		})
	}
	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "negative", value: "-1"},
		{name: "explicit plus", value: "+1"},
		{name: "leading zero", value: "01"},
		{name: "overflow", value: "9223372036854775808"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := "actions:\n  budgets:\n    - id: invalid\n      selector: {tool_ids: [tool]}\n" +
				"      limits: {call_count: " + test.value + "}\n      reset: never\n      on_exhaustion: block\n"
			if _, err := ParseRuleDocuments(actionBundle(body)); err == nil {
				t.Fatalf("non-canonical budget value %q was accepted", test.value)
			}
		})
	}
	unknown := `actions:
  budgets:
    - id: invalid
      selector: {tool_ids: [tool]}
      limits: {calls: 1}
      reset: never
      on_exhaustion: block
`
	if _, err := ParseRuleDocuments(actionBundle(unknown)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown budget limit error = %v", err)
	}
}

func TestParseActionBudgetWindowEnforcesUint32Range(t *testing.T) {
	t.Parallel()
	parse := func(value uint64) (*ParsedPolicy, error) {
		body := fmt.Sprintf(`actions:
  budgets:
    - id: bounded-window
      selector: {tool_ids: [tool]}
      limits: {call_count: 1}
      reset: fixed_window
      window_seconds: %d
      on_exhaustion: block
`, value)
		return ParseRuleDocuments(actionBundle(body))
	}
	parsed, err := parse(math.MaxUint32)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Actions.Budgets[0].WindowSeconds; got != math.MaxUint32 {
		t.Fatalf("window_seconds = %d, want %d", got, uint32(math.MaxUint32))
	}
	if _, err := parse(uint64(math.MaxUint32) + 1); err == nil ||
		!strings.Contains(err.Error(), "must be between 1 and 4294967295") {
		t.Fatalf("overflowing window error = %v", err)
	}
}

func TestParseActionPolicyMergesOnlyExplicitImpactCandidateSources(t *testing.T) {
	t.Parallel()
	base := policy.PolicySource{
		Kind: policy.SourceCompilerConfig, Path: ".reconc.yml",
		Content: `actions:
  tools:
    - id: database-write
      transport: mcp_stdio
      server_label: database
      tool: execute
      effect:
        kind: external
  defaults:
    declared_tool: allow
`,
	}
	candidate := policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: ".reconc/impact/candidate.yml",
		BlockID: policy.ImpactCandidateBlockPrefix + "candidate",
		Content: `actions:
  defaults:
    declared_tool: warn
  rules:
    - id: block-production
      selector:
        tool_ids: [database-write]
      decision: block
  approvals:
    - id: database-summary
      selector:
        tool_ids: [database-write]
        phases: [pre_call]
      selected_arguments: [/target]
`,
	}
	parsed, err := ParseRuleDocuments(&ingest.SourceBundle{Sources: []policy.PolicySource{base, candidate}})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Actions == nil || len(parsed.Actions.Tools) != 1 || len(parsed.Actions.Rules) != 1 ||
		len(parsed.Actions.Approvals) != 1 ||
		parsed.Actions.Defaults.DeclaredTool != action.DecisionWarn ||
		parsed.Actions.Rules[0].SourceIdentity != candidate.Path ||
		parsed.Actions.Approvals[0].SourceIdentity != candidate.Path {
		t.Fatalf("merged action candidate = %#v", parsed.Actions)
	}
	if _, err := action.CompilePlan(*parsed.Actions); err != nil {
		t.Fatal(err)
	}

	candidate.BlockID = "ordinary-policy-fragment"
	if _, err := ParseRuleDocuments(&ingest.SourceBundle{Sources: []policy.PolicySource{base, candidate}}); err == nil ||
		!strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ordinary fragment gained action authority: %v", err)
	}
}

func TestParseActionPolicyMergesSemanticallyIdenticalLedgerDefaults(t *testing.T) {
	t.Parallel()
	base := policy.PolicySource{
		Kind: policy.SourceCompilerConfig, Path: ".reconc.yml",
		Content: "actions:\n  ledger: {}\n",
	}
	candidate := policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: ".reconc/impact/candidate.yml",
		BlockID: policy.ImpactCandidateBlockPrefix + "candidate",
		Content: "actions:\n  ledger:\n    mode: required\n    tool_identity: declaration_id\n    selected_fields: []\n",
	}
	parsed, err := ParseRuleDocuments(&ingest.SourceBundle{Sources: []policy.PolicySource{base, candidate}})
	if err != nil {
		t.Fatal(err)
	}
	ledger := parsed.Actions.Ledger
	if ledger == nil || ledger.Mode != action.LedgerRequired ||
		ledger.ToolIdentity != action.LedgerDeclarationID || ledger.SelectedFields == nil ||
		len(ledger.SelectedFields) != 0 {
		t.Fatalf("merged ledger = %#v", ledger)
	}
}

func actionBundle(content string) *ingest.SourceBundle {
	return &ingest.SourceBundle{Sources: []policy.PolicySource{{
		Kind: policy.SourceCompilerConfig, Path: ".reconc.yml", Content: content,
	}}}
}
