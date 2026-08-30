package action

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestEvaluatorDefaultsAndDecisionPrecedence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		rules    []Rule
		decision Decision
		reason   ReasonCode
		matched  []string
	}{
		{name: "declared default", decision: DecisionAllow, reason: ReasonDeclaredTool, matched: []string{}},
		{
			name: "warn over allow",
			rules: []Rule{
				{ID: "allow", Decision: DecisionAllow, SourceIdentity: ".reconc.yml"},
				{ID: "warn", Decision: DecisionWarn, SourceIdentity: ".reconc.yml"},
			},
			decision: DecisionWarn, reason: ReasonRuleMatched, matched: []string{"warn", "allow"},
		},
		{
			name: "approval over warn",
			rules: []Rule{
				{ID: "warn", Decision: DecisionWarn, SourceIdentity: ".reconc.yml"},
				{ID: "approval", Decision: DecisionRequireApproval, SourceIdentity: ".reconc.yml"},
			},
			decision: DecisionRequireApproval, reason: ReasonApprovalRequired, matched: []string{"approval", "warn"},
		},
		{
			name: "block over approval and stable tie",
			rules: []Rule{
				{ID: "z-block", Decision: DecisionBlock, SourceIdentity: ".reconc.yml"},
				{ID: "approval", Decision: DecisionRequireApproval, SourceIdentity: ".reconc.yml"},
				{ID: "a-block", Decision: DecisionBlock, SourceIdentity: ".reconc.yml"},
			},
			decision: DecisionBlock, reason: ReasonRuleMatched,
			matched: []string{"a-block", "z-block", "approval"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			evaluator, input := testActionEvaluator(t, test.rules, Defaults{}, testExternalEffect())
			result := evaluator.Evaluate(input)
			if result.Decision != test.decision || result.Reason != test.reason || result.Failure != nil {
				t.Fatalf("result = %#v", result)
			}
			if strings.Join(result.MatchedRuleIDs, ",") != strings.Join(test.matched, ",") {
				t.Fatalf("matched rules = %#v, want %#v", result.MatchedRuleIDs, test.matched)
			}
			if result.PhaseOutcome != phaseOutcome(PhasePreCall, test.decision) {
				t.Fatalf("phase outcome = %s", result.PhaseOutcome)
			}
			if test.decision == DecisionRequireApproval && result.RequiredApprovalIdentity == "" {
				t.Fatal("approval decision has no bound requirement identity")
			}
		})
	}
}

func TestEvaluatorUnmatchedTransportDefaults(t *testing.T) {
	t.Parallel()
	evaluator, input := testActionEvaluator(t, nil, Defaults{}, testExternalEffect())
	input.Request.Tool = "unknown"
	refreshTestIdentities(evaluator, &input)
	result := evaluator.Evaluate(input)
	if result.Decision != DecisionBlock || result.Reason != ReasonToolUnclassified || result.ToolID != "" {
		t.Fatalf("gateway unmatched result = %#v", result)
	}

	input.Request.Transport = TransportHostMCP
	input.Request.Platform = PlatformClaudeCode
	input.Request.ServerLabel = ""
	input.Request.ServerFingerprint = "sha256:7777777777777777777777777777777777777777777777777777777777777777"
	refreshTestIdentities(evaluator, &input)
	result = evaluator.Evaluate(input)
	if result.Decision != DecisionAllow || result.Reason != ReasonHostUnmatched {
		t.Fatalf("host unmatched result = %#v", result)
	}
}

