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
