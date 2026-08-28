package action

import (
	"strings"
	"testing"
)

const budgetTestKeyID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func keyedBudgetTestIdentity(fill string) string {
	return "hmac-sha256:v1:" + budgetTestKeyID + ":" + strings.Repeat(fill, 64)
}

func testBudgetEvaluator(t testing.TB) (*Evaluator, EvaluationInput) {
	t.Helper()
	serverIdentity := keyedBudgetTestIdentity("1")
	repositoryIdentity := keyedBudgetTestIdentity("2")
	tool := Tool{
		ID: "database-write", Transport: TransportMCPStdio,
		ServerLabel: "database", ServerFingerprint: serverIdentity,
		Tool: "execute", Effect: Effect{Kind: EffectExternal},
		Origin: OriginActions, SourceIdentity: ".reconc.yml",
	}
	budget := Budget{
		ID: "calls", Selector: Selector{ToolIDs: []string{"database-write"}},
		Limits: BudgetLimits{CallCount: 2}, Reset: BudgetResetNever,
		OnExhaustion: DecisionBlock, SourceIdentity: ".reconc.yml",
	}
	compiled, err := CompilePlan(Plan{Tools: []Tool{tool}, Budgets: []Budget{budget}})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewEvaluator(compiled)
	if err != nil {
		t.Fatal(err)
	}
	arguments := mustTestValue(t, `{"amount":1}`)
	stateVersion := keyedBudgetTestIdentity("3")
	input := EvaluationInput{
		Request: Request{
			FormatVersion: RequestFormatVersion, CallID: "act_aaaaaaaaaaaaaaaaaaaaaaaaaa",
			Transport: TransportMCPStdio, ServerLabel: "database",
			ServerFingerprint: serverIdentity, Tool: "execute",
			ToolContractDigest: testToolContractDigest, Phase: PhasePreCall,
			RepositoryIdentity: repositoryIdentity, PolicyDigest: testPolicyDigest,
			LockDigest: testLockDigest, AuthorityMode: AuthorityOperatorPinned,
			Arguments: &arguments, Context: []ContextValue{}, Completeness: CompleteEvidence(),
			Deadline: DeadlineReady, StateVersion: stateVersion,
		},
		SourceIdentity: testSourceDigest, ContextIdentity: keyedBudgetTestIdentity("4"),
		ExecutableDigest: testExecutableDigest, Principal: "operator",
		CredentialLabels: []string{"database-writer"},
		Approval:         ApprovalSnapshot{Status: ApprovalNone, Identity: "approval-none"},
		Taint:            TaintSnapshot{Status: TaintClean, Identity: "taint-clean"},
		Lifecycle:        LifecycleActive, CachePolicyVersion: CacheIdentityVersion,
	}
	required, err := RequiredBudgetUsage(budget.Limits, tool, input.Request)
	if err != nil {
		t.Fatal(err)
	}
	input.Budget = BudgetSnapshot{
		StateVersion: stateVersion, Identity: keyedBudgetTestIdentity("5"),
		ReservationIdentity: keyedBudgetTestIdentity("6"), Complete: true,
		Candidates: []BudgetCandidate{{
			BudgetID: budget.ID, ScopeIdentity: keyedBudgetTestIdentity("7"),
			LineageIdentity: keyedBudgetTestIdentity("8"),
			Scope: BudgetScope{
				RepositoryIdentity: repositoryIdentity, Principal: input.Principal,
				CredentialLabels: []string{"database-writer"},
				ServerLabel:      "database", ServerIdentity: serverIdentity,
				ToolID: "database-write", RunIdentity: "absent",
				SessionIdentity: "absent", WindowIdentity: "absent",
			},
			Reset: BudgetResetNever, Limits: budget.Limits,
			Reserved: required, Required: required,
			ReservationApplied: true, Available: true,
			Generation: BudgetGeneration{
				PolicyDigest: testPolicyDigest, ExecutableDigest: testExecutableDigest,
				ToolContractDigest: testToolContractDigest, KeyID: budgetTestKeyID,
			},
		}},
	}
	input.ResampledIdentities = evaluator.expectedIdentities(input)
	return evaluator, input
}