func TestSelectorMatchesEveryExactDimension(t *testing.T) {
	t.Parallel()
	mcpRequest := Request{
		Transport: TransportMCPStdio, ServerLabel: "database",
		ServerFingerprint: testServerFingerprint, Tool: "execute",
		ToolContractDigest: testToolContractDigest, Phase: PhasePreCall,
	}
	mcpSelector := Selector{
		ToolIDs: []string{"database-write"}, Transports: []Transport{TransportMCPStdio},
		ServerLabels: []string{"database"}, ServerFingerprints: []string{testServerFingerprint},
		Tools: []string{"execute"}, ToolContractDigests: []string{testToolContractDigest},
		Phases: []Phase{PhasePreCall},
	}
	if !selectorMatches(mcpSelector, mcpRequest, "database-write") {
		t.Fatal("complete MCP selector did not match its exact request")
	}
	tests := []struct {
		name   string
		mutate func(*Selector)
	}{
		{name: "tool id", mutate: func(value *Selector) { value.ToolIDs = []string{"other"} }},
		{name: "transport", mutate: func(value *Selector) { value.Transports = []Transport{TransportHostMCP} }},
		{name: "server label", mutate: func(value *Selector) { value.ServerLabels = []string{"other"} }},
		{name: "server fingerprint", mutate: func(value *Selector) { value.ServerFingerprints = []string{"sha256:" + strings.Repeat("7", 64)} }},
		{name: "tool", mutate: func(value *Selector) { value.Tools = []string{"other"} }},
		{name: "tool contract", mutate: func(value *Selector) { value.ToolContractDigests = []string{"sha256:" + strings.Repeat("8", 64)} }},
		{name: "phase", mutate: func(value *Selector) { value.Phases = []Phase{PhasePostResult} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selector := mcpSelector
			test.mutate(&selector)
			if selectorMatches(selector, mcpRequest, "database-write") {
				t.Fatal("mismatched selector unexpectedly matched")
			}
		})
	}
	hostRequest := mcpRequest
	hostRequest.Transport = TransportHostMCP
	hostRequest.Platform = PlatformClaudeCode
	hostRequest.ServerLabel = ""
	hostRequest.ServerFingerprint = "sha256:" + strings.Repeat("9", 64)
	if !selectorMatches(Selector{Platforms: []Platform{PlatformClaudeCode}}, hostRequest, "") ||
		selectorMatches(Selector{Platforms: []Platform{PlatformCursor}}, hostRequest, "") {
		t.Fatal("platform selector did not preserve exact host identity")
	}
	if !selectorMatches(Selector{}, hostRequest, "") {
		t.Fatal("absent selector fields are not wildcards")
	}
}

