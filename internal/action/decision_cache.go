package action

import (
	"encoding/json"
	"sync"
	"unsafe"
)

type decisionCacheEntry struct {
	result EvaluationResult
	bytes  uint64
}

type DecisionCache struct {
	mu      sync.Mutex
	entries map[string]decisionCacheEntry
	order   []string
	bytes   uint64
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
	return &DecisionCache{entries: make(map[string]decisionCacheEntry)}
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
	entry, ok := c.entries[identity.Identity]
	if !ok || !entry.result.Cache.Eligible || entry.result.Cache.Identity != identity.Identity {
		c.mu.Unlock()
		return EvaluationResult{}, false, CacheEligible
	}
	// Entries are immutable after publication. Copy after releasing the shared
	// lock so deep cloning cannot serialize concurrent cache readers or writers.
	stored := entry.result
	c.mu.Unlock()
	return cloneEvaluationResult(stored), true, CacheEligible
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
	// Clone and account before taking the mutex. The map retains only this
	// private immutable graph; an oversized legal result simply bypasses cache.
	stored := cloneEvaluationResult(result)
	entryBytes := estimateDecisionCacheResultBytes(stored)
	if entryBytes > MaxDecisionCacheBytes {
		return false
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]decisionCacheEntry)
	}
	if previous, exists := c.entries[identity.Identity]; exists {
		c.bytes -= previous.bytes
	} else {
		c.order = append(c.order, identity.Identity)
	}
	c.entries[identity.Identity] = decisionCacheEntry{result: stored, bytes: entryBytes}
	c.bytes += entryBytes
	for len(c.order) > MaxDecisionCacheEntries || c.bytes > MaxDecisionCacheBytes {
		oldest := c.order[0]
		c.order = c.order[1:]
		if previous, exists := c.entries[oldest]; exists {
			c.bytes -= previous.bytes
			delete(c.entries, oldest)
		}
	}
	c.mu.Unlock()
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

const (
	decisionCacheAllocationOverhead = uint64(512)
	decisionCacheSliceOverhead      = uint64(64)
	decisionCacheStringOverhead     = uint64(16)
)

