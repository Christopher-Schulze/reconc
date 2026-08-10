package action

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecisionMergeIsMonotonicForEveryPair(t *testing.T) {
	t.Parallel()
	decisions := []Decision{
		DecisionAllow, DecisionWarn, DecisionRequireApproval, DecisionBlock,
	}
	for _, left := range decisions {
		for _, right := range decisions {
			name := string(left) + "+" + string(right)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				rules := []Rule{
					{ID: "left", Decision: left, SourceIdentity: ".reconc.yml"},
					{ID: "right", Decision: right, SourceIdentity: ".reconc.yml"},
				}
				evaluator, input := testActionEvaluator(t, rules, Defaults{}, testExternalEffect())
				result := evaluator.Evaluate(input)
				want := left
				if right.Strength() > left.Strength() {
					want = right
				}
				if result.Decision != want {
					t.Fatalf("decision = %s, want %s", result.Decision, want)
				}
			})
		}
	}
}

func TestRuleDeclarationOrderHasNoSemanticEffect(t *testing.T) {
	t.Parallel()
	rules := []Rule{
		{ID: "warn", Decision: DecisionWarn, SourceIdentity: ".reconc.yml"},
		{ID: "block", Decision: DecisionBlock, SourceIdentity: ".reconc.yml"},
		{ID: "allow", Decision: DecisionAllow, SourceIdentity: ".reconc.yml"},
	}
	permutations := [][]Rule{
		{rules[0], rules[1], rules[2]},
		{rules[2], rules[0], rules[1]},
		{rules[1], rules[2], rules[0]},
	}
	var canonical []byte
	for index, permutation := range permutations {
		evaluator, input := testActionEvaluator(t, permutation, Defaults{}, testExternalEffect())
		result := evaluator.Evaluate(input)
		body, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			canonical = body
			continue
		}
		if string(body) != string(canonical) {
			t.Fatalf("permutation %d changed result\nfirst=%s\nnext=%s", index, canonical, body)
		}
	}
}

func TestContextTrustCanOnlyIncreaseEligibility(t *testing.T) {
	t.Parallel()
	role := testStringValue(t, "operator")
	rule := testRule("trusted-allow", DecisionAllow, Predicate{
		Source: SourceContext, Pointer: "/role", Op: OperatorEqual, Value: &role,
	})
	provenances := []Provenance{
		ProvenanceAgentSupplied, ProvenanceAdapterAsserted,
		ProvenanceHostObserved, ProvenanceOperatorBound,
	}
	previousStrength := DecisionBlock.Strength()
	for _, provenance := range provenances {
		evaluator, input := testActionEvaluator(t, []Rule{rule}, Defaults{}, testExternalEffect())
		input.Request.Context = []ContextValue{{
			Name: "role", Value: role, Provenance: provenance, Available: true,
		}}
		refreshTestIdentities(evaluator, &input)
		result := evaluator.Evaluate(input)
		if result.Decision.Strength() > previousStrength {
			t.Fatalf("raising provenance made enforcement stronger: %s -> %s", provenance, result.Decision)
		}
		previousStrength = result.Decision.Strength()
		if provenance.Rank() < ProvenanceHostObserved.Rank() && result.Decision != DecisionBlock {
			t.Fatalf("untrusted provenance %s relaxed decision to %s", provenance, result.Decision)
		}
		if provenance.Rank() >= ProvenanceHostObserved.Rank() && result.Decision != DecisionAllow {
			t.Fatalf("trusted provenance %s did not satisfy exact allow", provenance)
		}
	}
}

func TestEvaluationReplayIsByteDeterministic(t *testing.T) {
	t.Parallel()
	const rawTarget = "seeded-raw-target-value"
	value := testStringValue(t, rawTarget)
	rules := []Rule{
		testRule("block-target", DecisionBlock, Predicate{
			Source: SourceArguments, Pointer: "/target", Op: OperatorEqual, Value: &value,
		}),
		{ID: "warn-all", Decision: DecisionWarn, SourceIdentity: ".reconc.yml"},
	}
	evaluator, input := testActionEvaluator(t, rules, Defaults{}, testExternalEffect())
	arguments := mustTestValue(t, `{"target":"`+rawTarget+`"}`)
	input.Request.Arguments = &arguments
	refreshTestIdentities(evaluator, &input)
	first, err := json.Marshal(evaluator.Evaluate(input))
	if err != nil {
		t.Fatal(err)
	}
	for replay := 0; replay < 100; replay++ {
		next, err := json.Marshal(evaluator.Evaluate(input))
		if err != nil {
			t.Fatal(err)
		}
		if string(next) != string(first) {
			t.Fatalf("replay %d changed deterministic result", replay)
		}
	}
	if strings.Contains(string(first), rawTarget) {
		t.Fatal("deterministic replay exposed the raw matched value")
	}
}
