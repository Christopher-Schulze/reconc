package action

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestDecisionCacheRequiresEveryExactIdentityComponent(t *testing.T) {
	t.Parallel()
	evaluator, baseline := testActionEvaluator(t, nil, Defaults{}, testExternalEffect())
	result := evaluator.Evaluate(baseline)
	if !result.Cache.Eligible {
		t.Fatalf("baseline cache = %#v", result.Cache)
	}
	cache := NewDecisionCache()
	if !cache.Store(evaluator, baseline, result) {
		t.Fatal("eligible result was not stored")
	}
	tests := []struct {
		name   string
		mutate func(*EvaluationInput)
	}{
		{
			name: "canonical request",
			mutate: func(input *EvaluationInput) {
				value := mustTestValue(t, `{"changed":true}`)
				input.Request.Arguments = &value
			},
		},
		{name: "tool", mutate: func(input *EvaluationInput) { input.Request.Tool = "other" }},
		{
			name: "transport",
			mutate: func(input *EvaluationInput) {
				input.Request.Transport = TransportHostMCP
				input.Request.Platform = PlatformClaudeCode
				input.Request.ServerLabel = ""
				value := "sha256:" + strings.Repeat("7", 64)
				input.Request.ServerFingerprint = value
				input.ResampledIdentities.ServerLabel = ""
				input.ResampledIdentities.ServerFingerprint = value
			},
		},
		{
			name: "server label",
			mutate: func(input *EvaluationInput) {
				input.Request.ServerLabel = "database-alt"
				input.ResampledIdentities.ServerLabel = "database-alt"
			},
		},
		{
			name: "server fingerprint",
			mutate: func(input *EvaluationInput) {
				value := "hmac-sha256:v1:key1:" + strings.Repeat("7", 64)
				input.Request.ServerFingerprint = value
				input.ResampledIdentities.ServerFingerprint = value
			},
		},
		{
			name: "tool contract",
			mutate: func(input *EvaluationInput) {
				value := "sha256:" + strings.Repeat("8", 64)
				input.Request.ToolContractDigest = value
				input.ResampledIdentities.ToolContractDigest = value
			},
		},
		{
			name: "source identity",
			mutate: func(input *EvaluationInput) {
				value := strings.Repeat("7", 64)
				input.SourceIdentity = value
				input.ResampledIdentities.SourceIdentity = value
			},
		},
		{
			name: "policy digest",
			mutate: func(input *EvaluationInput) {
				value := strings.Repeat("8", 64)
				input.Request.PolicyDigest = value
				input.ResampledIdentities.PolicyDigest = value
			},
		},
		{
			name: "lock digest",
			mutate: func(input *EvaluationInput) {
				value := strings.Repeat("9", 64)
				input.Request.LockDigest = value
				input.ResampledIdentities.LockDigest = value
			},
		},
		{
			name: "context value",
			mutate: func(input *EvaluationInput) {
				input.Request.Context = []ContextValue{{
					Name: "role", Value: testStringValue(t, "operator"),
					Provenance: ProvenanceHostObserved, Available: true,
				}}
			},
		},
		{
			name: "context provenance",
			mutate: func(input *EvaluationInput) {
				input.Request.Context = []ContextValue{{
					Name: "role", Value: testStringValue(t, "operator"),
					Provenance: ProvenanceAdapterAsserted, Available: true,
				}}
			},
		},
		{
			name: "context identity",
			mutate: func(input *EvaluationInput) {
				input.ContextIdentity = "context-v2"
				input.ResampledIdentities.ContextIdentity = "context-v2"
			},
		},
		{
			name: "principal",
			mutate: func(input *EvaluationInput) {
				input.Principal = "reviewer"
				input.ResampledIdentities.Principal = "reviewer"
			},
		},
		{
			name: "credential labels",
			mutate: func(input *EvaluationInput) {
				input.CredentialLabels = []string{"database-reader"}
				input.ResampledIdentities.CredentialLabels = []string{"database-reader"}
			},
		},
		{
			name: "repository identity",
			mutate: func(input *EvaluationInput) {
				value := "hmac-sha256:v1:key2:" + strings.Repeat("8", 64)
				input.Request.RepositoryIdentity = value
				input.ResampledIdentities.RepositoryIdentity = value
			},
		},
		{
			name: "state version",
			mutate: func(input *EvaluationInput) {
				input.Request.StateVersion = "state-v2"
				input.ResampledIdentities.StateVersion = "state-v2"
			},
		},
		{
			name: "approval state",
			mutate: func(input *EvaluationInput) {
				input.Approval.Identity = "approval-other"
				input.ResampledIdentities.ApprovalIdentity = "approval-other"
			},
		},
		{
			name: "approval status",
			mutate: func(input *EvaluationInput) {
				input.Approval.Status = ApprovalCurrentUnconsumed
			},
		},
		{
			name: "taint identity",
			mutate: func(input *EvaluationInput) {
				input.Taint.Identity = "taint-other"
				input.ResampledIdentities.TaintIdentity = "taint-other"
			},
		},
		{
			name: "authority mode",
			mutate: func(input *EvaluationInput) {
				input.Request.AuthorityMode = AuthorityRepositoryManaged
				input.ResampledIdentities.AuthorityMode = AuthorityRepositoryManaged
			},
		},
		{
			name: "phase",
			mutate: func(input *EvaluationInput) {
				value := mustTestValue(t, `{"ok":true}`)
				input.Request.Phase = PhasePostResult
				input.Request.Arguments = nil
				input.Request.Result = &value
			},
		},
		{
			name: "cache version",
			mutate: func(input *EvaluationInput) {
				input.CachePolicyVersion = "action-decision-v2"
			},
		},
		{
			name: "completeness",
			mutate: func(input *EvaluationInput) {
				input.Request.Completeness.ContextComplete = false
				input.Request.Completeness.Missing = []MissingEvidence{{
					Field: EvidenceContext, Reason: ReasonContextUntrusted,
				}}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := baseline
			test.mutate(&input)
			if _, hit, _ := cache.Lookup(evaluator, input); hit {
				t.Fatal("cache reused a decision after identity mutation")
			}
		})
	}
}