// estimateDecisionCacheResultBytes deliberately counts more than the wire
// representation: Go value headers, backing arrays, string payloads, and a
// conservative allocator allowance are included. It is calculated before the
// cache mutex is acquired and is used only for admission/eviction.
func estimateDecisionCacheResultBytes(result EvaluationResult) uint64 {
	size := decisionCacheAllocationOverhead + uint64(unsafe.Sizeof(result))
	addDecisionCacheString(&size, string(result.Decision))
	addDecisionCacheString(&size, string(result.Reason))
	addDecisionCacheString(&size, result.ToolID)
	addDecisionCacheString(&size, result.PolicyDigest)
	addDecisionCacheString(&size, result.LockDigest)
	addDecisionCacheString(&size, result.PlanIdentity)
	addDecisionCacheString(&size, result.SourceIdentity)
	addDecisionCacheString(&size, result.Cache.Identity)
	addDecisionCacheString(&size, string(result.Cache.Reason))
	addDecisionCacheString(&size, result.RequiredApprovalIdentity)
	addDecisionCacheString(&size, string(result.PhaseOutcome))

	addDecisionCacheStringSlice(&size, result.MatchedRuleIDs)
	addDecisionCacheSlice(&size, len(result.Candidates), unsafe.Sizeof(Candidate{}))
	for _, candidate := range result.Candidates {
		addDecisionCacheString(&size, string(candidate.Source))
		addDecisionCacheString(&size, candidate.ID)
		addDecisionCacheString(&size, string(candidate.Decision))
		addDecisionCacheString(&size, string(candidate.Reason))
	}
	addDecisionCacheSlice(&size, len(result.BudgetCandidates), unsafe.Sizeof(BudgetCandidate{}))
	for _, candidate := range result.BudgetCandidates {
		addDecisionCacheString(&size, candidate.BudgetID)
		addDecisionCacheString(&size, candidate.ScopeIdentity)
		addDecisionCacheString(&size, candidate.LineageIdentity)
		addDecisionCacheString(&size, string(candidate.Reset))
		addDecisionCacheString(&size, string(candidate.Reason))
		addDecisionCacheString(&size, candidate.Scope.RepositoryIdentity)
		addDecisionCacheString(&size, candidate.Scope.Principal)
		addDecisionCacheStringSlice(&size, candidate.Scope.CredentialLabels)
		addDecisionCacheString(&size, candidate.Scope.ServerLabel)
		addDecisionCacheString(&size, candidate.Scope.ServerIdentity)
		addDecisionCacheString(&size, candidate.Scope.ToolID)
		addDecisionCacheString(&size, candidate.Scope.RunIdentity)
		addDecisionCacheString(&size, candidate.Scope.SessionIdentity)
		addDecisionCacheString(&size, candidate.Scope.WindowIdentity)
		addDecisionCacheString(&size, candidate.Generation.PolicyDigest)
		addDecisionCacheString(&size, candidate.Generation.ExecutableDigest)
		addDecisionCacheString(&size, candidate.Generation.ToolContractDigest)
		addDecisionCacheString(&size, candidate.Generation.KeyID)
	}
	addDecisionCacheSlice(&size, len(result.Trace), unsafe.Sizeof(TraceEntry{}))
	for _, trace := range result.Trace {
		addDecisionCacheString(&size, trace.RuleID)
		addDecisionCacheString(&size, trace.ToolID)
		addDecisionCacheString(&size, string(trace.Selector))
		addDecisionCacheString(&size, string(trace.Condition))
		addDecisionCacheString(&size, string(trace.CandidateDecision))
		addDecisionCacheString(&size, string(trace.Reason))
		addDecisionCacheString(&size, string(trace.ActualProvenance))
		addDecisionCacheString(&size, string(trace.RequiredProvenance))
		addDecisionCacheString(&size, string(trace.Operand.PointerState))
		addDecisionCacheString(&size, string(trace.Operand.Kind))
	}
	addDecisionCacheSlice(&size, len(result.Completeness.Missing), unsafe.Sizeof(MissingEvidence{}))
	for _, missing := range result.Completeness.Missing {
		addDecisionCacheString(&size, string(missing.Field))
		addDecisionCacheString(&size, string(missing.Reason))
	}
	if result.Inspection != nil {
		size += decisionCacheAllocationOverhead + uint64(unsafe.Sizeof(*result.Inspection))
		addDecisionCacheString(&size, string(result.Inspection.Status))
		addDecisionCacheString(&size, result.Inspection.Identity)
		addDecisionCacheString(&size, string(result.Inspection.Decision))
		addDecisionCacheString(&size, string(result.Inspection.Reason))
		addDecisionCacheStringSlice(&size, result.Inspection.RuleIDs)
		addDecisionCacheStringSlice(&size, result.Inspection.PackIdentities)
		addDecisionCacheString(&size, string(result.Inspection.SchemaStatus))
		addDecisionCacheString(&size, result.Inspection.SchemaIdentity)
		addDecisionCacheSlice(&size, len(result.Inspection.Categories), unsafe.Sizeof(DetectorCategory("")))
		for _, category := range result.Inspection.Categories {
			addDecisionCacheString(&size, string(category))
		}
		addDecisionCacheSlice(&size, len(result.Inspection.Fields), unsafe.Sizeof(InspectionFieldEvidence{}))
		for _, field := range result.Inspection.Fields {
			addDecisionCacheString(&size, string(field.Source))
			addDecisionCacheString(&size, field.PointerIdentity)
			addDecisionCacheString(&size, field.ValueIdentity)
		}
		addDecisionCacheSlice(&size, len(result.Inspection.UnsupportedContent), unsafe.Sizeof(InspectionContentEvidence{}))
		for _, content := range result.Inspection.UnsupportedContent {
			addDecisionCacheString(&size, string(content.ContentType))
			addDecisionCacheString(&size, content.Identity)
		}
	}
	if result.Failure != nil {
		size += decisionCacheAllocationOverhead + uint64(unsafe.Sizeof(*result.Failure))
		addDecisionCacheString(&size, string(result.Failure.Code))
		addDecisionCacheString(&size, result.Failure.Message)
	}
	return size
}

func addDecisionCacheSlice(total *uint64, count int, elementSize uintptr) {
	if count <= 0 {
		return
	}
	addDecisionCacheBytes(total, decisionCacheSliceOverhead)
	addDecisionCacheBytes(total, uint64(count)*uint64(elementSize))
}

func addDecisionCacheStringSlice(total *uint64, values []string) {
	addDecisionCacheSlice(total, len(values), unsafe.Sizeof(""))
	for _, value := range values {
		addDecisionCacheString(total, value)
	}
}

func addDecisionCacheString(total *uint64, value string) {
	if value == "" {
		return
	}
	addDecisionCacheBytes(total, uint64(len(value))+decisionCacheStringOverhead)
}

func addDecisionCacheBytes(total *uint64, amount uint64) {
	if ^uint64(0)-*total < amount {
		*total = ^uint64(0)
		return
	}
	*total += amount
}