func TestEvaluatorTrustMonotonicityAndLexicalPathRestriction(t *testing.T) {
	t.Parallel()
	role := testStringValue(t, "operator")
	pathConstraint := mustTestValue(t, `{"style":"repository","base":"safe","case_sensitive":true}`)
	tests := []struct {
		name       string
		predicate  Predicate
		value      Value
		provenance Provenance
		decision   Decision
		reason     ReasonCode
	}{
		{
			name:      "adapter role cannot allow",
			predicate: Predicate{Source: SourceContext, Pointer: "/role", Op: OperatorEqual, Value: &role},
			value:     role, provenance: ProvenanceAdapterAsserted,
			decision: DecisionBlock, reason: ReasonContextUntrusted,
		},
		{
			name:      "host role can preserve allow",
			predicate: Predicate{Source: SourceContext, Pointer: "/role", Op: OperatorEqual, Value: &role},
			value:     role, provenance: ProvenanceHostObserved,
			decision: DecisionAllow, reason: ReasonRuleMatched,
		},
		{
			name:      "agent lexical path cannot allow",
			predicate: Predicate{Source: SourceContext, Pointer: "/path", Op: OperatorPathWithin, Value: &pathConstraint},
			value:     testStringValue(t, "safe/file"), provenance: ProvenanceAgentSupplied,
			decision: DecisionBlock, reason: ReasonContextUntrusted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rule := testRule("allow-trusted", DecisionAllow, test.predicate)
			evaluator, input := testActionEvaluator(t, []Rule{rule}, Defaults{}, testExternalEffect())
			name := strings.TrimPrefix(test.predicate.Pointer, "/")
			input.Request.Context = []ContextValue{{
				Name: name, Value: test.value,
				Provenance: test.provenance, Available: true,
			}}
			refreshTestIdentities(evaluator, &input)
			result := evaluator.Evaluate(input)
			if result.Decision != test.decision || result.Reason != test.reason {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestEvaluatorThreeValuedConditionSemantics(t *testing.T) {
	t.Parallel()
	truePredicate := Condition{Predicate: &Predicate{
		Source: SourceArguments, Pointer: "/target", Op: OperatorExists,
	}}
	falsePredicate := Condition{Predicate: &Predicate{
		Source: SourceArguments, Pointer: "/missing", Op: OperatorExists,
	}}
	value := testStringValue(t, "x")
	indeterminatePredicate := Condition{Predicate: &Predicate{
		Source: SourceArguments, Pointer: "/missing", Op: OperatorEqual, Value: &value,
	}}
	tests := []struct {
		name         string
		condition    Condition
		want         ConditionState
		wantComplete bool
		wantReason   ReasonCode
		wantNodes    int
	}{
		{name: "all false masks indeterminate", condition: Condition{All: []Condition{falsePredicate, indeterminatePredicate}}, want: ConditionFalse, wantComplete: true, wantNodes: 3},
		{name: "all true indeterminate", condition: Condition{All: []Condition{truePredicate, indeterminatePredicate}}, want: ConditionIndeterminate, wantComplete: false, wantReason: ReasonConditionIndeterminate, wantNodes: 3},
		{name: "any true masks indeterminate", condition: Condition{Any: []Condition{truePredicate, indeterminatePredicate}}, want: ConditionTrue, wantComplete: true, wantNodes: 3},
		{name: "any false indeterminate", condition: Condition{Any: []Condition{falsePredicate, indeterminatePredicate}}, want: ConditionIndeterminate, wantComplete: false, wantReason: ReasonConditionIndeterminate, wantNodes: 3},
		{name: "not indeterminate", condition: Condition{Not: &indeterminatePredicate}, want: ConditionIndeterminate, wantComplete: false, wantReason: ReasonConditionIndeterminate, wantNodes: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			compiled, err := CompilePlan(Plan{Rules: []Rule{{
				ID: "condition", Selector: Selector{Phases: []Phase{PhasePreCall}},
				When: &test.condition, Decision: DecisionBlock, SourceIdentity: ".reconc.yml",
			}}})
			if err != nil {
				t.Fatal(err)
			}
			arguments := mustTestValue(t, `{"target":"present"}`)
			got := evaluateConditionTree(compiled.Rules()[0].Condition, Request{Arguments: &arguments}, DecisionBlock, 1)
			if got.state != test.want {
				t.Fatalf("state = %s, want %s", got.state, test.want)
			}
			if got.complete != test.wantComplete || got.reason != test.wantReason || got.nodes != test.wantNodes {
				t.Fatalf("metadata = complete:%t reason:%s nodes:%d, want complete:%t reason:%s nodes:%d", got.complete, got.reason, got.nodes, test.wantComplete, test.wantReason, test.wantNodes)
			}
		})
	}
}

func TestEvaluatorHonorsOnIndeterminateForMembershipTargetTypeMismatch(t *testing.T) {
	t.Parallel()
	operand := mustTestValue(t, `["1","2"]`)
	for _, decision := range []Decision{DecisionBlock, DecisionRequireApproval} {
		decision := decision
		t.Run(string(decision), func(t *testing.T) {
			t.Parallel()
			rule := testRule("typed-membership", DecisionBlock, Predicate{
				Source: SourceArguments, Pointer: "/amount", Op: OperatorIn, Value: &operand,
			})
			rule.OnIndeterminate = decision
			evaluator, input := testActionEvaluator(t, []Rule{rule}, Defaults{}, testExternalEffect())
			result := evaluator.Evaluate(input)
			if result.Decision != decision || result.Reason != ReasonConditionIndeterminate ||
				result.Failure != nil || result.Completeness.Complete() || len(result.Trace) != 1 {
				t.Fatalf("result = %#v", result)
			}
			trace := result.Trace[0]
			if trace.Condition != ConditionIndeterminate || trace.CandidateDecision != decision ||
				trace.Reason != ReasonConditionIndeterminate || trace.Completeness {
				t.Fatalf("trace = %#v", trace)
			}
			if decision == DecisionRequireApproval && result.RequiredApprovalIdentity == "" {
				t.Fatal("indeterminate approval decision has no bound requirement identity")
			}
		})
	}
}

func TestEvaluatorHonorsOnIndeterminateForGlobWorkLimit(t *testing.T) {
	t.Parallel()
	pattern := testStringValue(t, "**/missing")
	for _, decision := range []Decision{DecisionBlock, DecisionRequireApproval} {
		decision := decision
		t.Run(string(decision), func(t *testing.T) {
			t.Parallel()
			globPredicate := Predicate{
				Source: SourceArguments, Pointer: "/path", Op: OperatorGlob, Value: &pattern,
			}
			existsPredicate := Predicate{
				Source: SourceArguments, Pointer: "/path", Op: OperatorExists,
			}
			rule := testRule("bounded-glob", DecisionBlock, globPredicate)
			rule.When = &Condition{All: []Condition{
				{Predicate: &existsPredicate}, {Predicate: &globPredicate},
			}}
			rule.OnIndeterminate = decision
			evaluator, input := testActionEvaluator(t, []Rule{rule}, Defaults{}, testExternalEffect())
			limited := false
			for _, child := range evaluator.rules[0].Condition.Children {
				if child.Predicate != nil && child.Predicate.Glob != nil {
					child.Predicate.Glob.workLimit = 1
					limited = true
				}
			}
			if !limited {
				t.Fatal("compiled glob predicate is absent")
			}
			arguments := mustTestValue(t, `{"path":"a/b/c/present"}`)
			input.Request.Arguments = &arguments
			refreshTestIdentities(evaluator, &input)
			result := evaluator.Evaluate(input)
			if result.Decision != decision || result.Reason != ReasonLimitExceeded ||
				result.Failure != nil || result.Completeness.Complete() || len(result.Trace) != 1 {
				t.Fatalf("result = %#v", result)
			}
			trace := result.Trace[0]
			if trace.Condition != ConditionIndeterminate || trace.CandidateDecision != decision ||
				trace.Reason != ReasonLimitExceeded || trace.Completeness {
				t.Fatalf("trace = %#v", trace)
			}
		})
	}
}

func TestConditionEvaluationRejectsInvalidChild(t *testing.T) {
	invalid := &CompiledCondition{Kind: ConditionKind("invalid")}
	condition := &CompiledCondition{Kind: ConditionAll, Children: []*CompiledCondition{invalid}}
	got := evaluateConditionTree(condition, Request{}, DecisionBlock, 1)
	if got.state != ConditionIndeterminate || got.reason != ReasonInternalInvariant || got.complete || got.nodes != 2 {
		t.Fatalf("invalid child evaluation = %#v, want indeterminate incomplete two-node result", got)
	}
}

func TestEvaluatorFailureOutcomesAreFailClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Evaluator, *EvaluationInput)
		code   ReasonCode
	}{
		{name: "deadline", mutate: func(_ *Evaluator, input *EvaluationInput) { input.Request.Deadline = DeadlineExceeded }, code: ReasonDeadlineExceeded},
		{name: "cancelled", mutate: func(_ *Evaluator, input *EvaluationInput) { input.Lifecycle = LifecycleCancelled }, code: ReasonCancelled},
		{name: "shutdown", mutate: func(_ *Evaluator, input *EvaluationInput) { input.Lifecycle = LifecycleShutdown }, code: ReasonShutdown},
		{name: "stale plan", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.PlanIdentity = "sha256:" + strings.Repeat("6", 64)
		}, code: ReasonPolicyStale},
		{name: "stale source", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.SourceIdentity = strings.Repeat("6", 64)
		}, code: ReasonPolicyStale},
		{name: "stale policy", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.PolicyDigest = strings.Repeat("9", 64)
		}, code: ReasonPolicyStale},
		{name: "lock mismatch", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.LockDigest = strings.Repeat("8", 64)
		}, code: ReasonLockMismatch},
		{name: "tool contract stale", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.ToolContractDigest = "sha256:" + strings.Repeat("7", 64)
		}, code: ReasonToolContractStale},
		{name: "authority drift", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.AuthorityMode = AuthorityRepositoryManaged
		}, code: ReasonIdentityUnavailable},
		{name: "server label drift", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.ServerLabel = "other"
		}, code: ReasonIdentityUnavailable},
		{name: "server fingerprint drift", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.ServerFingerprint = "hmac-sha256:v1:key1:" + strings.Repeat("7", 64)
		}, code: ReasonIdentityUnavailable},
		{name: "repository identity drift", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.RepositoryIdentity = "hmac-sha256:v1:key2:" + strings.Repeat("7", 64)
		}, code: ReasonIdentityUnavailable},
		{name: "context identity drift", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.ContextIdentity = "context-v2"
		}, code: ReasonIdentityUnavailable},
		{name: "principal drift", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.Principal = "reviewer"
		}, code: ReasonIdentityUnavailable},
		{name: "credential drift", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.CredentialLabels = []string{"database-reader"}
		}, code: ReasonIdentityUnavailable},
		{name: "state drift", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.StateVersion = "state-v2"
		}, code: ReasonStateUnavailable},
		{name: "approval drift", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.ApprovalIdentity = "approval-other"
		}, code: ReasonStateUnavailable},
		{name: "repository effect drift", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.RepositoryEffectIdentity = "repository-effect-other"
		}, code: ReasonStateUnavailable},
		{name: "taint drift", mutate: func(_ *Evaluator, input *EvaluationInput) {
			input.ResampledIdentities.TaintIdentity = "taint-other"
		}, code: ReasonIdentityUnavailable},
		{
			name: "incomplete evidence",
			mutate: func(_ *Evaluator, input *EvaluationInput) {
				input.Request.Completeness.ContextComplete = false
				input.Request.Completeness.Missing = []MissingEvidence{{Field: EvidenceContext, Reason: ReasonContextUntrusted}}
			},
			code: ReasonContextUntrusted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			evaluator, input := testActionEvaluator(t, nil, Defaults{}, testExternalEffect())
			test.mutate(evaluator, &input)
			result := evaluator.Evaluate(input)
			if result.Decision != DecisionBlock || result.Reason != test.code ||
				result.Failure == nil || result.Failure.Code != test.code || result.Cache.Eligible {
				t.Fatalf("failure result = %#v", result)
			}
		})
	}
}

