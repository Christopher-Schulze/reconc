package action

import "testing"

const (
	testSourceDigest       = "1111111111111111111111111111111111111111111111111111111111111111"
	testPolicyDigest       = "2222222222222222222222222222222222222222222222222222222222222222"
	testLockDigest         = "3333333333333333333333333333333333333333333333333333333333333333"
	testToolContractDigest = "sha256:4444444444444444444444444444444444444444444444444444444444444444"
	testExecutableDigest   = "sha256:7777777777777777777777777777777777777777777777777777777777777777"
	testServerFingerprint  = "hmac-sha256:v1:key1:5555555555555555555555555555555555555555555555555555555555555555"
	testRepositoryIdentity = "hmac-sha256:v1:key2:6666666666666666666666666666666666666666666666666666666666666666"
)

func testActionEvaluator(
	t testing.TB,
	rules []Rule,
	defaults Defaults,
	effect Effect,
) (*Evaluator, EvaluationInput) {
	t.Helper()
	if defaults == (Defaults{}) {
		defaults = FrozenDefaults()
	}
	tool := Tool{
		ID: "database-write", Transport: TransportMCPStdio,
		ServerLabel: "database", ServerFingerprint: testServerFingerprint,
		Tool: "execute", Effect: effect,
		Origin: OriginActions, SourceIdentity: ".reconc.yml",
	}
	compiled, err := CompilePlan(Plan{Tools: []Tool{tool}, Rules: rules, Defaults: defaults})
	if err != nil {
		t.Fatalf("CompilePlan: %v", err)
	}
	evaluator, err := NewEvaluator(compiled)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	arguments := mustTestValue(t, `{"path":"safe/data","target":"staging","amount":1,"ip":"10.2.3.4","url":"https://api.example.test/v1/items","tags":["a","b"]}`)
	input := EvaluationInput{
		Request: Request{
			FormatVersion: RequestFormatVersion,
			CallID:        "act_aaaaaaaaaaaaaaaaaaaaaaaaaa",
			Transport:     TransportMCPStdio, ServerLabel: "database",
			ServerFingerprint: testServerFingerprint,
			Tool:              "execute", ToolContractDigest: testToolContractDigest,
			Phase: PhasePreCall, RepositoryIdentity: testRepositoryIdentity,
			PolicyDigest: testPolicyDigest, LockDigest: testLockDigest,
			AuthorityMode: AuthorityOperatorPinned, Arguments: &arguments,
			Context: []ContextValue{}, Completeness: CompleteEvidence(),
			Deadline: DeadlineReady, StateVersion: "state-v1",
		},
		SourceIdentity: testSourceDigest, ContextIdentity: "context-v1", Principal: "operator",
		ExecutableDigest: testExecutableDigest,
		CredentialLabels: []string{"database-writer"},
		Budget: BudgetSnapshot{
			StateVersion: "state-v1", Identity: "absent",
			ReservationIdentity: "absent", Complete: true, Candidates: []BudgetCandidate{},
		},
		Approval:  ApprovalSnapshot{Status: ApprovalNone, Identity: "approval-none"},
		Taint:     TaintSnapshot{Status: TaintClean, Identity: "taint-clean"},
		Lifecycle: LifecycleActive, CachePolicyVersion: CacheIdentityVersion,
	}
	input.ResampledIdentities = evaluator.expectedIdentities(input)
	return evaluator, input
}

func testExternalEffect() Effect {
	return Effect{Kind: EffectExternal}
}

func testRule(
	id string,
	decision Decision,
	predicate Predicate,
) Rule {
	condition := Condition{Predicate: &predicate}
	return Rule{
		ID: id, Selector: Selector{Phases: []Phase{PhasePreCall}},
		When: &condition, Decision: decision, SourceIdentity: ".reconc.yml",
	}
}

func testStringValue(t testing.TB, value string) Value {
	t.Helper()
	parsed, err := String(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func mustTestValue(t testing.TB, raw string) Value {
	t.Helper()
	value, err := ParseJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testRawRequest(arguments []byte) RawRequest {
	return RawRequest{
		FormatVersion: RequestFormatVersion,
		CallID:        "act_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Transport:     TransportMCPStdio, ServerLabel: "database",
		ServerFingerprint: testServerFingerprint,
		Tool:              "execute", ToolContractDigest: testToolContractDigest,
		Phase: PhasePreCall, RepositoryIdentity: testRepositoryIdentity,
		PolicyDigest: testPolicyDigest, LockDigest: testLockDigest,
		AuthorityMode: AuthorityOperatorPinned, Arguments: arguments,
		Context: []RawContextValue{}, Completeness: CompleteEvidence(),
		Deadline: DeadlineReady, StateVersion: "state-v1",
	}
}

func refreshTestIdentities(evaluator *Evaluator, input *EvaluationInput) {
	input.ResampledIdentities = evaluator.expectedIdentities(*input)
}
