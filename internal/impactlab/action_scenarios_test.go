package impactlab

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
)

const (
	fixtureServerIdentity = "hmac-sha256:v1:fixture:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fixtureToolDigest     = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	fixtureRepoIdentity   = "hmac-sha256:v1:fixture:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	fixtureExecutable     = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	budgetKeyID           = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	budgetServerIdentity  = "hmac-sha256:v1:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	budgetRepoIdentity    = "hmac-sha256:v1:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	budgetStateIdentity   = "hmac-sha256:v1:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	budgetSnapshotID      = "hmac-sha256:v1:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	budgetReservationID   = "hmac-sha256:v1:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	budgetScopeID         = "hmac-sha256:v1:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	budgetLineageID       = "hmac-sha256:v1:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:1111111111111111111111111111111111111111111111111111111111111111"
	budgetWindowID        = "hmac-sha256:v1:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:2222222222222222222222222222222222222222222222222222222222222222"
)

func TestActionCorpusRunsExactPreAndPostScenariosThroughProductionEvaluator(t *testing.T) {
	repo, evaluator := makeActionImpactRepo(t, baseActionPolicyRules)
	compiled, err := evaluator.ActionRuntime()
	if err != nil {
		t.Fatal(err)
	}
	cases := []Case{
		newActionFixture("pre-allow", CaseActionPre, `{"target":"staging","operation":"read"}`, actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil, action.CacheEligible, action.OutcomeDispatchEligible, "")),
		newActionFixture("pre-warn", CaseActionPre, `{"target":"staging","operation":"bulk-delete"}`, actionAssertion(action.DecisionWarn, action.ReasonRuleMatched, "database-write", []string{"warn-bulk-delete"}, action.CacheEligible, action.OutcomeDispatchEligible, "")),
		newActionFixture("pre-block", CaseActionPre, `{"target":"production","operation":"read"}`, actionAssertion(action.DecisionBlock, action.ReasonRuleMatched, "database-write", []string{"block-production"}, action.CacheEligible, action.OutcomeDispatchBlocked, "")),
		newActionFixture("pre-approval", CaseActionPre, `{"target":"sensitive","operation":"read"}`, actionAssertion(action.DecisionRequireApproval, action.ReasonApprovalRequired, "database-write", []string{"approve-sensitive"}, action.CacheApprovalPending, action.OutcomeDispatchBlocked, "")),
		newActionFixture("pre-malformed", CaseActionPre, `{"target":"staging","target":"production"}`, actionAssertion(action.DecisionBlock, action.ReasonDuplicateKey, "", nil, action.CacheIdentityMissing, action.OutcomeDispatchBlocked, action.ReasonDuplicateKey)),
		withExtraneousResult(newActionFixture("pre-unsupported", CaseActionPre, `{"target":"staging"}`, actionAssertion(action.DecisionBlock, action.ReasonUnsupportedPhase, "", nil, action.CacheIdentityMissing, action.OutcomeDispatchBlocked, action.ReasonUnsupportedPhase))),
		withIdentityDrift(newActionFixture("pre-stale-lock", CaseActionPre, `{"target":"staging"}`, actionAssertion(action.DecisionBlock, action.ReasonLockMismatch, "", nil, action.CacheIdentityDrift, action.OutcomeDispatchBlocked, action.ReasonLockMismatch)), IdentityLock),
		withIncompleteContext(newActionFixture("pre-incomplete", CaseActionPre, `{"target":"staging"}`, actionAssertion(action.DecisionBlock, action.ReasonContextUntrusted, "", nil, action.CacheEvidenceIncomplete, action.OutcomeDispatchBlocked, action.ReasonContextUntrusted))),
		newActionFixture("post-allow", CaseActionPost, `{"status":"ok","items":2}`, actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil, action.CacheEligible, action.OutcomeDeliveryEligible, "")),
		newActionFixture("post-block", CaseActionPost, `{"status":"error"}`, actionAssertion(action.DecisionBlock, action.ReasonRuleMatched, "database-write", []string{"withhold-error-result"}, action.CacheEligible, action.OutcomeWithheld, "")),
		newActionFixture("post-malformed", CaseActionPost, `{"status":"ok","status":"error"}`, actionAssertion(action.DecisionBlock, action.ReasonDuplicateKey, "", nil, action.CacheIdentityMissing, action.OutcomeWithheld, action.ReasonDuplicateKey)),
		withExtraneousArguments(newActionFixture("post-unsupported", CaseActionPost, `{"status":"ok"}`, actionAssertion(action.DecisionBlock, action.ReasonUnsupportedPhase, "", nil, action.CacheIdentityMissing, action.OutcomeWithheld, action.ReasonUnsupportedPhase))),
		withIncompleteContext(newActionFixture("post-incomplete", CaseActionPost, `{"status":"ok"}`, actionAssertion(action.DecisionBlock, action.ReasonContextUntrusted, "", nil, action.CacheEvidenceIncomplete, action.OutcomeWithheld, action.ReasonContextUntrusted))),
	}
	required := ActionDimensions{
		Classes: []CaseKind{CaseActionPre, CaseActionPost}, Tools: []string{"database-write"},
		Phases: []action.Phase{action.PhasePreCall, action.PhasePostResult},
		Decisions: []action.Decision{
			action.DecisionAllow, action.DecisionWarn, action.DecisionRequireApproval, action.DecisionBlock,
		},
		Provenance: []action.Provenance{action.ProvenanceHostObserved},
		Outcomes: []action.PhaseOutcome{
			action.OutcomeDispatchEligible, action.OutcomeDispatchBlocked,
			action.OutcomeDeliveryEligible, action.OutcomeWithheld,
		},
		Approvals:           []action.ApprovalStatus{action.ApprovalNone},
		ApprovalTransitions: []actionapproval.Status{},
	}
	cases[0].Action.Expected.Approval = &ActionApprovalAssertion{
		Status: action.ApprovalNone, Identity: "approval-none",
	}
	_, approvalRequirement := exactApprovalEvaluation(t, *cases[3].Action, compiled)
	cases[3].Action.Expected.Approval = &ActionApprovalAssertion{
		Status: action.ApprovalNone, Identity: "approval-none",
		RequiredApprovalIdentity: approvalRequirement,
	}
	corpus, err := NewCorpusWithActionCoverage(repo, cases, AllEventClasses(), required)
	if err != nil {
		t.Fatal(err)
	}
	if !corpus.Completeness.CompleteReplay || !corpus.Completeness.Action.Complete {
		t.Fatalf("action completeness = %+v", corpus.Completeness)
	}
	report, err := Compare(repo, corpus, candidateFromEvaluator(t, evaluator), evaluator, evaluator)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.ActionCaseCount != len(cases) || report.Summary.ActionDecisionChanges != 0 ||
		!report.DeltaGate.Passed || len(report.ActionCorpusUnmatchedRules) != 0 {
		t.Fatalf("action report = %+v", report)
	}
	body, err := MarshalCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCorpus(body)
	if err != nil || decoded.CorpusID != corpus.CorpusID {
		t.Fatalf("decode action corpus = %v, %+v", err, decoded)
	}
}

func TestActionCaseRequiresAssertionForEveryApprovalRelevantState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ActionCase)
	}{
		{name: "approval decision", mutate: func(value *ActionCase) {
			value.Expected.Decision = action.DecisionRequireApproval
			value.Expected.Reason = action.ReasonApprovalRequired
			value.Expected.Cache = ActionCacheAssertion{Reason: action.CacheApprovalPending}
			value.Expected.PhaseOutcome = action.OutcomeDispatchBlocked
		}},
		{name: "approval snapshot", mutate: func(value *ActionCase) {
			value.State.Approval = action.ApprovalSnapshot{Status: action.ApprovalPending, Identity: "approval-pending"}
			value.State.ApprovalTransition = actionapproval.StatusPending
		}},
		{name: "approval transition", mutate: func(value *ActionCase) {
			value.State.Approval = action.ApprovalSnapshot{Status: action.ApprovalNone, Identity: "approval-rejected"}
			value.State.ApprovalTransition = actionapproval.StatusRejected
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newActionFixture("approval-assertion", CaseActionPre, `{"target":"staging"}`,
				actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
					action.CacheEligible, action.OutcomeDispatchEligible, ""))
			test.mutate(fixture.Action)
			if _, err := validateActionCase(fixture.Kind, *fixture.Action); err == nil ||
				!strings.Contains(err.Error(), "requires an exact approval assertion") {
				t.Fatalf("missing approval assertion error = %v", err)
			}
		})
	}

	baseline := newActionFixture("no-approval", CaseActionPre, `{"target":"staging"}`,
		actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
			action.CacheEligible, action.OutcomeDispatchEligible, ""))
	if _, err := validateActionCase(baseline.Kind, *baseline.Action); err != nil {
		t.Fatalf("ordinary action case unexpectedly requires approval assertion: %v", err)
	}
}