func TestNewEvaluatorRejectsMalformedCompiledPlan(t *testing.T) {
	t.Parallel()
	pattern := testStringValue(t, "prod-[0-9]+")
	tests := []struct {
		name   string
		rule   Rule
		mutate func(*CompiledPlan)
	}{
		{
			name: "unknown condition kind",
			rule: testRule("condition", DecisionBlock, Predicate{
				Source: SourceArguments, Pointer: "/target", Op: OperatorExists,
			}),
			mutate: func(compiled *CompiledPlan) {
				compiled.plan.Rules[0].When = &Condition{}
			},
		},
		{
			name: "missing immutable regex program",
			rule: testRule("condition", DecisionBlock, Predicate{
				Source: SourceArguments, Pointer: "/target", Op: OperatorRegex, Value: &pattern,
			}),
			mutate: func(compiled *CompiledPlan) {
				compiled.plan.Rules[0].When.Predicate.Value = func() *Value {
					value := testStringValue(t, "[")
					return &value
				}()
			},
		},
		{
			name: "agent argument changed to allow authority",
			rule: testRule("condition", DecisionBlock, Predicate{
				Source: SourceArguments, Pointer: "/target", Op: OperatorExists,
			}),
			mutate: func(compiled *CompiledPlan) {
				compiled.plan.Rules[0].Decision = DecisionAllow
			},
		},
		{
			name: "invalid tool index",
			rule: Rule{ID: "condition", Decision: DecisionWarn, SourceIdentity: ".reconc.yml"},
			mutate: func(compiled *CompiledPlan) {
				compiled.plan.Rules[0].ID = "INVALID"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := CompilePlan(Plan{Rules: []Rule{test.rule}})
			if err != nil {
				t.Fatalf("CompilePlan: %v", err)
			}
			test.mutate(compiled)
			if _, err := NewEvaluator(compiled); err == nil {
				t.Fatal("malformed compiled plan was accepted")
			}
		})
	}
}