func TestDecisionCacheBindsPlanIdentity(t *testing.T) {
	t.Parallel()
	first, input := testActionEvaluator(t, nil, Defaults{}, testExternalEffect())
	result := first.Evaluate(input)
	cache := NewDecisionCache()
	if !cache.Store(first, input, result) {
		t.Fatal("eligible result was not stored")
	}
	second, _ := testActionEvaluator(t, []Rule{{
		ID: "warning", Decision: DecisionWarn, SourceIdentity: ".reconc.yml",
	}}, Defaults{}, testExternalEffect())
	refreshTestIdentities(second, &input)
	if _, hit, _ := cache.Lookup(second, input); hit {
		t.Fatal("cache reused a decision under a different compiled plan")
	}
}

func TestDecisionCacheRejectsInactiveLifecycleBeforeLookup(t *testing.T) {
	t.Parallel()
	evaluator, input := testActionEvaluator(t, nil, Defaults{}, testExternalEffect())
	result := evaluator.Evaluate(input)
	cache := NewDecisionCache()
	if !cache.Store(evaluator, input, result) {
		t.Fatal("eligible result was not stored")
	}

	for _, lifecycle := range []LifecycleState{LifecycleCancelled, LifecycleShutdown} {
		candidate := input
		candidate.Lifecycle = lifecycle
		if _, hit, reason := cache.Lookup(evaluator, candidate); hit || reason != CacheLifecycleInactive {
			t.Fatalf("lifecycle %s lookup = hit %t, reason %s", lifecycle, hit, reason)
		}
	}
	candidate := input
	candidate.Request.Deadline = DeadlineExceeded
	if _, hit, reason := cache.Lookup(evaluator, candidate); hit || reason != CacheLifecycleInactive {
		t.Fatalf("expired lookup = hit %t, reason %s", hit, reason)
	}
}

func TestDecisionCacheBindsRepositoryEffectCandidate(t *testing.T) {
	t.Parallel()
	effect := Effect{Kind: EffectRepositoryWrite, PathFields: []string{"/path"}}
	evaluator, input := testActionEvaluator(t, nil, Defaults{}, effect)
	input.RepositoryEffect = &RepositoryEffectCandidate{
		Decision: DecisionWarn, Reason: ReasonRuleMatched,
		RuleIDs: []string{"repository-warning"}, Identity: "repository-effect-v1", Complete: true,
	}
	refreshTestIdentities(evaluator, &input)
	result := evaluator.Evaluate(input)
	cache := NewDecisionCache()
	if !cache.Store(evaluator, input, result) {
		t.Fatal("eligible repository result was not stored")
	}

	candidate := input
	changed := *input.RepositoryEffect
	changed.Decision = DecisionBlock
	changed.Reason = ReasonInspectionIncomplete
	changed.RuleIDs = []string{"repository-block"}
	candidate.RepositoryEffect = &changed
	if _, hit, _ := cache.Lookup(evaluator, candidate); hit {
		t.Fatal("cache reused a result after repository candidate mutation")
	}
}