func cloneBudgetEvaluationInput(input EvaluationInput) EvaluationInput {
	out := input
	out.CredentialLabels = append([]string(nil), input.CredentialLabels...)
	out.Budget = cloneBudgetSnapshot(input.Budget)
	out.ResampledIdentities.CredentialLabels = append(
		[]string(nil), input.ResampledIdentities.CredentialLabels...,
	)
	return out
}

func TestBudgetEvaluatorAllowsReservedCapacityAndBlocksExhaustion(t *testing.T) {
	t.Parallel()
	evaluator, available := testBudgetEvaluator(t)
	result := evaluator.Evaluate(available)
	if result.Failure != nil || result.Decision != DecisionAllow || result.Reason != ReasonDeclaredTool ||
		len(result.BudgetCandidates) != 1 || !result.BudgetCandidates[0].Available ||
		!result.Cache.Eligible {
		t.Fatalf("available budget result = %#v", result)
	}

	exhausted := cloneBudgetEvaluationInput(available)
	candidate := &exhausted.Budget.Candidates[0]
	candidate.Consumed.CallCount = candidate.Limits.CallCount
	candidate.Reserved = BudgetUsage{}
	candidate.ReservationApplied = false
	candidate.Available = false
	candidate.Reason = ReasonBudgetExhausted
	exhausted.Budget.ReservationIdentity = "absent"
	exhausted.ResampledIdentities = evaluator.expectedIdentities(exhausted)
	result = evaluator.Evaluate(exhausted)
	if result.Failure != nil || result.Decision != DecisionBlock || result.Reason != ReasonBudgetExhausted ||
		len(result.Candidates) != 2 || result.PhaseOutcome != OutcomeDispatchBlocked {
		t.Fatalf("exhausted budget result = %#v", result)
	}
}

func TestBudgetEvaluatorRejectsEveryMalformedSnapshotBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*EvaluationInput)
		code   ReasonCode
	}{
		{
			name: "incomplete", code: ReasonStateUnavailable,
			mutate: func(input *EvaluationInput) { input.Budget.Complete = false },
		},
		{
			name: "unkeyed snapshot", code: ReasonStateCorrupt,
			mutate: func(input *EvaluationInput) { input.Budget.Identity = "budget-v1" },
		},
		{
			name: "state key mismatch", code: ReasonStateCorrupt,
			mutate: func(input *EvaluationInput) {
				value := "hmac-sha256:v1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:" + strings.Repeat("1", 64)
				input.Request.StateVersion, input.Budget.StateVersion = value, value
			},
		},
		{
			name: "reservation key mismatch", code: ReasonStateCorrupt,
			mutate: func(input *EvaluationInput) {
				input.Budget.ReservationIdentity = "hmac-sha256:v1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:" + strings.Repeat("2", 64)
			},
		},
		{
			name: "candidate key mismatch", code: ReasonStateCorrupt,
			mutate: func(input *EvaluationInput) {
				input.Budget.Candidates[0].ScopeIdentity = "hmac-sha256:v1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:" + strings.Repeat("3", 64)
			},
		},
		{
			name: "generation key malformed", code: ReasonStateCorrupt,
			mutate: func(input *EvaluationInput) { input.Budget.Candidates[0].Generation.KeyID = "key" },
		},
		{
			name: "limit drift", code: ReasonStateCorrupt,
			mutate: func(input *EvaluationInput) { input.Budget.Candidates[0].Limits.CallCount++ },
		},
		{
			name: "required drift", code: ReasonStateCorrupt,
			mutate: func(input *EvaluationInput) { input.Budget.Candidates[0].Required.CallCount = 0 },
		},
		{
			name: "availability drift", code: ReasonStateCorrupt,
			mutate: func(input *EvaluationInput) { input.Budget.Candidates[0].Available = false },
		},
		{
			name: "partial reservation", code: ReasonStateCorrupt,
			mutate: func(input *EvaluationInput) { input.Budget.Candidates[0].ReservationApplied = false },
		},
		{
			name: "missing candidate", code: ReasonStateCorrupt,
			mutate: func(input *EvaluationInput) { input.Budget.Candidates = []BudgetCandidate{} },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			evaluator, baseline := testBudgetEvaluator(t)
			input := cloneBudgetEvaluationInput(baseline)
			test.mutate(&input)
			input.ResampledIdentities = evaluator.expectedIdentities(input)
			result := evaluator.Evaluate(input)
			if result.Failure == nil || result.Failure.Code != test.code || result.Decision != DecisionBlock {
				t.Fatalf("malformed result = %#v, want %s", result, test.code)
			}
		})
	}
}