func TestNilEvaluatorFailsClosed(t *testing.T) {
	t.Parallel()
	_, input := testActionEvaluator(t, nil, Defaults{}, testExternalEffect())
	var evaluator *Evaluator
	result := evaluator.Evaluate(input)
	if result.Decision != DecisionBlock || result.Reason != ReasonPolicyMissing ||
		result.Failure == nil || result.Failure.Code != ReasonPolicyMissing {
		t.Fatalf("nil evaluator result = %#v", result)
	}
}

func TestEvaluatorPhaseIsolation(t *testing.T) {
	t.Parallel()
	preRule := Rule{
		ID: "pre-block", Selector: Selector{Phases: []Phase{PhasePreCall}},
		Decision: DecisionBlock, SourceIdentity: ".reconc.yml",
	}
	postRule := Rule{
		ID: "post-allow", Selector: Selector{Phases: []Phase{PhasePostResult}},
		Decision: DecisionAllow, SourceIdentity: ".reconc.yml",
	}
	evaluator, input := testActionEvaluator(t, []Rule{postRule, preRule}, Defaults{}, testExternalEffect())
	pre := evaluator.Evaluate(input)
	if pre.Decision != DecisionBlock || pre.PhaseOutcome != OutcomeDispatchBlocked {
		t.Fatalf("pre-call result = %#v", pre)
	}
	resultValue := mustTestValue(t, `{"ok":true}`)
	input.Request.Phase = PhasePostResult
	input.Request.Arguments = nil
	input.Request.Result = &resultValue
	refreshTestIdentities(evaluator, &input)
	post := evaluator.Evaluate(input)
	if post.Decision != DecisionAllow || post.PhaseOutcome != OutcomeDeliveryEligible ||
		containsString(post.MatchedRuleIDs, "pre-block") {
		t.Fatalf("post-result result = %#v", post)
	}
}