func TestActionCorpusPreservesTrustedContextProvenance(t *testing.T) {
	repo, evaluator := makeActionImpactRepo(t, trustedContextActionPolicyRule)
	trusted := newActionFixture("trusted-context", CaseActionPre, `{"target":"staging"}`,
		actionAssertion(action.DecisionAllow, action.ReasonRuleMatched, "database-write", []string{"allow-trusted-context"}, action.CacheEligible, action.OutcomeDispatchEligible, ""))
	untrustedExpected := actionAssertion(action.DecisionBlock, action.ReasonContextUntrusted, "database-write",
		[]string{"allow-trusted-context"}, action.CacheEvidenceIncomplete, action.OutcomeDispatchBlocked, "")
	untrustedExpected.Completeness.ContextComplete = false
	untrustedExpected.Completeness.Missing = []action.MissingEvidence{{Field: action.EvidenceContext, Reason: action.ReasonContextUntrusted}}
	untrusted := newActionFixture("untrusted-context-spoof", CaseActionPre, `{"target":"staging"}`, untrustedExpected)
	untrusted.Action.Request.Context[0].Provenance = action.ProvenanceAgentSupplied
	required := ActionDimensions{
		Classes: []CaseKind{CaseActionPre}, Tools: []string{"database-write"}, Phases: []action.Phase{action.PhasePreCall},
		Decisions:           []action.Decision{action.DecisionAllow, action.DecisionBlock},
		Provenance:          []action.Provenance{action.ProvenanceAgentSupplied, action.ProvenanceHostObserved},
		Outcomes:            []action.PhaseOutcome{action.OutcomeDispatchEligible, action.OutcomeDispatchBlocked},
		Approvals:           []action.ApprovalStatus{action.ApprovalNone},
		ApprovalTransitions: []actionapproval.Status{},
	}
	trusted.Action.Expected.Approval = &ActionApprovalAssertion{
		Status: action.ApprovalNone, Identity: "approval-none",
	}
	corpus, err := NewCorpusWithActionCoverage(repo, []Case{trusted, untrusted}, AllEventClasses(), required)
	if err != nil {
		t.Fatal(err)
	}
	if !corpus.Completeness.Action.Complete {
		t.Fatalf("trusted-context coverage = %+v", corpus.Completeness.Action)
	}
	if _, err := Compare(repo, corpus, candidateFromEvaluator(t, evaluator), evaluator, evaluator); err != nil {
		t.Fatal(err)
	}
}

func TestActionCorpusReplaysBudgetReservationExhaustionContentionAndCorruption(t *testing.T) {
	repo, evaluator := makeBudgetActionImpactRepo(t)
	compiled, err := evaluator.ActionRuntime()
	if err != nil {
		t.Fatal(err)
	}
	available := budgetActionFixture(compiled, "budget-reserved", action.BudgetUsage{},
		action.BudgetUsage{CallCount: 1, RateWindow: 1}, true, true,
		actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
			action.CacheEligible, action.OutcomeDispatchEligible, ""))
	exhausted := budgetActionFixture(compiled, "budget-exhausted",
		action.BudgetUsage{CallCount: 2, RateWindow: 1}, action.BudgetUsage{}, false, false,
		actionAssertion(action.DecisionBlock, action.ReasonBudgetExhausted, "database-write", nil,
			action.CacheEligible, action.OutcomeDispatchBlocked, ""))
	contended := budgetActionFixture(compiled, "budget-contended", action.BudgetUsage{},
		action.BudgetUsage{CallCount: 2, RateWindow: 1}, false, false,
		actionAssertion(action.DecisionBlock, action.ReasonBudgetExhausted, "database-write", nil,
			action.CacheEligible, action.OutcomeDispatchBlocked, ""))
	corrupt := budgetActionFixture(compiled, "budget-corrupt", action.BudgetUsage{},
		action.BudgetUsage{CallCount: 1, RateWindow: 1}, true, true,
		actionAssertion(action.DecisionBlock, action.ReasonStateCorrupt, "", nil,
			action.CacheIdentityMissing, action.OutcomeDispatchBlocked, action.ReasonStateCorrupt))
	corrupt.Action.State.Budget.Candidates[0].ScopeIdentity = fixtureServerIdentity

	cases := []Case{available, exhausted, contended, corrupt}
	corpus, err := NewCorpus(repo, cases, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compare(repo, corpus, candidateFromEvaluator(t, evaluator), evaluator, evaluator); err != nil {
		t.Fatal(err)
	}
}

func TestActionCorpusReplaysEveryApprovalSnapshotTransitionAndExactRequirementIdentity(t *testing.T) {
	repo, evaluator := makeActionImpactRepo(t, baseActionPolicyRules)
	compiled, err := evaluator.ActionRuntime()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		status     action.ApprovalStatus
		transition actionapproval.Status
		identity   string
		cache      action.CacheReason
	}{
		{name: "none", status: action.ApprovalNone, identity: "approval-none", cache: action.CacheApprovalPending},
		{name: "pending", status: action.ApprovalPending, transition: actionapproval.StatusPending, identity: "approval-pending", cache: action.CacheApprovalPending},
		{name: "current", status: action.ApprovalCurrentUnconsumed, transition: actionapproval.StatusApproved, identity: driftSHAIdentity, cache: action.CacheEligible},
		{name: "consumed", status: action.ApprovalConsumed, transition: actionapproval.StatusApproved, identity: driftSHAIdentity, cache: action.CacheApprovalPending},
		{name: "rejected", status: action.ApprovalNone, transition: actionapproval.StatusRejected, identity: "approval-rejected", cache: action.CacheApprovalPending},
		{name: "expired", status: action.ApprovalNone, transition: actionapproval.StatusExpired, identity: "approval-expired", cache: action.CacheApprovalPending},
		{name: "cancelled", status: action.ApprovalNone, transition: actionapproval.StatusCancelled, identity: "approval-cancelled", cache: action.CacheApprovalPending},
		{name: "unavailable", status: action.ApprovalNone, transition: actionapproval.StatusUnavailable, identity: "approval-unavailable", cache: action.CacheApprovalPending},
		{name: "malformed", status: action.ApprovalNone, transition: actionapproval.StatusMalformed, identity: "approval-malformed", cache: action.CacheApprovalPending},
		{name: "replayed", status: action.ApprovalNone, transition: actionapproval.StatusReplayed, identity: "approval-replayed", cache: action.CacheApprovalPending},
	}
	cases := make([]Case, 0, len(tests))
	for _, test := range tests {
		fixture := newActionFixture("approval-"+test.name, CaseActionPre,
			`{"target":"sensitive","operation":"read"}`,
			actionAssertion(action.DecisionRequireApproval, action.ReasonApprovalRequired,
				"database-write", []string{"approve-sensitive"}, test.cache,
				action.OutcomeDispatchBlocked, ""))
		fixture.Action.State.Approval = action.ApprovalSnapshot{Status: test.status, Identity: test.identity}
		fixture.Action.State.ApprovalTransition = test.transition
		result, wantRequirement := exactApprovalEvaluation(t, *fixture.Action, compiled)
		if result.Decision != action.DecisionRequireApproval || result.Reason != action.ReasonApprovalRequired ||
			result.RequiredApprovalIdentity != wantRequirement || result.Cache.Reason != test.cache ||
			result.Cache.Eligible != (test.cache == action.CacheEligible) {
			t.Fatalf("approval %s result = %+v, want requirement %s", test.name, result, wantRequirement)
		}
		approval := &ActionApprovalAssertion{
			Status: test.status, Identity: test.identity,
			RequiredApprovalIdentity: wantRequirement,
			Transition:               test.transition,
		}
		observation, observationErr := observationFromResult(result, approval)
		if observationErr != nil {
			t.Fatal(observationErr)
		}
		fixture.Action.Expected = observation.Outcome
		cases = append(cases, fixture)
	}
	required := ActionDimensions{
		Classes: []CaseKind{CaseActionPre}, Tools: []string{"database-write"},
		Phases: []action.Phase{action.PhasePreCall}, Decisions: []action.Decision{action.DecisionRequireApproval},
		Provenance: []action.Provenance{action.ProvenanceHostObserved},
		Outcomes:   []action.PhaseOutcome{action.OutcomeDispatchBlocked},
		Approvals: []action.ApprovalStatus{
			action.ApprovalNone, action.ApprovalPending,
			action.ApprovalCurrentUnconsumed, action.ApprovalConsumed,
		},
		ApprovalTransitions: []actionapproval.Status{
			actionapproval.StatusPending, actionapproval.StatusApproved,
			actionapproval.StatusRejected, actionapproval.StatusExpired,
			actionapproval.StatusCancelled, actionapproval.StatusUnavailable,
			actionapproval.StatusMalformed, actionapproval.StatusReplayed,
		},
	}
	corpus, err := NewCorpusWithActionCoverage(repo, cases, AllEventClasses(), required)
	if err != nil {
		t.Fatal(err)
	}
	if !corpus.Completeness.Action.Complete {
		t.Fatalf("approval action coverage = %+v", corpus.Completeness.Action)
	}
	if _, err := Compare(repo, corpus, candidateFromEvaluator(t, evaluator), evaluator, evaluator); err != nil {
		t.Fatal(err)
	}

	mutations := []struct {
		name   string
		mutate func(*ActionCase)
	}{
		{name: "status", mutate: func(value *ActionCase) {
			if value.Expected.Approval.Status == action.ApprovalNone {
				value.Expected.Approval.Status = action.ApprovalConsumed
			} else {
				value.Expected.Approval.Status = action.ApprovalNone
			}
		}},
		{name: "identity", mutate: func(value *ActionCase) { value.Expected.Approval.Identity = "approval-altered" }},
		{name: "requirement", mutate: func(value *ActionCase) { value.Expected.Approval.RequiredApprovalIdentity = driftSHAIdentity }},
		{name: "transition", mutate: func(value *ActionCase) { value.Expected.Approval.Transition = actionapproval.StatusReplayed }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			mutated := cloneCorpusForTest(t, corpus)
			mutation.mutate(mutated.Cases[0].Action)
			mutated.CorpusID = mustCorpusIdentity(t, mutated)
			_, marshalErr := MarshalCorpus(mutated)
			if marshalErr == nil {
				_, marshalErr = Compare(repo, mutated, candidateFromEvaluator(t, evaluator), evaluator, evaluator)
			}
			if marshalErr == nil {
				t.Fatal("altered approval assertion passed")
			}
		})
	}
	current := ActionObservation{Outcome: cases[0].Action.Expected, Trace: []action.TraceEntry{}, TraceComplete: true}
	candidate := cloneActionObservation(current)
	candidate.Outcome.Approval.RequiredApprovalIdentity = driftSHAIdentity
	if got := actionDeltas(current, candidate); !slicesEqualActionDelta(got, []ActionDeltaKind{DeltaApproval}) {
		t.Fatalf("approval deltas = %v", got)
	}
}