func TestDecisionCacheExplicitNonCacheableReasons(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		rules  []Rule
		mutate func(*EvaluationInput)
		reason CacheReason
	}{
		{
			name: "rule never", rules: []Rule{{ID: "never", Decision: DecisionWarn, Cache: CacheNever, SourceIdentity: ".reconc.yml"}},
			reason: CacheRuleNever,
		},
		{
			name: "approval pending", rules: []Rule{{ID: "approve", Decision: DecisionRequireApproval, SourceIdentity: ".reconc.yml"}},
			reason: CacheApprovalPending,
		},
		{
			name: "tainted",
			mutate: func(input *EvaluationInput) {
				input.Taint.Status = TaintPresent
				input.Taint.Identity = "taint-present"
				input.ResampledIdentities.TaintIdentity = "taint-present"
			},
			reason: CacheEvidenceTainted,
		},
		{
			name: "unresolved context",
			mutate: func(input *EvaluationInput) {
				input.Request.Context = []ContextValue{{Name: "role", Provenance: ProvenanceHostObserved}}
			},
			reason: CacheContextUnresolved,
		},
		{
			name: "identity drift",
			mutate: func(input *EvaluationInput) {
				input.ResampledIdentities.PlanIdentity = "sha256:" + strings.Repeat("9", 64)
			},
			reason: CacheIdentityDrift,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			evaluator, input := testActionEvaluator(t, test.rules, Defaults{}, testExternalEffect())
			if test.mutate != nil {
				test.mutate(&input)
			}
			result := evaluator.Evaluate(input)
			if result.Cache.Eligible || result.Cache.Reason != test.reason {
				t.Fatalf("cache = %#v, want %s", result.Cache, test.reason)
			}
		})
	}
}

func TestDecisionCacheDefensiveCopyAndConcurrentAccess(t *testing.T) {
	evaluator, input := testActionEvaluator(t, []Rule{{
		ID: "warn", Decision: DecisionWarn, SourceIdentity: ".reconc.yml",
	}}, Defaults{}, testExternalEffect())
	result := evaluator.Evaluate(input)
	cache := NewDecisionCache()
	if !cache.Store(evaluator, input, result) {
		t.Fatal("store failed")
	}
	first, hit, _ := cache.Lookup(evaluator, input)
	if !hit {
		t.Fatal("cache miss")
	}
	first.MatchedRuleIDs[0] = "mutated"
	second, hit, _ := cache.Lookup(evaluator, input)
	if !hit || second.MatchedRuleIDs[0] != "warn" {
		t.Fatal("cache entry mutated through returned result")
	}

	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 100; iteration++ {
				cache.Store(evaluator, input, result)
				if _, ok, _ := cache.Lookup(evaluator, input); !ok {
					t.Errorf("concurrent cache miss")
					return
				}
			}
		}()
	}
	wait.Wait()
}

func TestDecisionCacheCurrentApprovalAndPrivacyBoundary(t *testing.T) {
	t.Parallel()
	rule := Rule{ID: "approve", Decision: DecisionRequireApproval, SourceIdentity: ".reconc.yml"}
	evaluator, input := testActionEvaluator(t, []Rule{rule}, Defaults{}, testExternalEffect())
	input.Approval = ApprovalSnapshot{Status: ApprovalCurrentUnconsumed, Identity: "approval-current"}
	refreshTestIdentities(evaluator, &input)
	result := evaluator.Evaluate(input)
	if !result.Cache.Eligible || result.RequiredApprovalIdentity == "" {
		t.Fatalf("current approval cache result = %#v", result)
	}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cache.Identity == "" || strings.Contains(string(body), result.Cache.Identity) {
		t.Fatal("internal cache identity is missing or crossed the result serialization boundary")
	}
}

func TestDecisionCacheNilAndEvictionBehavior(t *testing.T) {
	t.Parallel()
	evaluator, baseline := testActionEvaluator(t, nil, Defaults{}, testExternalEffect())
	result := evaluator.Evaluate(baseline)
	var nilCache *DecisionCache
	if nilCache.Store(evaluator, baseline, result) {
		t.Fatal("nil cache stored a result")
	}
	if _, hit, reason := nilCache.Lookup(evaluator, baseline); hit || reason != CacheEligible {
		t.Fatalf("nil cache lookup = hit %t, reason %s", hit, reason)
	}
	var nilEvaluator *Evaluator
	if identity := nilEvaluator.CacheIdentity(baseline); identity.Eligible || identity.Reason != CacheIdentityMissing {
		t.Fatalf("nil evaluator cache identity = %#v", identity)
	}

	cache := NewDecisionCache()
	inputs := make([]EvaluationInput, MaxDecisionCacheEntries+1)
	for index := range inputs {
		input := baseline
		arguments := mustTestValue(t, fmt.Sprintf(`{"index":%d}`, index))
		input.Request.Arguments = &arguments
		result := evaluator.Evaluate(input)
		if !cache.Store(evaluator, input, result) {
			t.Fatalf("store %d failed", index)
		}
		inputs[index] = input
	}
	if _, hit, _ := cache.Lookup(evaluator, inputs[0]); hit {
		t.Fatal("oldest cache entry survived the exact capacity bound")
	}
	if _, hit, _ := cache.Lookup(evaluator, inputs[len(inputs)-1]); !hit {
		t.Fatal("newest cache entry was evicted")
	}
}