func TestDecisionCacheBindsEveryBudgetStateComponent(t *testing.T) {
	t.Parallel()
	evaluator, baseline := testBudgetEvaluator(t)
	result := evaluator.Evaluate(baseline)
	cache := NewDecisionCache()
	if !cache.Store(evaluator, baseline, result) {
		t.Fatalf("budget result was not cacheable: %#v", result.Cache)
	}
	tests := []struct {
		name   string
		mutate func(*EvaluationInput)
	}{
		{name: "snapshot identity", mutate: func(input *EvaluationInput) { input.Budget.Identity = keyedBudgetTestIdentity("9") }},
		{name: "reservation identity", mutate: func(input *EvaluationInput) { input.Budget.ReservationIdentity = keyedBudgetTestIdentity("0") }},
		{name: "scope identity", mutate: func(input *EvaluationInput) { input.Budget.Candidates[0].ScopeIdentity = keyedBudgetTestIdentity("9") }},
		{name: "lineage identity", mutate: func(input *EvaluationInput) {
			input.Budget.Candidates[0].LineageIdentity = keyedBudgetTestIdentity("9")
		}},
		{name: "consumed", mutate: func(input *EvaluationInput) { input.Budget.Candidates[0].Consumed.CallCount = 1 }},
		{name: "reserved", mutate: func(input *EvaluationInput) { input.Budget.Candidates[0].Reserved.CallCount = 2 }},
		{name: "required", mutate: func(input *EvaluationInput) { input.Budget.Candidates[0].Required.CallCount = 0 }},
		{name: "limits", mutate: func(input *EvaluationInput) { input.Budget.Candidates[0].Limits.CallCount = 3 }},
		{name: "scope", mutate: func(input *EvaluationInput) { input.Budget.Candidates[0].Scope.Principal = "reviewer" }},
		{name: "generation", mutate: func(input *EvaluationInput) { input.Budget.Candidates[0].Generation.PolicyDigest = testLockDigest }},
		{name: "reservation applied", mutate: func(input *EvaluationInput) { input.Budget.Candidates[0].ReservationApplied = false }},
		{name: "availability", mutate: func(input *EvaluationInput) { input.Budget.Candidates[0].Available = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := cloneBudgetEvaluationInput(baseline)
			test.mutate(&input)
			input.ResampledIdentities = evaluator.expectedIdentities(input)
			if _, hit, _ := cache.Lookup(evaluator, input); hit {
				t.Fatal("cache reused a decision after budget-state mutation")
			}
		})
	}
}

func TestCanonicalArgumentBytesForBudgetsUsesCanonicalSizeOnce(t *testing.T) {
	t.Parallel()
	arguments := mustTestValue(t, `{"unicode":"ä","number":1.0,"nested":{"ok":[true,false]}}`)
	request := Request{Arguments: &arguments}
	tool := Tool{}
	budgets := []Budget{
		{Limits: BudgetLimits{CallCount: 1}},
		{Limits: BudgetLimits{ArgumentBytes: 1}},
		{Limits: BudgetLimits{ArgumentBytes: 4096, ResultBytes: 1}},
	}
	bytes, known, err := canonicalArgumentBytesForBudgets(budgets, request)
	if err != nil || !known {
		t.Fatalf("canonical argument size = %d, known %t, %v", bytes, known, err)
	}
	body, err := arguments.MarshalJSON()
	if err != nil || bytes != uint64(len(body)) {
		t.Fatalf("canonical argument bytes = %d, want %d (%s), err = %v", bytes, len(body), body, err)
	}
	first, err := expectedBudgetUsageWithArgumentBytes(
		budgets[1].Limits, tool, request, bytes, known, nil,
	)
	if err != nil || first.ArgumentBytes != bytes {
		t.Fatalf("first shared usage = %#v, %v", first, err)
	}
	second, err := expectedBudgetUsageWithArgumentBytes(
		budgets[2].Limits, tool, request, bytes, known, nil,
	)
	if err != nil || second.ArgumentBytes != bytes {
		t.Fatalf("second shared usage = %#v, %v", second, err)
	}
	withoutArgumentLimit, withoutArgumentLimitKnown, err := canonicalArgumentBytesForBudgets(
		[]Budget{{Limits: BudgetLimits{CallCount: 1}}}, request,
	)
	if err != nil || withoutArgumentLimitKnown || withoutArgumentLimit != 0 {
		t.Fatalf("unneeded argument sizing = %d, known %t, %v", withoutArgumentLimit, withoutArgumentLimitKnown, err)
	}
}

