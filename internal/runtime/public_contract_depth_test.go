package runtime

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/policy"
)

func TestAssertRuleByIDSynthesizesTemplateTrigger(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", `rules:
  - id: templated-deny
    kind: deny_write
    when_paths: ['docs/todo/{task_id}.md']
    paths: ['docs/todo/**']
    mode: block
    message: generated TODO is protected
  - id: unrelated
    kind: require_claim
    when_paths: ['src/**']
    claims: [approved]
    mode: block
    message: approval required
`)

	report, err := AssertRuleByID(repo, "templated-deny", map[string]string{"task_id": "TODO-001"}, Empty())
	if err != nil {
		t.Fatalf("AssertRuleByID: %v", err)
	}
	if report.Decision != DecisionBlock || len(report.Violations) != 1 ||
		report.Violations[0].RuleID != "templated-deny" ||
		!strings.Contains(strings.Join(report.Inputs.WritePaths, ","), "TODO-001") {
		t.Fatalf("unexpected assertion report: %+v", report)
	}

	if _, err := AssertRuleByID(repo, "templated-deny", nil, Empty()); err == nil ||
		!strings.Contains(err.Error(), "provide via --var") {
		t.Fatalf("missing template variable was accepted: %v", err)
	}
	if _, err := AssertRuleByID(repo, "missing", nil, Empty()); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing rule was accepted: %v", err)
	}
}

func TestCheckRepoPolicyForKindsFiltersWithoutWeakeningEvaluation(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", `rules:
  - id: deny-generated
    kind: deny_write
    paths: ['generated/**']
    mode: block
    message: generated writes forbidden
  - id: approval
    kind: require_claim
    when_paths: ['src/**']
    claims: [approved]
    mode: block
    message: approval required
`)
	inputs := Empty()
	inputs.WritePaths = []string{"generated/out.go", "src/main.go"}

	report, err := CheckRepoPolicyForKinds(repo, inputs, map[policy.Kind]struct{}{
		policy.KindRequireClaim: {},
	})
	if err != nil {
		t.Fatalf("CheckRepoPolicyForKinds: %v", err)
	}
	if len(report.Violations) != 1 || report.Violations[0].RuleID != "approval" {
		t.Fatalf("kind filter evaluated wrong rules: %+v", report.Violations)
	}

	report, err = CheckRepoPolicyForKinds(repo, inputs, map[policy.Kind]struct{}{})
	if err != nil {
		t.Fatalf("CheckRepoPolicyForKinds(empty): %v", err)
	}
	if !report.OK || len(report.Violations) != 0 {
		t.Fatalf("empty kind set did not exclude all rules: %+v", report)
	}
}

func TestLoadMCPPolicyReturnsCanonicalOptionalContract(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", `mcp:
  unclassified: deny
  tools:
    - platform: kilo
      tool: z_external
      effect: external
    - platform: cursor
      tool: a_write
      effect: repository_write
      path_fields: [/path]
`, "")
	contract, err := LoadMCPPolicy(repo)
	if err != nil {
		t.Fatalf("LoadMCPPolicy: %v", err)
	}
	if contract == nil || contract.Unclassified != policy.MCPUnclassifiedDeny ||
		len(contract.Tools) != 2 || contract.Tools[0].Tool != "a_write" || contract.Tools[1].Tool != "z_external" {
		t.Fatalf("unexpected MCP contract: %+v", contract)
	}
	actions, err := LoadActionPlan(repo)
	if err != nil {
		t.Fatalf("LoadActionPlan: %v", err)
	}
	if len(actions.Tools) != 2 || actions.Tools[0].Origin != action.OriginLegacyMCP || actions.Defaults.HostUnmatched != action.DecisionBlock {
		t.Fatalf("unexpected canonical action plan: %+v", actions)
	}

	without := makeRepo(t, "# project\n", "", "rules: []\n")
	contract, err = LoadMCPPolicy(without)
	if err != nil || contract != nil {
		t.Fatalf("absent MCP contract = (%+v, %v)", contract, err)
	}
}

func TestDecodeActionPlanRejectsMalformedPayloads(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload map[string]interface{}
		err     string
	}{
		{name: "missing", payload: map[string]interface{}{}, err: "actions object"},
		{name: "wrong type", payload: map[string]interface{}{"actions": "deny"}, err: "plan is invalid"},
		{name: "unknown field", payload: map[string]interface{}{"actions": map[string]interface{}{"format_version": "1", "tools": []interface{}{}, "rules": []interface{}{}, "defaults": map[string]interface{}{}, "extra": true}}, err: "plan is invalid"},
		{name: "invalid default", payload: map[string]interface{}{"actions": map[string]interface{}{"format_version": "1", "tools": []interface{}{}, "rules": []interface{}{}, "defaults": map[string]interface{}{"host_unmatched": "warn"}}}, err: "host_unmatched"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeActionPlan(test.payload); err == nil || !strings.Contains(err.Error(), test.err) {
				t.Fatalf("expected %q error, got %v", test.err, err)
			}
		})
	}
}

func TestRuntimeIntegerFormattingContracts(t *testing.T) {
	for _, test := range []struct {
		value int
		want  string
	}{
		{value: 0, want: "0"},
		{value: 7, want: "7"},
		{value: -42, want: "-42"},
		{value: 12345, want: "12345"},
		{value: math.MinInt, want: strconv.FormatInt(int64(math.MinInt), 10)},
	} {
		if got := itoa(test.value); got != test.want {
			t.Fatalf("itoa(%d) = %q, want %q", test.value, got, test.want)
		}
	}
	if got := numAsInt(json.Number("12")); got != 12 {
		t.Fatalf("numAsInt(json.Number) = %d", got)
	}
	if got := numAsInt(13); got != 13 {
		t.Fatalf("numAsInt(int) = %d", got)
	}
	if got := numAsInt(float64(14)); got != 14 {
		t.Fatalf("numAsInt(float64) = %d", got)
	}
	if got := numAsInt("15"); got != 0 {
		t.Fatalf("numAsInt(unsupported) = %d", got)
	}
}