func TestEvaluatorReturnsCanonicalOutcomeForEveryPhase(t *testing.T) {
	t.Parallel()
	evaluator, baseline := testActionEvaluator(t, nil, Defaults{}, testExternalEffect())
	value := mustTestValue(t, `{"ok":true}`)
	tests := []struct {
		phase   Phase
		outcome PhaseOutcome
	}{
		{phase: PhasePreCall, outcome: OutcomeDispatchEligible},
		{phase: PhasePostResult, outcome: OutcomeDeliveryEligible},
		{phase: PhaseProgress, outcome: OutcomeProgressEligible},
		{phase: PhaseObservation, outcome: OutcomeRecorded},
	}
	for _, test := range tests {
		t.Run(string(test.phase), func(t *testing.T) {
			input := baseline
			input.Request.Phase = test.phase
			input.Request.Arguments = nil
			switch test.phase {
			case PhasePreCall:
				input.Request.Arguments = baseline.Request.Arguments
			case PhasePostResult:
				input.Request.Result = &value
			case PhaseProgress:
				input.Request.Progress = &value
			}
			refreshTestIdentities(evaluator, &input)
			result := evaluator.Evaluate(input)
			if result.Decision != DecisionAllow || result.PhaseOutcome != test.outcome || result.Failure != nil {
				t.Fatalf("phase %s result = %#v", test.phase, result)
			}
		})
	}
}

func TestEvaluatorRequiresRepositoryEffectEvidence(t *testing.T) {
	t.Parallel()
	effect := Effect{Kind: EffectRepositoryWrite, PathFields: []string{"/path"}}
	evaluator, input := testActionEvaluator(t, nil, Defaults{}, effect)
	result := evaluator.Evaluate(input)
	if result.Reason != ReasonInspectionIncomplete || result.Decision != DecisionBlock {
		t.Fatalf("missing repository evidence result = %#v", result)
	}
	input.RepositoryEffect = &RepositoryEffectCandidate{
		Decision: DecisionWarn, Reason: ReasonRuleMatched,
		RuleIDs: []string{"repository-warning"}, Identity: "repository-effect-v1", Complete: true,
	}
	refreshTestIdentities(evaluator, &input)
	result = evaluator.Evaluate(input)
	if result.Decision != DecisionWarn || !containsString(result.MatchedRuleIDs, "repository-warning") {
		t.Fatalf("repository evidence result = %#v", result)
	}
}

func TestEvaluatorTraceIsBoundedAndRedacted(t *testing.T) {
	t.Parallel()
	const secret = "seeded-super-secret-value"
	secretValue := testStringValue(t, secret)
	rules := make([]Rule, 300)
	for index := range rules {
		rules[index] = testRule(
			"rule-"+strconv.Itoa(index), DecisionBlock,
			Predicate{Source: SourceArguments, Pointer: "/secret", Op: OperatorEqual, Value: &secretValue},
		)
	}
	evaluator, input := testActionEvaluator(t, rules, Defaults{}, testExternalEffect())
	arguments := mustTestValue(t, `{"secret":"`+secret+`"}`)
	input.Request.Arguments = &arguments
	refreshTestIdentities(evaluator, &input)
	result := evaluator.Evaluate(input)
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), secret) {
		t.Fatal("raw operand leaked into evaluation result")
	}
	if result.TraceComplete || result.TraceOmitted == 0 || len(result.Trace) > MaxTraceEntries ||
		result.Trace[len(result.Trace)-1].Omitted == 0 || len(body) == 0 {
		t.Fatalf("trace bound result = %#v", result)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
