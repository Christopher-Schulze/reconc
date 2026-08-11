package impactlab

import (
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/runtime"
)

func TestActionLedgerAssertionUsesCompiledPolicyAndProducesExactDelta(t *testing.T) {
	currentRepo, current := makeLedgerImpactRepo(t, "required", "declaration_id", "arguments", "/target")
	_, candidate := makeLedgerImpactRepo(t, "best_effort", "keyed_name", "result", "/rows")
	currentRuntime, err := current.ActionRuntime()
	if err != nil {
		t.Fatal(err)
	}
	fixture := newActionFixture(
		"ledger-policy", CaseActionPre, `{"target":"staging"}`,
		actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
			action.CachePolicyNever, action.OutcomeDispatchEligible, ""),
	)
	fixture.Action.Expected.Ledger = &ActionLedgerAssertion{}
	fixture, err = BindCapturedActionExpectation(fixture, currentRuntime)
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := NewCorpus(currentRepo, []Case{fixture}, []EventClass{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := Compare(currentRepo, corpus, candidateFromEvaluator(t, candidate), current, candidate)
	if err != nil {
		t.Fatal(err)
	}
	comparison := report.Cases[0].Action
	if comparison == nil || !slicesEqualActionDelta(comparison.Deltas, []ActionDeltaKind{DeltaLedger}) ||
		report.Summary.ActionLedgerChanges != 1 || comparison.Current.Outcome.Ledger == nil ||
		comparison.Candidate.Outcome.Ledger == nil {
		t.Fatalf("ledger comparison = %#v; summary = %#v", comparison, report.Summary)
	}
	currentLedger := comparison.Current.Outcome.Ledger
	if currentLedger.Mode != action.LedgerRequired || !currentLedger.Required ||
		currentLedger.Event != actionledger.EventPreDecision ||
		currentLedger.ToolIdentity != action.LedgerDeclarationID ||
		len(currentLedger.SelectedFields) != 1 || currentLedger.SelectedFields[0].Pointer != "/target" {
		t.Fatalf("current ledger assertion = %#v", currentLedger)
	}
}

func TestActionLedgerAssertionMapsEveryActionPhase(t *testing.T) {
	policy := &action.LedgerPolicy{
		Mode: action.LedgerRequired, ToolIdentity: action.LedgerDeclarationID,
		SelectedFields: []action.LedgerField{},
	}
	for _, test := range []struct {
		phase action.Phase
		event actionledger.EventType
	}{
		{phase: action.PhasePreCall, event: actionledger.EventPreDecision},
		{phase: action.PhasePostResult, event: actionledger.EventResultInspection},
		{phase: action.PhaseProgress, event: actionledger.EventPreDecision},
		{phase: action.PhaseObservation, event: actionledger.EventPreDecision},
	} {
		assertion, err := LedgerAssertionForPhase(test.phase, policy)
		if err != nil || assertion.Event != test.event {
			t.Fatalf("LedgerAssertionForPhase(%s) = %#v, %v", test.phase, assertion, err)
		}
	}
}

func TestActionLedgerAssertionRejectsEveryContractMutation(t *testing.T) {
	valid, err := LedgerAssertionForPhase(action.PhasePreCall, &action.LedgerPolicy{
		Mode: action.LedgerRequired, ToolIdentity: action.LedgerDeclarationID,
		SelectedFields: []action.LedgerField{
			{Source: action.SourceArguments, Pointer: "/a"},
			{Source: action.SourceResult, Pointer: "/b"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*ActionLedgerAssertion)
	}{
		{name: "mode", mutate: func(value *ActionLedgerAssertion) { value.Mode = "sometimes" }},
		{name: "event", mutate: func(value *ActionLedgerAssertion) { value.Event = actionledger.EventFinalDelivery }},
		{name: "required", mutate: func(value *ActionLedgerAssertion) { value.Required = false }},
		{name: "tool identity", mutate: func(value *ActionLedgerAssertion) { value.ToolIdentity = "raw" }},
		{name: "null fields", mutate: func(value *ActionLedgerAssertion) { value.SelectedFields = nil }},
		{name: "wrong source", mutate: func(value *ActionLedgerAssertion) { value.SelectedFields[0].Source = action.SourceContext }},
		{name: "bad pointer", mutate: func(value *ActionLedgerAssertion) { value.SelectedFields[0].Pointer = "relative" }},
		{name: "unsorted", mutate: func(value *ActionLedgerAssertion) {
			value.SelectedFields[0], value.SelectedFields[1] = value.SelectedFields[1], value.SelectedFields[0]
		}},
		{name: "duplicate", mutate: func(value *ActionLedgerAssertion) {
			value.SelectedFields[1] = value.SelectedFields[0]
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := cloneActionLedgerAssertion(valid)
			test.mutate(mutated)
			if err := validateActionLedgerAssertion(action.PhasePreCall, mutated); err == nil {
				t.Fatal("ledger assertion mutation passed validation")
			}
		})
	}
	off, err := LedgerAssertionForPhase(action.PhasePostResult, &action.LedgerPolicy{
		Mode: action.LedgerOff, ToolIdentity: action.LedgerDeclarationID,
		SelectedFields: []action.LedgerField{},
	})
	if err != nil || off.Event != "" || off.Required {
		t.Fatalf("off ledger assertion = %#v, %v", off, err)
	}
}

func makeLedgerImpactRepo(
	t *testing.T,
	mode string,
	toolIdentity string,
	source string,
	pointer string,
) (string, *runtime.CompiledPolicyEvaluator) {
	t.Helper()
	repo := t.TempDir()
	body := `default_mode: warn
actions:
  tools:
    - id: database-write
      transport: mcp_stdio
      server_label: database
      server_fingerprint: ` + fixtureServerIdentity + `
      tool: execute
      effect:
        kind: external
  rules: []
  ledger:
    mode: ` + mode + `
    tool_identity: ` + toolIdentity + `
    selected_fields:
      - source: ` + source + `
        pointer: ` + pointer + `
rules: []
`
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "ledger-impact-test"); err != nil {
		t.Fatalf("compile ledger action repo:\n%s\n%v", body, err)
	}
	evaluator, _, err := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(repo)
	if err != nil {
		t.Fatal(err)
	}
	return repo, evaluator
}