func TestActionScenarioAppliesEveryDeclaredResampleDrift(t *testing.T) {
	_, evaluator := makeActionImpactRepo(t, "")
	compiled, err := evaluator.ActionRuntime()
	if err != nil {
		t.Fatal(err)
	}
	components := []ActionIdentityComponent{
		IdentityPlan, IdentitySource, IdentityPolicy, IdentityLock, IdentityAuthority,
		IdentityServer, IdentityToolContract, IdentityExecutable, IdentityRepository, IdentityContext,
		IdentityPrincipal, IdentityCredentials, IdentityState, IdentityBudget, IdentityReservation, IdentityApproval,
		IdentityTaint, IdentityRepositoryEffect,
	}
	for _, component := range components {
		t.Run(string(component), func(t *testing.T) {
			fixture := newActionFixture("identity-drift", CaseActionPre, `{"target":"staging"}`,
				actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
					action.CacheEligible, action.OutcomeDispatchEligible, ""))
			fixture.Action.State.ResampleDrift = []ActionIdentityComponent{component}
			observation, err := evaluateActionScenario(*fixture.Action, compiled)
			if err != nil {
				t.Fatal(err)
			}
			wantCache := action.CacheIdentityDrift
			if component == IdentityState || component == IdentityBudget || component == IdentityReservation ||
				component == IdentityApproval || component == IdentityRepositoryEffect {
				wantCache = action.CacheStateStale
			}
			if observation.Outcome.Decision != action.DecisionBlock || observation.Outcome.FailureCode == "" ||
				observation.Outcome.Cache.Eligible || observation.Outcome.Cache.Reason != wantCache {
				t.Fatalf("identity drift outcome = %+v", observation.Outcome)
			}
		})
	}
}

func TestActionComparisonRejectsEveryCandidateIdentityDrift(t *testing.T) {
	repo, evaluator := makeActionImpactRepo(t, "")
	fixture := newActionFixture("candidate-identity", CaseActionPre, `{"target":"staging"}`,
		actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
			action.CacheEligible, action.OutcomeDispatchEligible, ""))
	corpus, err := NewCorpus(repo, []Case{fixture}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Candidate)
	}{
		{name: "kind", mutate: func(value *Candidate) { value.Kind = "unknown" }},
		{name: "name", mutate: func(value *Candidate) { value.Name = "../private" }},
		{name: "source", mutate: func(value *Candidate) { value.SourceDigest = driftDigest }},
		{name: "lock", mutate: func(value *Candidate) { value.LockDigest = driftDigest }},
		{name: "plan", mutate: func(value *Candidate) { value.ActionPlanIdentity = driftSHAIdentity }},
		{name: "tools", mutate: func(value *Candidate) { value.ActionToolCount++ }},
		{name: "action rules", mutate: func(value *Candidate) { value.ActionRuleCount++ }},
		{name: "repository rules", mutate: func(value *Candidate) { value.RuleCount++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := candidateFromEvaluator(t, evaluator)
			test.mutate(&candidate)
			if _, err := Compare(repo, corpus, candidate, evaluator, evaluator); err == nil ||
				!strings.Contains(err.Error(), "candidate metadata") {
				t.Fatalf("candidate identity drift error = %v", err)
			}
		})
	}
	wrongKind := newActionFixture("wrong-kind", CaseActionPre, `{"target":"staging"}`,
		actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
			action.CacheEligible, action.OutcomeDispatchEligible, ""))
	wrongKind.Kind = CaseActionPost
	if _, err := NewCorpus(repo, []Case{wrongKind}, AllEventClasses()); err == nil ||
		!strings.Contains(err.Error(), "kind does not match") {
		t.Fatalf("action discriminant mismatch error = %v", err)
	}
}

