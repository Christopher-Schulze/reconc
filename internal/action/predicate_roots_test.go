package action

import "testing"

func TestPredicateRootsReuseContextRootWithoutLeakingRequestMutation(t *testing.T) {
	t.Parallel()
	role := testStringValue(t, "operator")
	request := Request{Context: []ContextValue{{
		Name: "role", Value: role, Provenance: ProvenanceHostObserved, Available: true,
	}}}
	exists := compileTestPredicate(t, Predicate{Source: SourceContext, Op: OperatorExists})
	roots := predicateRoots{}
	first := evaluatePredicateWithRoots(exists, request, DecisionBlock, &roots)
	second := evaluatePredicateWithRoots(exists, request, DecisionBlock, &roots)
	if roots.contextBuilds != 1 || !roots.contextReady {
		t.Fatalf("context root builds = %d, ready = %t", roots.contextBuilds, roots.contextReady)
	}
	if first.state != ConditionTrue || second.state != ConditionTrue ||
		first.actual != ProvenanceHostObserved || second.actual != ProvenanceHostObserved {
		t.Fatalf("root predicate results = %#v, %#v", first, second)
	}

	request.Context[0] = ContextValue{
		Name: "role", Value: testStringValue(t, "mutated"),
		Provenance: ProvenanceAgentSupplied, Available: true,
	}
	third := evaluatePredicateWithRoots(exists, request, DecisionBlock, &roots)
	if third.state != first.state || third.actual != first.actual || third.summary != first.summary {
		t.Fatalf("cached root changed after request mutation: before = %#v, after = %#v", first, third)
	}

	uncached := evaluatePredicate(exists, Request{Context: []ContextValue{{
		Name: "role", Value: role, Provenance: ProvenanceHostObserved, Available: true,
	}}}, DecisionBlock)
	if uncached != first {
		t.Fatalf("cached and uncached results differ: cached = %#v, uncached = %#v", first, uncached)
	}
}

func TestPredicateRootsPreserveNonRootPointerResolution(t *testing.T) {
	t.Parallel()
	arguments := mustTestValue(t, `{"name":"alpha"}`)
	result := mustTestValue(t, `{"ok":true}`)
	progress := mustTestValue(t, `{"step":1}`)
	request := Request{
		Arguments: &arguments, Result: &result, Progress: &progress,
		Context: []ContextValue{{Name: "role", Value: testStringValue(t, "operator"), Provenance: ProvenanceHostObserved, Available: true}},
	}
	tests := []struct {
		name       string
		source     ValueSource
		pointer    string
		operator   Operator
		value      *Value
		provenance Provenance
	}{
		{name: "arguments", source: SourceArguments, pointer: "/name", operator: OperatorExists, provenance: ProvenanceAgentSupplied},
		{name: "result", source: SourceResult, pointer: "/ok", operator: OperatorEqual, value: func() *Value { value := Boolean(true); return &value }(), provenance: ProvenanceAgentSupplied},
		{name: "progress", source: SourceProgress, pointer: "/step", operator: OperatorExists, provenance: ProvenanceAgentSupplied},
		{name: "context member", source: SourceContext, pointer: "/role", operator: OperatorExists, provenance: ProvenanceHostObserved},
		{name: "context root", source: SourceContext, pointer: "", operator: OperatorExists, provenance: ProvenanceHostObserved},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			phase := PhasePreCall
			switch test.source {
			case SourceResult:
				phase = PhasePostResult
			case SourceProgress:
				phase = PhaseProgress
			}
			condition := Condition{Predicate: &Predicate{
				Source: test.source, Pointer: test.pointer, Op: test.operator, Value: test.value,
			}}
			plan, err := CompilePlan(Plan{Rules: []Rule{{
				ID: "predicate", Selector: Selector{Phases: []Phase{phase}},
				When: &condition, Decision: DecisionBlock, SourceIdentity: ".reconc.yml",
			}}})
			if err != nil {
				t.Fatal(err)
			}
			predicate := plan.Rules()[0].Condition.Predicate
			want := evaluatePredicate(predicate, request, DecisionBlock)
			roots := predicateRoots{}
			got := evaluatePredicateWithRoots(predicate, request, DecisionBlock, &roots)
			if got != want || got.actual != test.provenance {
				t.Fatalf("rooted result = %#v, uncached = %#v, want provenance %s", got, want, test.provenance)
			}
		})
	}
}

func TestPredicateRootsMemoizeOnlyRequiredRootSummaries(t *testing.T) {
	t.Parallel()
	arguments := mustTestValue(t, `{"name":"alpha","items":[1,2,3]}`)
	request := Request{Arguments: &arguments}
	exists := compileTestPredicate(t, Predicate{Source: SourceArguments, Op: OperatorExists})

	roots := predicateRoots{}
	first := evaluatePredicateWithRoots(exists, request, DecisionBlock, &roots)
	second := evaluatePredicateWithRoots(exists, request, DecisionBlock, &roots)
	wantSize, err := arguments.CanonicalJSONSize()
	if err != nil {
		t.Fatal(err)
	}
	if !roots.argumentSize.ready || !roots.argumentSize.valid ||
		first.summary.ByteLength != wantSize || second.summary != first.summary {
		t.Fatalf("memoized root summaries = %#v, %#v; memo = %#v", first.summary, second.summary, roots.argumentSize)
	}

	logicalRoots := predicateRoots{}
	condition := &CompiledCondition{Kind: ConditionAll, Children: []*CompiledCondition{
		{Kind: ConditionPredicate, Predicate: exists},
		{Kind: ConditionPredicate, Predicate: exists},
	}}
	result := evaluateConditionTreeWithRoots(condition, request, DecisionBlock, 1, &logicalRoots)
	if result.state != ConditionTrue || result.summary != (OperandSummary{}) || logicalRoots.argumentSize.ready {
		t.Fatalf("unused logical summary = %#v; memo = %#v", result, logicalRoots.argumentSize)
	}
}