func TestCanonicalArgumentBytesForBudgetsPropagatesSizeFailure(t *testing.T) {
	t.Parallel()
	request := Request{Arguments: &Value{kind: ValueKind("corrupt")}}
	_, _, err := canonicalArgumentBytesForBudgets(
		[]Budget{{Limits: BudgetLimits{ArgumentBytes: 1}}}, request,
	)
	if err == nil {
		t.Fatal("corrupt canonical argument value was accepted")
	}
}

func TestBudgetArgumentSizeMatchesCanonicalEncodingAcrossValueKinds(t *testing.T) {
	deep := strings.Repeat("[", MaxJSONDepth) + "null" + strings.Repeat("]", MaxJSONDepth)
	for _, raw := range []string{
		`null`, `true`, `-12345e-9`, `"controls\n<& and unicode ä"`, `[]`, `[null,true,1,"x"]`,
		`{}`, `{"z":1.0,"a":{"nested":[false]}}`, deep,
	} {
		value := mustTestValue(t, raw)
		request := Request{Arguments: &value}
		got, err := canonicalArgumentBytes(request)
		body, marshalErr := value.MarshalJSON()
		if err != nil || marshalErr != nil || got != uint64(len(body)) {
			t.Fatalf("canonical budget size for %s = %d, encoded %d, errors %v/%v", raw, got, len(body), err, marshalErr)
		}
	}
}

func TestBudgetArgumentSizePreservesCanonicalErrors(t *testing.T) {
	if _, err := canonicalArgumentBytes(Request{}); err == nil || err.Error() != "budgeted pre-call arguments are absent" {
		t.Fatalf("nil arguments error = %v", err)
	}
	value := Null()
	for range MaxJSONDepth + 1 {
		value = Value{kind: ValueArray, array: []Value{value}}
	}
	for _, invalid := range []Value{{kind: ValueKind("corrupt")}, value} {
		_, wantErr := invalid.MarshalJSON()
		_, gotErr := canonicalArgumentBytes(Request{Arguments: &invalid})
		if wantErr == nil || gotErr == nil || gotErr.Error() != wantErr.Error() {
			t.Fatalf("canonical error mismatch: budget=%v marshal=%v", gotErr, wantErr)
		}
	}
	if _, err := addJSONSize(int(^uint(0)>>1), 1); err == nil {
		t.Fatal("canonical size overflow was accepted")
	}
}

func TestCanonicalArgumentBytesForBudgetsDoesNotAllocate(t *testing.T) {
	arguments := mustTestValue(t, `{"unicode":"ä","number":1.0,"nested":{"ok":[true,false]}}`)
	request := Request{Arguments: &arguments}
	budgets := []Budget{{Limits: BudgetLimits{ArgumentBytes: 4096}}}
	allocations := testing.AllocsPerRun(1_000, func() {
		bytes, known, err := canonicalArgumentBytesForBudgets(budgets, request)
		if err != nil || !known || bytes == 0 {
			panic("canonical argument sizing failed")
		}
	})
	if allocations != 0 {
		t.Fatalf("canonical budget argument sizing allocated %.2f objects per run", allocations)
	}
}

func BenchmarkBudgetArgumentSizeShared(b *testing.B) {
	arguments := mustTestValue(b, `{"unicode":"ä","number":1.0,"nested":{"ok":[true,false]}}`)
	request := Request{Arguments: &arguments}
	budgets := make([]Budget, 64)
	for index := range budgets {
		budgets[index].Limits.ArgumentBytes = 4096
	}
	tool := Tool{}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		bytes, known, err := canonicalArgumentBytesForBudgets(budgets, request)
		if err != nil || !known {
			b.Fatal(err)
		}
		for _, budget := range budgets {
			if _, err := expectedBudgetUsageWithArgumentBytes(budget.Limits, tool, request, bytes, known, nil); err != nil {
				b.Fatal(err)
			}
		}
	}
}