func TestActionCorpusCoversOversizedCallWithoutWeakeningCorpusBound(t *testing.T) {
	repo, evaluator := makeActionImpactRepo(t, baseActionPolicyRules)
	payload := append([]byte(`{"payload":"`), bytes.Repeat([]byte{'a'}, action.MaxArgumentBytes)...)
	payload = append(payload, []byte(`"}`)...)
	fixture := newActionFixture("pre-oversized", CaseActionPre, string(payload),
		actionAssertion(action.DecisionBlock, action.ReasonLimitExceeded, "", nil, action.CacheIdentityMissing, action.OutcomeDispatchBlocked, action.ReasonLimitExceeded))
	corpus, err := NewCorpus(repo, []Case{fixture}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	body, err := MarshalCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= action.MaxArgumentBytes || len(body) > MaxCorpusBytes {
		t.Fatalf("oversized corpus bytes = %d", len(body))
	}
	if bytes.Contains(body, []byte(`payload`)) || corpus.Cases[0].Action.RedactionCount != 1 ||
		corpus.Cases[0].Action.SelectedValues[0].Category != "oversized-value" {
		t.Fatalf("oversized corpus did not use the canonical privacy surrogate")
	}
	if _, err := Compare(repo, corpus, candidateFromEvaluator(t, evaluator), evaluator, evaluator); err != nil {
		t.Fatal(err)
	}
	nonCanonical := cloneCorpusForTest(t, corpus)
	nonCanonical.Cases[0].Action.Request.Arguments = ActionPayload(`"` + strings.Repeat("z", action.MaxArgumentBytes) + `"`)
	nonCanonical.CorpusID = mustCorpusIdentity(t, nonCanonical)
	if _, err := MarshalCorpus(nonCanonical); err == nil || !strings.Contains(err.Error(), "canonical privacy surrogate") {
		t.Fatalf("non-canonical oversized payload error = %v", err)
	}
}

func TestImpactResourceLimitsMatchFrozenContractAtBoundaries(t *testing.T) {
	if MaxCorpusBytes != 64<<20 || MaxDeltaManifestBytes != 8<<20 || maxCases != 10000 {
		t.Fatalf("impact limits = corpus:%d manifest:%d cases:%d", MaxCorpusBytes, MaxDeltaManifestBytes, maxCases)
	}
	corpusBytes := make([]byte, MaxCorpusBytes+1)
	corpusBytes[0] = '!'
	for _, boundary := range []struct {
		name      string
		size      int
		oversized bool
	}{
		{name: "minus one", size: MaxCorpusBytes - 1},
		{name: "exact", size: MaxCorpusBytes},
		{name: "plus one", size: MaxCorpusBytes + 1, oversized: true},
	} {
		t.Run("corpus "+boundary.name, func(t *testing.T) {
			_, err := DecodeCorpus(corpusBytes[:boundary.size])
			if err == nil || strings.Contains(err.Error(), "oversized") != boundary.oversized {
				t.Fatalf("corpus boundary error = %v", err)
			}
		})
	}
	for _, boundary := range []struct {
		name      string
		size      int
		oversized bool
	}{
		{name: "minus one", size: MaxDeltaManifestBytes - 1},
		{name: "exact", size: MaxDeltaManifestBytes},
		{name: "plus one", size: MaxDeltaManifestBytes + 1, oversized: true},
	} {
		t.Run("manifest "+boundary.name, func(t *testing.T) {
			_, err := DecodeDeltaManifest(corpusBytes[:boundary.size])
			if err == nil || strings.Contains(err.Error(), "oversized") != boundary.oversized {
				t.Fatalf("manifest boundary error = %v", err)
			}
		})
	}

	makeCases := func(count int) []Case {
		cases := make([]Case, count)
		for index := range cases {
			cases[index] = Case{
				ID: "case-" + fixedDecimal(index, 5), Kind: CaseRepository,
				Repository: &RepositoryCase{Inputs: runtime.Empty(), RedactedEventClasses: []EventClass{}},
			}
		}
		return cases
	}
	for _, count := range []int{maxCases - 1, maxCases} {
		cases := makeCases(count)
		corpus := Corpus{FormatVersion: CorpusFormatVersion, Cases: cases}
		corpus.Completeness = buildCompleteness(cases, AllEventClasses(), emptyActionDimensions())
		corpus.CorpusID = mustCorpusIdentity(t, corpus)
		if err := validateCorpus(corpus); err != nil {
			t.Fatalf("case boundary %d: %v", count, err)
		}
	}
	tooMany := Corpus{FormatVersion: CorpusFormatVersion, CorpusID: "invalid", Cases: makeCases(maxCases + 1)}
	if err := validateCorpus(tooMany); err == nil || !strings.Contains(err.Error(), "1..10000") {
		t.Fatalf("case plus-one error = %v", err)
	}
}

func TestActionDeltaManifestRequiresExactCurrentCandidateAndExpiryBindings(t *testing.T) {
	currentRepo, current := makeActionImpactRepo(t, "")
	_, candidate := makeActionImpactRepo(t, `
    - id: block-staging
      selector:
        tool_ids: [database-write]
        phases: [pre_call]
      when:
        predicate:
          source: arguments
          pointer: /target
          op: eq
          value: staging
      decision: block
      message: Staging blocked.
`)
	fixture := newActionFixture("staging-write", CaseActionPre, `{"target":"staging"}`,
		actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil, action.CacheEligible, action.OutcomeDispatchEligible, ""))
	corpus, err := NewCorpus(currentRepo, []Case{fixture}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	report, err := Compare(currentRepo, corpus, candidateFromEvaluator(t, candidate), current, candidate)
	if err != nil {
		t.Fatal(err)
	}
	comparison := report.Cases[0]
	if report.DeltaGate.Passed || report.DeltaGate.RequiredCount != 1 || comparison.Action == nil ||
		!slicesEqualActionDelta(comparison.Action.Deltas, []ActionDeltaKind{
			DeltaDecision, DeltaNewlyBlocked, DeltaReason, DeltaRuleTrace, DeltaPhaseOutcome,
		}) {
		t.Fatalf("unreviewed action delta = %+v", report)
	}
	entry := reviewedEntry(report, 0, DeltaNewlyBlocked)
	manifest, err := NewDeltaManifest([]ReviewedActionDelta{entry})
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := ApplyDeltaManifest(report, manifest, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || !reviewed.DeltaGate.Passed || !reviewed.Cases[0].Action.Reviewed {
		t.Fatalf("reviewed gate = %+v, %v", reviewed.DeltaGate, err)
	}

	tests := []struct {
		name   string
		mutate func(*ReviewedActionDelta)
		want   string
	}{
		{name: "orphan", mutate: func(value *ReviewedActionDelta) { value.CaseID = "other-case" }, want: "orphaned"},
		{name: "case identity", mutate: func(value *ReviewedActionDelta) { value.CaseIdentity = driftSHAIdentity }, want: "stale"},
		{name: "candidate digest", mutate: func(value *ReviewedActionDelta) { value.CandidateLockDigest = driftDigest }, want: "stale"},
		{name: "old result", mutate: func(value *ReviewedActionDelta) { value.Current.Decision = action.DecisionWarn }, want: "stale"},
		{name: "new result", mutate: func(value *ReviewedActionDelta) { value.Candidate.Reason = action.ReasonHostUnmatched }, want: "stale"},
		{name: "expired", mutate: func(value *ReviewedActionDelta) { value.Permanent = false; value.ExpiresAt = "2029-01-01T00:00:00Z" }, want: "expired"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := entry
			test.mutate(&mutated)
			candidateManifest, manifestErr := NewDeltaManifest([]ReviewedActionDelta{mutated})
			if manifestErr != nil {
				t.Fatalf("construct mutation: %v", manifestErr)
			}
			_, applyErr := ApplyDeltaManifest(report, candidateManifest, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
			if applyErr == nil || !strings.Contains(applyErr.Error(), test.want) {
				t.Fatalf("apply error = %v, want %q", applyErr, test.want)
			}
		})
	}

	invalid := []ReviewedActionDelta{entry, entry}
	if _, err := NewDeltaManifest(invalid); err == nil || !strings.Contains(err.Error(), "unique") {
		t.Fatalf("duplicate manifest error = %v", err)
	}
	wildcard := entry
	wildcard.CaseID = "*"
	if _, err := NewDeltaManifest([]ReviewedActionDelta{wildcard}); err == nil {
		t.Fatal("wildcard manifest entry was accepted")
	}
	missingRationale := entry
	missingRationale.Rationale = ""
	if _, err := NewDeltaManifest([]ReviewedActionDelta{missingRationale}); err == nil {
		t.Fatal("missing manifest rationale was accepted")
	}
	wrongDelta := entry
	wrongDelta.Delta = DeltaNewlyAllowed
	wrongDeltaManifest, err := NewDeltaManifest([]ReviewedActionDelta{wrongDelta})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyDeltaManifest(report, wrongDeltaManifest, time.Now().UTC()); err == nil ||
		!strings.Contains(err.Error(), "orphaned") {
		t.Fatalf("wrong manifest delta error = %v", err)
	}
	missingStatus := entry
	missingStatus.Permanent = false
	if _, err := NewDeltaManifest([]ReviewedActionDelta{missingStatus}); err == nil {
		t.Fatal("manifest review without permanent status or expiry was accepted")
	}
	conflictingStatus := entry
	conflictingStatus.ExpiresAt = "2031-01-01T00:00:00Z"
	if _, err := NewDeltaManifest([]ReviewedActionDelta{conflictingStatus}); err == nil {
		t.Fatal("manifest review with permanent status and expiry was accepted")
	}
	privateRationale := entry
	privateRationale.Rationale = "reviewed with sk-secretvalue123"
	if _, err := NewDeltaManifest([]ReviewedActionDelta{privateRationale}); err == nil {
		t.Fatal("secret-shaped manifest rationale was accepted")
	}
	invalidUTF8Rationale := entry
	invalidUTF8Rationale.Rationale = string([]byte{'o', 'k', 0xff})
	if _, err := NewDeltaManifest([]ReviewedActionDelta{invalidUTF8Rationale}); err == nil {
		t.Fatal("invalid UTF-8 manifest rationale was accepted")
	}
	tooManyEntries := make([]ReviewedActionDelta, maxCases+1)
	if _, err := NewDeltaManifest(tooManyEntries); err == nil || !strings.Contains(err.Error(), "10000 entries") {
		t.Fatalf("manifest entry bound error = %v", err)
	}
	aliased := []ReviewedActionDelta{entry}
	clonedManifest, err := NewDeltaManifest(aliased)
	if err != nil {
		t.Fatal(err)
	}
	aliased[0].Current.MatchedRuleIDs = append(aliased[0].Current.MatchedRuleIDs, "mutated")
	if len(clonedManifest.Entries[0].Current.MatchedRuleIDs) != len(entry.Current.MatchedRuleIDs) {
		t.Fatal("delta manifest retained caller-owned assertion slices")
	}
	body, err := MarshalDeltaManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	mutatedBody := bytes.Replace(body, []byte(`"rationale": "reviewed staging block"`), []byte(`"rationale": "changed"`), 1)
	if _, err := DecodeDeltaManifest(mutatedBody); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("mutated manifest error = %v", err)
	}
}

func TestActionDeltaGateTreatsBlockToWarnAsNewPermission(t *testing.T) {
	repo := t.TempDir()
	config := `actions:
  tools:
    - id: database-write
      transport: mcp_stdio
      server_label: database
      server_fingerprint: ` + fixtureServerIdentity + `
      tool: execute
      effect:
        kind: external
  defaults:
    declared_tool: block
  rules: []
rules: []
`
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	current, _, err := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(repo)
	if err != nil {
		t.Fatal(err)
	}
	_, candidateLock, _, err := compiler.RenderRepoPolicyWithCandidate(repo, "test", compiler.CandidateSource{
		Kind: policy.SourcePolicyFile, Name: "warn-declared",
		Content: `actions:
  defaults:
    declared_tool: warn
  rules: []
rules: []
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := runtime.NewCompiledPolicyEvaluator(candidateLock)
	if err != nil {
		t.Fatal(err)
	}
	fixture := newActionFixture("block-to-warn", CaseActionPre, `{"target":"staging"}`,
		actionAssertion(action.DecisionBlock, action.ReasonDeclaredTool, "database-write", nil,
			action.CacheEligible, action.OutcomeDispatchBlocked, ""))
	corpus, err := NewCorpus(repo, []Case{fixture}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	report, err := Compare(repo, corpus, candidateFromEvaluator(t, candidate), current, candidate)
	if err != nil {
		t.Fatal(err)
	}
	want := []ActionDeltaKind{
		DeltaDecision, DeltaNewlyAllowed, DeltaNewlyWarned, DeltaPhaseOutcome,
	}
	if report.DeltaGate.Passed || report.DeltaGate.RequiredCount != 1 ||
		!slicesEqualActionDelta(report.Cases[0].Action.Deltas, want) {
		t.Fatalf("block-to-warn permission gate = %+v", report)
	}
	manifest, err := NewDeltaManifest([]ReviewedActionDelta{
		reviewedEntry(report, 0, DeltaNewlyAllowed),
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := ApplyDeltaManifest(report, manifest, time.Now().UTC())
	if err != nil || !reviewed.DeltaGate.Passed {
		t.Fatalf("review block-to-warn permission = %+v, %v", reviewed.DeltaGate, err)
	}
}

func TestActionDeltaManifestPartialReviewKeepsGateBlocked(t *testing.T) {
	currentRepo, current := makeActionImpactRepo(t, "")
	_, candidate := makeActionImpactRepo(t, `
    - id: block-staging
      selector:
        tool_ids: [database-write]
        phases: [pre_call]
      when:
        predicate:
          source: arguments
          pointer: /target
          op: eq
          value: staging
      decision: block
      message: Staging blocked.
`)
	cases := []Case{
		newActionFixture("staging-one", CaseActionPre, `{"target":"staging"}`,
			actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil, action.CacheEligible, action.OutcomeDispatchEligible, "")),
		newActionFixture("staging-two", CaseActionPre, `{"target":"staging"}`,
			actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil, action.CacheEligible, action.OutcomeDispatchEligible, "")),
	}
	corpus, err := NewCorpus(currentRepo, cases, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	report, err := Compare(currentRepo, corpus, candidateFromEvaluator(t, candidate), current, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if report.DeltaGate.RequiredCount != 2 {
		t.Fatalf("required delta reviews = %d", report.DeltaGate.RequiredCount)
	}
	manifest, err := NewDeltaManifest([]ReviewedActionDelta{reviewedEntry(report, 0, DeltaNewlyBlocked)})
	if err != nil {
		t.Fatal(err)
	}
	partial, err := ApplyDeltaManifest(report, manifest, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if partial.DeltaGate.Passed || partial.DeltaGate.ReviewedCount != 1 ||
		len(partial.DeltaGate.UnreviewedCases) != 1 || partial.DeltaGate.UnreviewedCases[0] != "staging-two" {
		t.Fatalf("partial review gate = %+v", partial.DeltaGate)
	}
}

func TestActionCorpusPrivacyAndCompletenessMutationsFailClosed(t *testing.T) {
	repo, evaluator := makeActionImpactRepo(t, "")
	fixture := newActionFixture("private", CaseActionPre, `{"authorization":"Bearer sk-secretvalue123","target":"staging"}`,
		actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil, action.CacheEligible, action.OutcomeDispatchEligible, ""))
	fixture.Action.Expected.Approval = &ActionApprovalAssertion{
		Status: action.ApprovalNone, Identity: "approval-none",
	}
	corpus, err := NewCorpus(repo, []Case{fixture}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	body, err := MarshalCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("secretvalue123")) || !bytes.Contains(body, []byte(`"category": "credential"`)) ||
		corpus.Completeness.CompleteReplay || corpus.Completeness.RedactionCount != 1 {
		t.Fatalf("private action corpus = %s", body)
	}
	if _, err := Compare(repo, corpus, candidateFromEvaluator(t, evaluator), evaluator, evaluator); err != nil {
		t.Fatal(err)
	}
	syntheticUserPath := "/" + "Users/example/private"
	if _, _, err := sanitizeActionRawValue(ActionPayload(`{"path":"`+syntheticUserPath+`"`), action.SourceArguments,
		action.ProvenanceAgentSupplied, "", []ActionValueSummary{}); err == nil {
		t.Fatal("malformed physical path escaped action privacy validation")
	}
	cleanedPath, pathSummaries, err := sanitizeActionRawValue(
		ActionPayload(`{"path":"`+syntheticUserPath+`"}`), action.SourceArguments,
		action.ProvenanceAgentSupplied, "", []ActionValueSummary{},
	)
	if err != nil || strings.Contains(string(cleanedPath), "/"+"Users/") ||
		len(pathSummaries) != 1 || pathSummaries[0].Category != "physical-path" {
		t.Fatalf("physical path privacy = %q, %+v, %v", cleanedPath, pathSummaries, err)
	}
	for _, privatePath := range []string{
		"log at " + syntheticUserPath + "/output.txt",
		"log at (/var/private/output.txt)",
		"log at C:\\" + "Users\\example\\private\\output.txt",
		"log at file://" + syntheticUserPath + "/output.txt",
	} {
		privateJSON, marshalErr := json.Marshal(privatePath)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		embeddedPath, embeddedSummaries, embeddedErr := sanitizeActionRawValue(
			ActionPayload(`{"message":`+string(privateJSON)+`}`), action.SourceResult,
			action.ProvenanceHostObserved, "", []ActionValueSummary{},
		)
		if embeddedErr != nil || strings.Contains(string(embeddedPath), "output.txt") ||
			len(embeddedSummaries) != 1 || embeddedSummaries[0].Category != "physical-path" {
			t.Fatalf("embedded physical path privacy = %q, %+v, %v", embeddedPath, embeddedSummaries, embeddedErr)
		}
	}
	safeURL := ActionPayload(`{"endpoint":"https://example.test/public/result"}`)
	cleanedURL, urlSummaries, err := sanitizeActionRawValue(
		safeURL, action.SourceResult, action.ProvenanceHostObserved, "", []ActionValueSummary{},
	)
	if err != nil || cleanedURL != safeURL || len(urlSummaries) != 0 {
		t.Fatalf("safe URL privacy = %q, %+v, %v", cleanedURL, urlSummaries, err)
	}
	if _, _, err := sanitizeActionRawValue(
		ActionPayload(`{"sk-secretvalue123":"safe"}`), action.SourceArguments,
		action.ProvenanceAgentSupplied, "", []ActionValueSummary{},
	); err == nil {
		t.Fatal("secret-shaped JSON member name escaped action privacy validation")
	}
	if _, _, err := sanitizeActionRawValue(
		ActionPayload(`{"auth\u006frization":"safe"`), action.SourceArguments,
		action.ProvenanceAgentSupplied, "", []ActionValueSummary{},
	); err == nil {
		t.Fatal("escaped secret-shaped malformed JSON escaped action privacy validation")
	}
	if _, _, err := sanitizeActionRawValue(
		ActionPayload(string([]byte{'"', 0xff, '"'})), action.SourceArguments,
		action.ProvenanceAgentSupplied, "", []ActionValueSummary{},
	); err == nil {
		t.Fatal("invalid UTF-8 action payload escaped deterministic export validation")
	}
	privateContext := newActionFixture("private-context", CaseActionPre, `{"target":"staging"}`,
		actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
			action.CacheEligible, action.OutcomeDispatchEligible, ""))
	privateContext.Action.Request.Context[0].Name = "sk-secretvalue123"
	if _, err := NewCorpus(repo, []Case{privateContext}, AllEventClasses()); err == nil {
		t.Fatal("secret-shaped context name escaped action privacy validation")
	}
	privateCaseID := newActionFixture("sk-secretvalue123", CaseActionPre, `{"target":"staging"}`,
		actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
			action.CacheEligible, action.OutcomeDispatchEligible, ""))
	if _, err := NewCorpus(repo, []Case{privateCaseID}, AllEventClasses()); err == nil {
		t.Fatal("secret-shaped case id escaped action privacy validation")
	}
	oversizedSafe := ActionPayload(`"` + strings.Repeat("x", maxValueBytes+1) + `"`)
	cleaned, summaries, err := sanitizeActionRawValue(oversizedSafe, action.SourceResult,
		action.ProvenanceHostObserved, "", []ActionValueSummary{})
	if err != nil || cleaned != ActionPayload(`"\u003credacted\u003e"`) || len(summaries) != 1 || summaries[0].Category != "oversized-value" {
		t.Fatalf("oversized safe value privacy = %q, %+v, %v", cleaned, summaries, err)
	}
	mutations := []struct {
		name   string
		mutate func(*ActionCoverage)
	}{
		{name: "class", mutate: func(value *ActionCoverage) { value.Observed.Classes = []CaseKind{} }},
		{name: "tool", mutate: func(value *ActionCoverage) { value.Observed.Tools = []string{} }},
		{name: "phase", mutate: func(value *ActionCoverage) { value.Observed.Phases = []action.Phase{} }},
		{name: "decision", mutate: func(value *ActionCoverage) { value.Observed.Decisions = []action.Decision{} }},
		{name: "provenance", mutate: func(value *ActionCoverage) { value.Observed.Provenance = []action.Provenance{} }},
		{name: "outcome", mutate: func(value *ActionCoverage) { value.Observed.Outcomes = []action.PhaseOutcome{} }},
		{name: "approval", mutate: func(value *ActionCoverage) { value.Observed.Approvals = []action.ApprovalStatus{} }},
		{name: "approval transition", mutate: func(value *ActionCoverage) {
			value.Observed.ApprovalTransitions = []actionapproval.Status{actionapproval.StatusPending}
		}},
		{name: "complete", mutate: func(value *ActionCoverage) { value.Complete = !value.Complete }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			mutated := cloneCorpusForTest(t, corpus)
			mutation.mutate(&mutated.Completeness.Action)
			mutated.CorpusID = mustCorpusIdentity(t, mutated)
			if _, err := MarshalCorpus(mutated); err == nil || !strings.Contains(err.Error(), "completeness") {
				t.Fatalf("completeness mutation error = %v", err)
			}
		})
	}
}

func TestRequiredActionCoverageCannotBeCompleteWithoutActionCases(t *testing.T) {
	repo, _ := makeActionImpactRepo(t, "")
	required := ActionDimensions{
		Classes: []CaseKind{CaseActionPre}, Tools: []string{"database-write"},
		Phases: []action.Phase{action.PhasePreCall}, Decisions: []action.Decision{action.DecisionAllow},
		Provenance:          []action.Provenance{action.ProvenanceHostObserved},
		Outcomes:            []action.PhaseOutcome{action.OutcomeDispatchEligible},
		Approvals:           []action.ApprovalStatus{action.ApprovalNone},
		ApprovalTransitions: []actionapproval.Status{},
	}
	corpus, err := NewCorpusWithActionCoverage(repo,
		[]Case{NewRepositoryCase("repository-only", runtime.Empty())}, AllEventClasses(), required)
	if err != nil {
		t.Fatal(err)
	}
	if corpus.Completeness.CompleteReplay || corpus.Completeness.Action.Complete ||
		actionDimensionsEmpty(corpus.Completeness.Action.Missing) {
		t.Fatalf("missing required action coverage was marked complete: %+v", corpus.Completeness)
	}
}

func TestDecodedActionCoverageCannotBypassConstructorValidation(t *testing.T) {
	repo, _ := makeActionImpactRepo(t, "")
	fixture := newActionFixture("coverage-decode", CaseActionPre, `{"target":"staging"}`,
		actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
			action.CacheEligible, action.OutcomeDispatchEligible, ""))
	corpus, err := NewCorpus(repo, []Case{fixture}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*ActionDimensions)
	}{
		{name: "invalid tool", mutate: func(value *ActionDimensions) { value.Tools = []string{"not safe!"} }},
		{name: "invalid class", mutate: func(value *ActionDimensions) { value.Classes = []CaseKind{"unknown"} }},
		{name: "duplicate phase", mutate: func(value *ActionDimensions) {
			value.Phases = []action.Phase{action.PhasePreCall, action.PhasePreCall}
		}},
		{name: "invalid approval", mutate: func(value *ActionDimensions) {
			value.Approvals = []action.ApprovalStatus{"invalid"}
		}},
		{name: "invalid approval transition", mutate: func(value *ActionDimensions) {
			value.ApprovalTransitions = []actionapproval.Status{"invalid"}
		}},
		{name: "tool bound", mutate: func(value *ActionDimensions) {
			value.Tools = make([]string, action.MaxTools+1)
			for index := range value.Tools {
				value.Tools[index] = "tool-" + fixedDecimal(index, 3)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := cloneCorpusForTest(t, corpus)
			test.mutate(&mutated.Completeness.Action.Required)
			required := mutated.Completeness.Action.Required
			mutated.Completeness = buildCompleteness(
				mutated.Cases, mutated.Completeness.CompleteEventClasses,
				required,
			)
			mutated.Completeness.Action.Required = required
			mutated.CorpusID = mustCorpusIdentity(t, mutated)
			if _, err := MarshalCorpus(mutated); err == nil ||
				!strings.Contains(err.Error(), "action coverage") {
				t.Fatalf("decoded coverage error = %v", err)
			}
		})
	}
}

func TestActionAssertionAndDeltaMutationsCannotPass(t *testing.T) {
	repo, evaluator := makeActionImpactRepo(t, "")
	mutations := []struct {
		name   string
		mutate func(*ActionAssertion)
	}{
		{name: "decision", mutate: func(value *ActionAssertion) { value.Decision = action.DecisionWarn }},
		{name: "reason", mutate: func(value *ActionAssertion) { value.Reason = action.ReasonRuleMatched }},
		{name: "tool", mutate: func(value *ActionAssertion) { value.ToolID = "" }},
		{name: "rules", mutate: func(value *ActionAssertion) { value.MatchedRuleIDs = []string{"unexpected"} }},
		{name: "cache", mutate: func(value *ActionAssertion) { value.Cache = ActionCacheAssertion{Reason: action.CachePolicyNever} }},
		{name: "request completeness", mutate: func(value *ActionAssertion) {
			setMissingAssertionEvidence(value, action.EvidenceRequest)
		}},
		{name: "policy completeness", mutate: func(value *ActionAssertion) {
			setMissingAssertionEvidence(value, action.EvidencePolicy)
		}},
		{name: "identity completeness", mutate: func(value *ActionAssertion) {
			setMissingAssertionEvidence(value, action.EvidenceIdentity)
		}},
		{name: "context completeness", mutate: func(value *ActionAssertion) {
			setMissingAssertionEvidence(value, action.EvidenceContext)
		}},
		{name: "state completeness", mutate: func(value *ActionAssertion) {
			setMissingAssertionEvidence(value, action.EvidenceState)
		}},
		{name: "phase completeness", mutate: func(value *ActionAssertion) {
			setMissingAssertionEvidence(value, action.EvidencePhase)
		}},
		{name: "phase outcome", mutate: func(value *ActionAssertion) {
			value.Decision, value.PhaseOutcome = action.DecisionBlock, action.OutcomeDispatchBlocked
		}},
		{name: "failure", mutate: func(value *ActionAssertion) {
			value.Reason, value.FailureCode = action.ReasonInvalidRequest, action.ReasonInvalidRequest
		}},
		{name: "omitted decision", mutate: func(value *ActionAssertion) { value.Decision = "" }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			fixture := newActionFixture("assertion-mutation", CaseActionPre, `{"target":"staging"}`,
				actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil, action.CacheEligible, action.OutcomeDispatchEligible, ""))
			mutation.mutate(&fixture.Action.Expected)
			corpus, err := NewCorpus(repo, []Case{fixture}, AllEventClasses())
			if err == nil {
				_, err = Compare(repo, corpus, candidateFromEvaluator(t, evaluator), evaluator, evaluator)
			}
			if err == nil {
				t.Fatal("mutated exact assertion passed")
			}
		})
	}

	base := ActionObservation{
		Outcome: actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
			action.CacheEligible, action.OutcomeDispatchEligible, ""),
		Trace: []action.TraceEntry{}, TraceComplete: true,
	}
	deltaTests := []struct {
		name   string
		mutate func(*ActionObservation)
		want   []ActionDeltaKind
	}{
		{name: "newly allowed", mutate: func(value *ActionObservation) {}, want: []ActionDeltaKind{DeltaDecision, DeltaNewlyAllowed}},
		{name: "newly warned", mutate: func(value *ActionObservation) { value.Outcome.Decision = action.DecisionWarn }, want: []ActionDeltaKind{DeltaDecision, DeltaNewlyWarned}},
		{name: "blocked to warned", mutate: func(value *ActionObservation) {
			value.Outcome.Decision = action.DecisionWarn
		}, want: []ActionDeltaKind{DeltaDecision, DeltaNewlyAllowed, DeltaNewlyWarned, DeltaPhaseOutcome}},
		{name: "blocked to approval", mutate: func(value *ActionObservation) {
			value.Outcome.Decision, value.Outcome.PhaseOutcome = action.DecisionRequireApproval, action.OutcomeDispatchBlocked
		}, want: []ActionDeltaKind{DeltaDecision, DeltaNewlyAllowed, DeltaNewlyApprovalRequired}},
		{name: "approval to blocked", mutate: func(value *ActionObservation) {
			value.Outcome.Decision, value.Outcome.PhaseOutcome = action.DecisionBlock, action.OutcomeDispatchBlocked
		}, want: []ActionDeltaKind{DeltaDecision, DeltaNewlyBlocked}},
		{name: "approval", mutate: func(value *ActionObservation) {
			value.Outcome.Decision, value.Outcome.PhaseOutcome = action.DecisionRequireApproval, action.OutcomeDispatchBlocked
		}, want: []ActionDeltaKind{DeltaDecision, DeltaNewlyApprovalRequired, DeltaNewlyBlocked, DeltaPhaseOutcome}},
		{name: "blocked", mutate: func(value *ActionObservation) {
			value.Outcome.Decision, value.Outcome.PhaseOutcome = action.DecisionBlock, action.OutcomeDispatchBlocked
		}, want: []ActionDeltaKind{DeltaDecision, DeltaNewlyBlocked, DeltaPhaseOutcome}},
		{name: "reason", mutate: func(value *ActionObservation) { value.Outcome.Reason = action.ReasonRuleMatched }, want: []ActionDeltaKind{DeltaReason}},
		{name: "tool", mutate: func(value *ActionObservation) { value.Outcome.ToolID = "other-tool" }, want: []ActionDeltaKind{DeltaToolIdentity}},
		{name: "failure", mutate: func(value *ActionObservation) {
			value.Outcome.Reason, value.Outcome.FailureCode = action.ReasonInvalidRequest, action.ReasonInvalidRequest
		}, want: []ActionDeltaKind{DeltaReason, DeltaFailure}},
		{name: "trace", mutate: func(value *ActionObservation) { value.Trace = []action.TraceEntry{{RuleID: "trace"}} }, want: []ActionDeltaKind{DeltaRuleTrace}},
		{name: "cache", mutate: func(value *ActionObservation) {
			value.Outcome.Cache = ActionCacheAssertion{Reason: action.CachePolicyNever}
		}, want: []ActionDeltaKind{DeltaCache}},
		{name: "phase", mutate: func(value *ActionObservation) { value.Outcome.PhaseOutcome = action.OutcomeDispatchBlocked }, want: []ActionDeltaKind{DeltaPhaseOutcome}},
		{name: "completeness", mutate: func(value *ActionObservation) {
			value.Outcome.Completeness.ContextComplete = false
			value.Outcome.Completeness.Missing = []action.MissingEvidence{{Field: action.EvidenceContext, Reason: action.ReasonContextUntrusted}}
		}, want: []ActionDeltaKind{DeltaCompleteness}},
	}
	for _, test := range deltaTests {
		t.Run("delta "+test.name, func(t *testing.T) {
			current, candidate := cloneActionObservation(base), cloneActionObservation(base)
			if test.name == "newly allowed" {
				current.Outcome.Decision = action.DecisionWarn
			} else if test.name == "blocked to warned" {
				current.Outcome.Decision, current.Outcome.PhaseOutcome = action.DecisionBlock, action.OutcomeDispatchBlocked
				test.mutate(&candidate)
			} else if test.name == "blocked to approval" {
				current.Outcome.Decision, current.Outcome.PhaseOutcome = action.DecisionBlock, action.OutcomeDispatchBlocked
				test.mutate(&candidate)
			} else if test.name == "approval to blocked" {
				current.Outcome.Decision, current.Outcome.PhaseOutcome = action.DecisionRequireApproval, action.OutcomeDispatchBlocked
				test.mutate(&candidate)
			} else {
				test.mutate(&candidate)
			}
			if got := actionDeltas(current, candidate); !slicesEqualActionDelta(got, test.want) {
				t.Fatalf("deltas = %v, want %v", got, test.want)
			}
		})
	}
}

func TestActionSelectedValueSummaryFieldsAreIdentityBound(t *testing.T) {
	repo, _ := makeActionImpactRepo(t, "")
	fixture := newActionFixture("private-summary", CaseActionPre,
		`{"authorization":"Bearer sk-secretvalue123","target":"staging"}`,
		actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
			action.CacheEligible, action.OutcomeDispatchEligible, ""))
	corpus, err := NewCorpus(repo, []Case{fixture}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name   string
		mutate func(*ActionCase)
	}{
		{name: "source", mutate: func(value *ActionCase) { value.SelectedValues[0].Source = action.SourceResult }},
		{name: "pointer", mutate: func(value *ActionCase) { value.SelectedValues[0].Pointer = "/target" }},
		{name: "category", mutate: func(value *ActionCase) { value.SelectedValues[0].Category = "physical-path" }},
		{name: "byte length", mutate: func(value *ActionCase) { value.SelectedValues[0].ByteLength++ }},
		{name: "item count", mutate: func(value *ActionCase) { value.SelectedValues[0].ItemCount++ }},
		{name: "provenance", mutate: func(value *ActionCase) {
			value.SelectedValues[0].Provenance = action.ProvenanceHostObserved
		}},
		{name: "identity", mutate: func(value *ActionCase) { value.SelectedValues[0].Identity = driftSHAIdentity }},
		{name: "redaction count", mutate: func(value *ActionCase) { value.RedactionCount++ }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			mutated := cloneCorpusForTest(t, corpus)
			mutation.mutate(mutated.Cases[0].Action)
			if _, err := MarshalCorpus(mutated); err == nil || !strings.Contains(err.Error(), "identity") {
				t.Fatalf("selected-value mutation error = %v", err)
			}
		})
	}
}

func TestLegacyCorpusMigratesOnlyAfterExactV1Validation(t *testing.T) {
	repo := makeImpactRepo(t)
	inputs := runtime.Empty()
	inputs.WritePaths = []string{"src/main.go"}
	repository, err := sanitizeRepositoryCase(repo, "legacy", RepositoryCase{Inputs: inputs})
	if err != nil {
		t.Fatal(err)
	}
	legacy := legacyCorpus{
		FormatVersion: LegacyCorpusFormatVersion,
		Completeness: legacyCompleteness{
			ObservedEventClasses: []EventClass{EventClassWrite}, CompleteEventClasses: AllEventClasses(),
			MissingEventClasses: []EventClass{}, RedactedEventClasses: []EventClass{}, CompleteReplay: true,
		},
		Cases: []legacyCase{{
			ID: "legacy", Inputs: repository.Repository.Inputs,
			RedactedEventClasses: []EventClass{},
		}},
	}
	legacy.CorpusID = mustLegacyCorpusIdentity(t, legacy)
	body, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := DecodeCorpus(body)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.FormatVersion != CorpusFormatVersion || migrated.Cases[0].Kind != CaseRepository ||
		migrated.CorpusID == legacy.CorpusID {
		t.Fatalf("migrated corpus = %+v", migrated)
	}
	mutated := bytes.Replace(body, []byte(`"legacy"`), []byte(`"changed"`), 1)
	if _, err := MigrateCorpusV1(mutated); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("mutated legacy error = %v", err)
	}
	wrongCase := bytes.Replace(body, []byte(`"format_version"`), []byte(`"FORMAT_VERSION"`), 1)
	if _, err := MigrateCorpusV1(wrongCase); err == nil || !strings.Contains(err.Error(), "incorrectly cased") {
		t.Fatalf("incorrectly cased legacy field error = %v", err)
	}
	invalidUTF8 := append([]byte(`{"format_version":"reconc-impact-corpus/v1","corpus_id":"`), 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`"}`)...)
	if _, err := MigrateCorpusV1(invalidUTF8); err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("legacy invalid UTF-8 error = %v", err)
	}
}

func TestCorpusAndManifestRejectIncorrectlyCasedJSONFields(t *testing.T) {
	repo, _ := makeActionImpactRepo(t, "")
	fixture := newActionFixture("strict-json-fields", CaseActionPre, `{"target":"staging"}`,
		actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
			action.CacheEligible, action.OutcomeDispatchEligible, ""))
	fixture.Action.Request.Context[0].Value = json.RawMessage(`{"MixedCasePayloadKey":true}`)
	corpus, err := NewCorpus(repo, []Case{fixture}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	body, err := MarshalCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCorpus(body); err != nil {
		t.Fatalf("arbitrary raw payload fields must remain valid: %v", err)
	}
	for _, mutation := range [][]byte{
		bytes.Replace(body, []byte(`"format_version"`), []byte(`"FORMAT_VERSION"`), 1),
		bytes.Replace(body, []byte(`"tool_id"`), []byte(`"TOOL_ID"`), 1),
		bytes.Replace(body, []byte(`"format_version":`), []byte(`"FORMAT_VERSION":"reconc-impact-corpus/v2","format_version":`), 1),
	} {
		if _, err := DecodeCorpus(mutation); err == nil || !strings.Contains(err.Error(), "incorrectly cased") {
			t.Fatalf("incorrectly cased corpus field error = %v", err)
		}
	}
	wrongScalar := bytes.Replace(body, []byte(`"tool_id": "database-write"`), []byte(`"tool_id": null`), 1)
	if _, err := DecodeCorpus(wrongScalar); err == nil || !strings.Contains(err.Error(), "invalid scalar type") {
		t.Fatalf("invalid corpus scalar error = %v", err)
	}

	current := actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
		action.CacheEligible, action.OutcomeDispatchEligible, "")
	candidate := actionAssertion(action.DecisionBlock, action.ReasonRuleMatched, "database-write", nil,
		action.CacheEligible, action.OutcomeDispatchBlocked, "")
	manifest, err := NewDeltaManifest([]ReviewedActionDelta{{
		CaseID: "strict-json-fields", CaseIdentity: driftSHAIdentity, Delta: DeltaNewlyBlocked,
		CandidateLockDigest: driftDigest, Current: current, Candidate: candidate,
		Rationale: "reviewed strict field contract", Permanent: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	manifestBody, err := MarshalDeltaManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestBody = bytes.Replace(manifestBody, []byte(`"case_id"`), []byte(`"CASE_ID"`), 1)
	if _, err := DecodeDeltaManifest(manifestBody); err == nil || !strings.Contains(err.Error(), "incorrectly cased") {
		t.Fatalf("incorrectly cased manifest field error = %v", err)
	}
	manifestBody, err = MarshalDeltaManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestBody = bytes.Replace(manifestBody, []byte(`"permanent": true`), []byte(`"permanent": "true"`), 1)
	if _, err := DecodeDeltaManifest(manifestBody); err == nil || !strings.Contains(err.Error(), "invalid scalar type") {
		t.Fatalf("invalid manifest scalar error = %v", err)
	}
}

func TestPortableHarnessActionCorpusMatchesProductionContract(t *testing.T) {
	repo, evaluator := makeActionImpactRepo(t, "")
	fixture := newActionFixture("portable-database-staging", CaseActionPre, `{"target":"staging"}`,
		actionAssertion(action.DecisionAllow, action.ReasonDeclaredTool, "database-write", nil,
			action.CacheEligible, action.OutcomeDispatchEligible, ""))
	corpus, err := NewCorpus(repo, []Case{fixture}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	want, err := MarshalCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join("..", "..", "harness", "template", "audits", "testdata", "action-impact", "corpus.json")
	got, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read portable action corpus: %v\nexpected fixture:\n%s", err, want)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("portable action corpus drifted\ngot:\n%s\nwant:\n%s", got, want)
	}
	decoded, err := DecodeCorpus(got)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compare(repo, decoded, candidateFromEvaluator(t, evaluator), evaluator, evaluator); err != nil {
		t.Fatal(err)
	}
}

func newActionFixture(id string, kind CaseKind, payload string, expected ActionAssertion) Case {
	phase := action.PhasePreCall
	request := ActionRequestFixture{
		FormatVersion: action.RequestFormatVersion, CallID: "act_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Transport: action.TransportMCPStdio, ServerLabel: "database",
		ServerFingerprint: fixtureServerIdentity, Tool: "execute",
		ToolContractDigest: fixtureToolDigest, Phase: phase,
		RepositoryIdentity: fixtureRepoIdentity, AuthorityMode: action.AuthorityOperatorPinned,
		Context: []action.RawContextValue{{
			Name: "environment", Value: json.RawMessage(`"test"`),
			Provenance: action.ProvenanceHostObserved, Available: true,
		}},
		Completeness: action.CompleteEvidence(), Deadline: action.DeadlineReady,
		StateVersion: "state-v1",
	}
	if kind == CaseActionPost {
		request.Phase, request.Result = action.PhasePostResult, ActionPayload(payload)
	} else {
		request.Arguments = ActionPayload(payload)
	}
	return Case{ID: id, Kind: kind, Action: &ActionCase{
		ToolID: "database-write", Request: request,
		State: ActionStateFixture{
			ContextIdentity: "context-v1", ExecutableDigest: fixtureExecutable, Principal: "operator",
			CredentialLabels: []string{"database-writer"},
			Approval:         action.ApprovalSnapshot{Status: action.ApprovalNone, Identity: "approval-none"},
			Taint:            action.TaintSnapshot{Status: action.TaintClean, Identity: "taint-clean"},
			Lifecycle:        action.LifecycleActive, CachePolicyVersion: action.CacheIdentityVersion,
			Budget: action.BudgetSnapshot{
				StateVersion: "state-v1", Identity: "absent",
				ReservationIdentity: "absent", Complete: true, Candidates: []action.BudgetCandidate{},
			},
			ResampleDrift: []ActionIdentityComponent{},
		},
		Expected: expected, SelectedValues: []ActionValueSummary{},
	}}
}

func budgetActionFixture(
	compiled runtime.CompiledActionRuntime,
	id string,
	consumed action.BudgetUsage,
	reserved action.BudgetUsage,
	reservationApplied bool,
	available bool,
	expected ActionAssertion,
) Case {
	fixture := newActionFixture(id, CaseActionPre, `{"target":"staging"}`, expected)
	fixture.Action.Request.ServerFingerprint = budgetServerIdentity
	fixture.Action.Request.RepositoryIdentity = budgetRepoIdentity
	fixture.Action.Request.StateVersion = budgetStateIdentity
	fixture.Action.State.Budget = action.BudgetSnapshot{
		StateVersion: budgetStateIdentity, Identity: budgetSnapshotID,
		ReservationIdentity: budgetReservationID, Complete: true,
		Candidates: []action.BudgetCandidate{{
			BudgetID: "database-window", ScopeIdentity: budgetScopeID,
			LineageIdentity: budgetLineageID,
			Scope: action.BudgetScope{
				RepositoryIdentity: budgetRepoIdentity, Principal: "operator",
				CredentialLabels: []string{"database-writer"}, ServerLabel: "database",
				ServerIdentity: budgetServerIdentity, ToolID: "database-write",
				RunIdentity: "absent", SessionIdentity: "absent", WindowIdentity: budgetWindowID,
				WindowStartUnix: 120,
			},
			Reset: action.BudgetResetFixedWindow, WindowSeconds: 60,
			Limits:   action.BudgetLimits{CallCount: 2, RateWindow: 1},
			Consumed: consumed, Reserved: reserved,
			Required:           action.BudgetUsage{CallCount: 1, RateWindow: 1},
			ReservationApplied: reservationApplied, Available: available,
			Generation: action.BudgetGeneration{
				PolicyDigest: compiled.SourceDigest, ExecutableDigest: fixtureExecutable,
				ToolContractDigest: fixtureToolDigest, KeyID: budgetKeyID,
			},
		}},
	}
	if !available {
		fixture.Action.State.Budget.ReservationIdentity = "absent"
		fixture.Action.State.Budget.Candidates[0].Reason = action.ReasonBudgetExhausted
	}
	return fixture
}

func actionAssertion(decision action.Decision, reason action.ReasonCode, toolID string, rules []string, cacheReason action.CacheReason, outcome action.PhaseOutcome, failure action.ReasonCode) ActionAssertion {
	if rules == nil {
		rules = []string{}
	}
	return ActionAssertion{
		Decision: decision, Reason: reason, ToolID: toolID,
		MatchedRuleIDs: rules,
		Cache:          ActionCacheAssertion{Eligible: cacheReason == action.CacheEligible, Reason: cacheReason},
		Completeness:   action.CompleteEvidence(), PhaseOutcome: outcome, FailureCode: failure,
	}
}

func withIdentityDrift(input Case, component ActionIdentityComponent) Case {
	input.Action.State.ResampleDrift = []ActionIdentityComponent{component}
	return input
}

func withIncompleteContext(input Case) Case {
	incomplete := action.CompleteEvidence()
	incomplete.ContextComplete = false
	incomplete.Missing = []action.MissingEvidence{{Field: action.EvidenceContext, Reason: action.ReasonContextUntrusted}}
	input.Action.Request.Completeness = incomplete
	input.Action.Expected.Completeness = incomplete
	return input
}

func setMissingAssertionEvidence(assertion *ActionAssertion, field action.EvidenceField) {
	switch field {
	case action.EvidenceRequest:
		assertion.Completeness.RequestComplete = false
	case action.EvidencePolicy:
		assertion.Completeness.PolicyComplete = false
	case action.EvidenceIdentity:
		assertion.Completeness.IdentityComplete = false
	case action.EvidenceContext:
		assertion.Completeness.ContextComplete = false
	case action.EvidenceState:
		assertion.Completeness.StateComplete = false
	case action.EvidencePhase:
		assertion.Completeness.PhaseComplete = false
	}
	assertion.Completeness.Missing = []action.MissingEvidence{{
		Field: field, Reason: action.ReasonContextUntrusted,
	}}
}

func withExtraneousArguments(input Case) Case {
	input.Action.Request.Arguments = ActionPayload(`{}`)
	return input
}

func withExtraneousResult(input Case) Case {
	input.Action.Request.Result = ActionPayload(`{}`)
	return input
}

func cloneCorpusForTest(t *testing.T, input Corpus) Corpus {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output Corpus
	if err := json.Unmarshal(body, &output); err != nil {
		t.Fatal(err)
	}
	return output
}

func mustCorpusIdentity(t testing.TB, corpus Corpus) string {
	t.Helper()
	identity, err := corpusIdentity(corpus)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func mustLegacyCorpusIdentity(t testing.TB, corpus legacyCorpus) string {
	t.Helper()
	identity, err := legacyCorpusIdentity(corpus)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func cloneActionObservation(input ActionObservation) ActionObservation {
	output := input
	output.Outcome.MatchedRuleIDs = append([]string{}, input.Outcome.MatchedRuleIDs...)
	output.Outcome.Completeness.Missing = append([]action.MissingEvidence{}, input.Outcome.Completeness.Missing...)
	if input.Outcome.Approval != nil {
		approval := *input.Outcome.Approval
		output.Outcome.Approval = &approval
	}
	output.Trace = append([]action.TraceEntry{}, input.Trace...)
	return output
}

func exactApprovalEvaluation(
	t testing.TB,
	scenario ActionCase,
	compiled runtime.CompiledActionRuntime,
) (action.EvaluationResult, string) {
	t.Helper()
	raw := actionRawRequest(scenario.Request, compiled.SourceDigest, compiled.LockDigest)
	request, err := action.NormalizeRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	input := action.EvaluationInput{
		Request: request, SourceIdentity: compiled.SourceDigest,
		ContextIdentity: scenario.State.ContextIdentity, ExecutableDigest: scenario.State.ExecutableDigest,
		Principal: scenario.State.Principal, CredentialLabels: append([]string(nil), scenario.State.CredentialLabels...),
		Budget: scenario.State.Budget, Approval: scenario.State.Approval, Taint: scenario.State.Taint,
		Lifecycle: scenario.State.Lifecycle, CachePolicyVersion: scenario.State.CachePolicyVersion,
	}
	input.ResampledIdentities = compiled.Evaluator.IdentitySnapshot(input)
	result := compiled.Evaluator.EvaluateRaw(raw, input)
	binding := struct {
		CallID       string          `json:"call_id"`
		Request      string          `json:"request"`
		Plan         string          `json:"plan"`
		Source       string          `json:"source"`
		Rules        []string        `json:"rules"`
		Decision     action.Decision `json:"decision"`
		StateVersion string          `json:"state_version"`
	}{
		CallID: input.Request.CallID, Request: result.Cache.Identity,
		Plan: result.PlanIdentity, Source: result.SourceIdentity,
		Rules: result.MatchedRuleIDs, Decision: result.Decision,
		StateVersion: input.Request.StateVersion,
	}
	body, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	return result, "sha256:" + hex.EncodeToString(digest[:])
}

func makeActionImpactRepo(t *testing.T, rules string) (string, *runtime.CompiledPolicyEvaluator) {
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
  rules:
` + rules + "\nrules: []\n"
	if rules == "" {
		body = strings.Replace(body, "  rules:\n\nrules", "  rules: []\nrules", 1)
	}
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile action repo:\n%s\n%v", body, err)
	}
	evaluator, _, err := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(repo)
	if err != nil {
		t.Fatal(err)
	}
	return repo, evaluator
}

func makeBudgetActionImpactRepo(t *testing.T) (string, *runtime.CompiledPolicyEvaluator) {
	t.Helper()
	repo := t.TempDir()
	body := `actions:
  tools:
    - id: database-write
      transport: mcp_stdio
      server_label: database
      server_fingerprint: ` + budgetServerIdentity + `
      tool: execute
      effect:
        kind: external
  rules: []
  budgets:
    - id: database-window
      selector:
        tool_ids: [database-write]
      limits:
        call_count: 2
        rate_window: 1
      reset: fixed_window
      window_seconds: 60
      on_exhaustion: block
rules: []
`
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile budget action repo:\n%s\n%v", body, err)
	}
	evaluator, _, err := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(repo)
	if err != nil {
		t.Fatal(err)
	}
	return repo, evaluator
}

func candidateFromEvaluator(t *testing.T, evaluator *runtime.CompiledPolicyEvaluator) Candidate {
	t.Helper()
	compiled, err := evaluator.ActionRuntime()
	if err != nil {
		t.Fatal(err)
	}
	return Candidate{
		Kind: "policy_file", Name: "candidate", SourceDigest: compiled.SourceDigest,
		LockDigest: compiled.LockDigest, ActionPlanIdentity: compiled.Evaluator.PlanIdentity(),
		ActionToolCount: compiled.ToolCount, ActionRuleCount: compiled.ActionRuleCount,
	}
}

func reviewedEntry(report Report, caseIndex int, delta ActionDeltaKind) ReviewedActionDelta {
	comparison := report.Cases[caseIndex]
	return ReviewedActionDelta{
		CaseID: comparison.ID, CaseIdentity: comparison.CaseIdentity, Delta: delta,
		CandidateLockDigest: report.Candidate.LockDigest,
		Current:             comparison.Action.Current.Outcome, Candidate: comparison.Action.Candidate.Outcome,
		Rationale: "reviewed staging block", Permanent: true,
	}
}

func slicesEqualActionDelta(left, right []ActionDeltaKind) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

const baseActionPolicyRules = `
    - id: approve-sensitive
      selector:
        tool_ids: [database-write]
        phases: [pre_call]
      when:
        predicate:
          source: arguments
          pointer: /target
          op: eq
          value: sensitive
      decision: require_approval
      message: Approval required.
    - id: block-production
      selector:
        tool_ids: [database-write]
        phases: [pre_call]
      when:
        predicate:
          source: arguments
          pointer: /target
          op: eq
          value: production
      decision: block
      message: Production blocked.
    - id: warn-bulk-delete
      selector:
        tool_ids: [database-write]
        phases: [pre_call]
      when:
        predicate:
          source: arguments
          pointer: /operation
          op: eq
          value: bulk-delete
      decision: warn
      message: Bulk delete warning.
    - id: withhold-error-result
      selector:
        tool_ids: [database-write]
        phases: [post_result]
      when:
        predicate:
          source: result
          pointer: /status
          op: eq
          value: error
      decision: block
      message: Error result withheld.
`

const trustedContextActionPolicyRule = `
    - id: allow-trusted-context
      selector:
        tool_ids: [database-write]
        phases: [pre_call]
      when:
        predicate:
          source: context
          pointer: /environment
          op: eq
          value: test
          minimum_provenance: host_observed
      decision: allow
      message: Trusted context allows the action.
`
