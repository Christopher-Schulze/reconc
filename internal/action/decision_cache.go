package action

import (
	"encoding/json"
	"sync"
)

type DecisionCache struct {
	mu      sync.Mutex
	entries map[string]EvaluationResult
	order   []string
}

type cacheBinding struct {
	Version                  string                     `json:"version"`
	Request                  string                     `json:"request"`
	Transport                Transport                  `json:"transport"`
	ServerLabel              string                     `json:"server_label"`
	ServerFingerprint        string                     `json:"server_fingerprint"`
	Tool                     string                     `json:"tool"`
	ToolContractDigest       string                     `json:"tool_contract_digest"`
	ExecutableDigest         string                     `json:"executable_digest"`
	Phase                    Phase                      `json:"phase"`
	PlanIdentity             string                     `json:"plan_identity"`
	SourceIdentity           string                     `json:"source_identity"`
	PolicyAuthority          AuthorityMode              `json:"policy_authority"`
	ContextIdentity          string                     `json:"context_identity"`
	Principal                string                     `json:"principal"`
	CredentialLabels         []string                   `json:"credential_labels"`
	RepositoryIdentity       string                     `json:"repository_identity"`
	StateVersion             string                     `json:"state_version"`
	Budget                   BudgetSnapshot             `json:"budget"`
	Approval                 ApprovalSnapshot           `json:"approval"`
	Taint                    TaintSnapshot              `json:"taint"`
	Lifecycle                LifecycleState             `json:"lifecycle"`
	Completeness             Completeness               `json:"completeness"`
	RepositoryEffect         *RepositoryEffectCandidate `json:"repository_effect,omitempty"`
	RepositoryEffectIdentity string                     `json:"repository_effect_identity"`
	Inspection               *InspectionEvidence        `json:"inspection,omitempty"`
	InspectionIdentity       string                     `json:"inspection_identity"`
	Resampled                IdentitySnapshot           `json:"resampled"`
}

func NewDecisionCache() *DecisionCache {
	return &DecisionCache{entries: make(map[string]EvaluationResult)}
}

func (c *DecisionCache) Lookup(
	evaluator *Evaluator,
	input EvaluationInput,
) (EvaluationResult, bool, CacheReason) {
	return c.lookupIdentity(evaluator.CacheIdentity(input))
}

// LookupPrepared performs no normalization or identity construction.
func (c *DecisionCache) LookupPrepared(prepared *PreparedEvaluation) (EvaluationResult, bool, CacheReason) {
	return c.lookupIdentity(preparedCacheIdentity(prepared))
}

func (c *DecisionCache) lookupIdentity(identity CacheResult) (EvaluationResult, bool, CacheReason) {
	if !identity.Eligible {
		return EvaluationResult{}, false, identity.Reason
	}
	if c == nil {
		return EvaluationResult{}, false, CacheEligible
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[identity.Identity]
	if !ok || !entry.Cache.Eligible || entry.Cache.Identity != identity.Identity {
		return EvaluationResult{}, false, CacheEligible
	}
	return cloneEvaluationResult(entry), true, CacheEligible
}

func (c *DecisionCache) Store(
	evaluator *Evaluator,
	input EvaluationInput,
	result EvaluationResult,
) bool {
	return c.storeIdentity(evaluator.CacheIdentity(input), result)
}

// StorePrepared verifies the result against the exact prepared eligible
// identity without repeating input normalization.
func (c *DecisionCache) StorePrepared(prepared *PreparedEvaluation, result EvaluationResult) bool {
	return c.storeIdentity(preparedCacheIdentity(prepared), result)
}

func (c *DecisionCache) storeIdentity(identity CacheResult, result EvaluationResult) bool {
	if c == nil || !identity.Eligible || !result.Cache.Eligible || result.Failure != nil ||
		identity.Identity != result.Cache.Identity {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]EvaluationResult)
	}
	if _, exists := c.entries[identity.Identity]; !exists {
		c.order = append(c.order, identity.Identity)
	}
	c.entries[identity.Identity] = cloneEvaluationResult(result)
	for len(c.order) > MaxDecisionCacheEntries {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
	return true
}

func preparedCacheIdentity(prepared *PreparedEvaluation) CacheResult {
	if prepared == nil {
		return CacheResult{Reason: CacheIdentityMissing}
	}
	return prepared.cache
}

func (e *Evaluator) CacheIdentity(input EvaluationInput) CacheResult {
	if e == nil {
		return CacheResult{Reason: CacheIdentityMissing}
	}
	normalized, err := e.normalizeEvaluationInput(input)
	if err != nil {
		return CacheResult{Reason: CacheIdentityMissing}
	}
	input = normalized
	requestIdentity, identityErr := requestDigestValidated(input.Request)
	if identityErr != nil {
		return CacheResult{Reason: CacheIdentityMissing}
	}
	expected := e.expectedIdentities(input)
	return e.cacheIdentityNormalized(input, requestIdentity, expected)
}

func (e *Evaluator) cacheIdentityNormalized(
	input EvaluationInput,
	requestIdentity string,
	expected IdentitySnapshot,
) CacheResult {
	if input.Lifecycle != LifecycleActive || input.Request.Deadline != DeadlineReady {
		return e.cacheIdentityWithReason(input, requestIdentity, expected, CacheLifecycleInactive)
	}
	if e.plan.Defaults.Cache == CacheNever {
		return e.cacheIdentityWithReason(input, requestIdentity, expected, CachePolicyNever)
	}
	if !input.Request.Completeness.Complete() {
		return e.cacheIdentityWithReason(input, requestIdentity, expected, CacheEvidenceIncomplete)
	}
	for _, entry := range input.Request.Context {
		if !entry.Available {
			return e.cacheIdentityWithReason(input, requestIdentity, expected, CacheContextUnresolved)
		}
	}
	if input.Taint.Status != TaintClean {
		return e.cacheIdentityWithReason(input, requestIdentity, expected, CacheEvidenceTainted)
	}
	if code := verifyResampledIdentitiesExpected(input.ResampledIdentities, expected); code != "" {
		reason := CacheIdentityDrift
		if code == ReasonStateUnavailable {
			reason = CacheStateStale
		}
		return e.cacheIdentityWithReason(input, requestIdentity, expected, reason)
	}
	return e.cacheIdentityWithReason(input, requestIdentity, expected, CacheEligible)
}

func finalizeCacheResult(
	base CacheResult,
	ruleNever bool,
	decision Decision,
	approval ApprovalSnapshot,
	completeness Completeness,
	failure bool,
) CacheResult {
	if failure {
		base.Eligible = false
		base.Reason = CacheFailureResult
		return base
	}
	if !completeness.Complete() {
		base.Eligible = false
		base.Reason = CacheEvidenceIncomplete
		return base
	}
	if decision == DecisionRequireApproval && approval.Status != ApprovalCurrentUnconsumed {
		base.Eligible = false
		base.Reason = CacheApprovalPending
		return base
	}
	if ruleNever {
		base.Eligible = false
		base.Reason = CacheRuleNever
		return base
	}
	return base
}

func (e *Evaluator) cacheIdentityWithReason(
	input EvaluationInput,
	requestIdentity string,
	expected IdentitySnapshot,
	reason CacheReason,
) CacheResult {
	binding := cacheBinding{
		Version: input.CachePolicyVersion, Request: requestIdentity,
		Transport: input.Request.Transport, ServerLabel: input.Request.ServerLabel,
		ServerFingerprint: input.Request.ServerFingerprint, Tool: input.Request.Tool,
		ToolContractDigest: input.Request.ToolContractDigest, Phase: input.Request.Phase,
		ExecutableDigest: input.ExecutableDigest,
		PlanIdentity:     e.identity, SourceIdentity: input.SourceIdentity,
		PolicyAuthority: input.Request.AuthorityMode, ContextIdentity: input.ContextIdentity,
		Principal:          input.Principal,
		CredentialLabels:   input.CredentialLabels,
		RepositoryIdentity: input.Request.RepositoryIdentity,
		StateVersion:       input.Request.StateVersion, Budget: cloneBudgetSnapshot(input.Budget),
		Approval: input.Approval,
		Taint:    input.Taint, Lifecycle: input.Lifecycle,
		Completeness: input.Request.Completeness, RepositoryEffect: input.RepositoryEffect,
		RepositoryEffectIdentity: expected.RepositoryEffectIdentity,
		Inspection:               cloneInspectionEvidence(input.Inspection),
		InspectionIdentity:       expected.InspectionIdentity,
		Resampled:                input.ResampledIdentities,
	}
	body, err := json.Marshal(binding)
	if err != nil {
		return CacheResult{Reason: CacheIdentityMissing}
	}
	return CacheResult{
		Eligible: reason == CacheEligible,
		Reason:   reason,
		Identity: digestBytes(body),
	}
}

func cloneEvaluationResult(source EvaluationResult) EvaluationResult {
	out := source
	out.MatchedRuleIDs = append([]string(nil), source.MatchedRuleIDs...)
	out.Candidates = append([]Candidate(nil), source.Candidates...)
	out.BudgetCandidates = cloneBudgetSnapshot(BudgetSnapshot{Candidates: source.BudgetCandidates}).Candidates
	out.Trace = append([]TraceEntry(nil), source.Trace...)
	out.Completeness.Missing = append([]MissingEvidence(nil), source.Completeness.Missing...)
	out.Inspection = cloneInspectionEvidence(source.Inspection)
	if source.Failure != nil {
		failure := *source.Failure
		out.Failure = &failure
	}
	return out
}
