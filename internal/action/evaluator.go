package action

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

type Evaluator struct {
	plan        Plan
	rules       []CompiledRule
	toolByExact map[string]int
	identity    string
}

type matchedRule struct {
	id       string
	decision Decision
}

type evaluationAccumulator struct {
	evaluator       *Evaluator
	input           EvaluationInput
	requestIdentity string
	tool            *Tool
	toolID          string
	candidates      []Candidate
	matched         []matchedRule
	trace           []TraceEntry
	budgets         []BudgetCandidate
	roots           predicateRoots
	cacheNever      bool
	completeness    Completeness
}

func NewEvaluator(compiled *CompiledPlan) (*Evaluator, error) {
	if compiled == nil {
		return nil, fmt.Errorf("action plan is required")
	}
	recompiled, err := CompilePlan(compiled.Plan())
	if err != nil {
		return nil, fmt.Errorf("validate action plan for evaluator: %w", err)
	}
	plan := recompiled.Plan()
	body, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("encode action plan identity: %w", err)
	}
	evaluator := &Evaluator{
		plan: plan, rules: recompiled.Rules(),
		toolByExact: make(map[string]int, len(plan.Tools)),
		identity:    digestBytes(body),
	}
	for index, tool := range plan.Tools {
		key := ToolIdentityKey(tool)
		if _, duplicate := evaluator.toolByExact[key]; duplicate {
			return nil, fmt.Errorf("action plan contains ambiguous tool ownership")
		}
		evaluator.toolByExact[key] = index
	}
	return evaluator, nil
}

func (e *Evaluator) PlanIdentity() string {
	if e == nil {
		return ""
	}
	return e.identity
}

// RuleIDs returns the canonical action rule identities in stable order.
func (e *Evaluator) RuleIDs() []string {
	if e == nil {
		return []string{}
	}
	ids := make([]string, len(e.plan.Rules))
	for index, rule := range e.plan.Rules {
		ids[index] = rule.ID
	}
	return ids
}

// IdentitySnapshot derives the exact identity binding expected by this pure
// evaluator from caller-supplied state. It performs no IO or trust upgrade;
// callers remain responsible for freshly observing every supplied identity.
func (e *Evaluator) IdentitySnapshot(input EvaluationInput) IdentitySnapshot {
	if e == nil {
		return IdentitySnapshot{}
	}
	return e.expectedIdentities(input)
}

// EvaluateRaw applies the production request normalizer before evaluation.
// The Request field in input is replaced by raw; all other state and the
// independently resampled identity snapshot remain caller-owned.
func (e *Evaluator) EvaluateRaw(raw RawRequest, input EvaluationInput) EvaluationResult {
	request, err := NormalizeRequest(raw)
	if err != nil {
		input.Request = requestMetadata(raw)
		code, message := ReasonInvalidRequest, "request normalization failed"
		var requestError *RequestError
		if errors.As(err, &requestError) {
			code, message = requestError.Code, requestError.Message
		}
		return failEvaluation(e, input, code, message)
	}
	input.Request = request
	return e.Evaluate(input)
}

func (e *Evaluator) Evaluate(input EvaluationInput) EvaluationResult {
	if e == nil {
		return failEvaluation(nil, input, ReasonPolicyMissing, "compiled action policy is unavailable")
	}
	normalized, requestIdentity, failure := e.prepareEvaluation(input)
	if failure != nil {
		return *failure
	}
	accumulator := newEvaluationAccumulator(e, normalized, requestIdentity)
	if failure = accumulator.addInspection(); failure != nil {
		return *failure
	}
	if failure = accumulator.addBudgets(); failure != nil {
		return *failure
	}
	if failure = accumulator.addRepositoryEffect(); failure != nil {
		return *failure
	}
	if failure = accumulator.evaluateRules(); failure != nil {
		return *failure
	}
	return accumulator.result()
}

func (e *Evaluator) prepareEvaluation(
	input EvaluationInput,
) (EvaluationInput, string, *EvaluationResult) {
	normalized, err := e.normalizeEvaluationInput(input)
	if err != nil {
		failure := failEvaluation(e, input, err.Code, err.Message)
		return EvaluationInput{}, "", &failure
	}
	input = normalized
	requestIdentity, identityErr := requestDigestValidated(input.Request)
	if identityErr != nil {
		failure := failEvaluation(e, input, ReasonInternalInvariant, "canonical request identity failed")
		return EvaluationInput{}, "", &failure
	}
	if code := e.preflightFailure(input); code != "" {
		failure := failEvaluation(e, input, code, failureMessage(code))
		return EvaluationInput{}, "", &failure
	}
	return input, requestIdentity, nil
}

func (e *Evaluator) preflightFailure(input EvaluationInput) ReasonCode {
	if input.Request.Deadline == DeadlineExceeded {
		return ReasonDeadlineExceeded
	}
	if input.Lifecycle == LifecycleCancelled {
		return ReasonCancelled
	}
	if input.Lifecycle == LifecycleShutdown {
		return ReasonShutdown
	}
	if !input.Request.Completeness.Complete() {
		return input.Request.Completeness.Missing[0].Reason
	}
	if code := e.verifyResampledIdentities(input); code != "" {
		return code
	}
	return ""
}

func newEvaluationAccumulator(
	evaluator *Evaluator,
	input EvaluationInput,
	requestIdentity string,
) *evaluationAccumulator {
	tool, toolID := evaluator.selectTool(input.Request)
	return &evaluationAccumulator{
		evaluator: evaluator, input: input, requestIdentity: requestIdentity,
		tool: tool, toolID: toolID,
		candidates:   []Candidate{evaluator.baselineCandidate(input.Request, tool)},
		matched:      make([]matchedRule, 0, len(evaluator.rules)),
		trace:        make([]TraceEntry, 0, min(len(evaluator.rules)+1, MaxTraceEntries)),
		budgets:      cloneBudgetSnapshot(input.Budget).Candidates,
		completeness: input.Request.Completeness,
	}
}

func (a *evaluationAccumulator) addBudgets() *EvaluationResult {
	for _, budget := range a.budgets {
		if budget.Available {
			continue
		}
		a.candidates = append(a.candidates, Candidate{
			Source: CandidateBudget, ID: budget.BudgetID,
			Decision: DecisionBlock, Reason: ReasonBudgetExhausted,
		})
	}
	return nil
}

func (a *evaluationAccumulator) addInspection() *EvaluationResult {
	if a.input.Inspection == nil {
		return nil
	}
	evidence := a.input.Inspection
	if evidence.Status == InspectionIncomplete {
		markEvaluationIncomplete(&a.completeness, evidence.Reason)
		a.input.Request.Completeness = a.completeness
		failure := failEvaluation(a.evaluator, a.input, evidence.Reason, "content inspection did not complete safely")
		return &failure
	}
	if evidence.Status != InspectionMatched {
		return nil
	}
	candidate := Candidate{
		Source: CandidateInspection, ID: evidence.RuleIDs[0],
		Decision: evidence.Decision, Reason: evidence.Reason,
	}
	a.candidates = append(a.candidates, candidate)
	for _, ruleID := range evidence.RuleIDs {
		a.matched = append(a.matched, matchedRule{id: ruleID, decision: candidate.Decision})
		a.trace = append(a.trace, TraceEntry{
			RuleID: ruleID, ToolID: a.toolID, Selector: SelectorMatched,
			Condition: ConditionTrue, CandidateDecision: candidate.Decision,
			Reason: candidate.Reason, Completeness: true,
			Operand: OperandSummary{ByteLength: int(evidence.ScannedBytes), ItemCount: int(evidence.ScannedItems)},
		})
	}
	return nil
}

func (a *evaluationAccumulator) addRepositoryEffect() *EvaluationResult {
	if a.tool == nil || a.tool.Effect.Kind != EffectRepositoryRead &&
		a.tool.Effect.Kind != EffectRepositoryWrite {
		return nil
	}
	if a.input.RepositoryEffect == nil || !a.input.RepositoryEffect.Complete {
		failure := failEvaluation(a.evaluator, a.input, ReasonInspectionIncomplete, "repository effect evidence is incomplete")
		return &failure
	}
	candidate := Candidate{
		Source: CandidateRepositoryEffect, ID: "repository-effect",
		Decision: a.input.RepositoryEffect.Decision, Reason: a.input.RepositoryEffect.Reason,
	}
	a.candidates = append(a.candidates, candidate)
	for _, ruleID := range a.input.RepositoryEffect.RuleIDs {
		a.matched = append(a.matched, matchedRule{id: ruleID, decision: candidate.Decision})
		a.trace = append(a.trace, TraceEntry{
			RuleID: ruleID, ToolID: a.toolID, Selector: SelectorMatched,
			Condition: ConditionTrue, CandidateDecision: candidate.Decision,
			Reason: candidate.Reason, Completeness: true,
		})
	}
	return nil
}

func (a *evaluationAccumulator) evaluateRules() *EvaluationResult {
	for _, rule := range a.evaluator.rules {
		if code := a.evaluateRule(rule); code != "" {
			failure := failEvaluation(a.evaluator, a.input, code, failureMessage(code))
			return &failure
		}
	}
	return nil
}

func (a *evaluationAccumulator) evaluateRule(rule CompiledRule) ReasonCode {
	entry := TraceEntry{
		RuleID: rule.Rule.ID, ToolID: a.toolID,
		Selector: SelectorUnmatched, Condition: ConditionFalse, Completeness: true,
	}
	if !selectorMatches(rule.Rule.Selector, a.input.Request, a.toolID) {
		a.trace = append(a.trace, entry)
		return ""
	}
	entry.Selector = SelectorMatched
	condition := evaluateConditionTreeWithRoots(rule.Condition, a.input.Request, rule.Rule.Decision, 1, &a.roots)
	if condition.reason == ReasonInternalInvariant {
		return ReasonInternalInvariant
	}
	populateConditionTrace(&entry, condition)
	if condition.state == ConditionFalse {
		a.trace = append(a.trace, entry)
		return ""
	}
	if condition.state == ConditionIndeterminate {
		entry.CandidateDecision, entry.Reason = rule.Rule.OnIndeterminate, condition.reason
		markEvaluationIncomplete(&a.completeness, condition.reason)
	} else if condition.state == ConditionTrue {
		entry.CandidateDecision, entry.Reason = rule.Rule.Decision, candidateReason(rule.Rule.Decision)
	} else {
		return ReasonInternalInvariant
	}
	a.addRuleCandidate(rule, entry)
	return ""
}

func populateConditionTrace(entry *TraceEntry, condition conditionEvaluation) {
	entry.Condition = condition.state
	entry.ActualProvenance = condition.actual
	entry.RequiredProvenance = condition.required
	entry.Completeness = condition.complete
	entry.Operand = condition.summary
}

func (a *evaluationAccumulator) addRuleCandidate(rule CompiledRule, entry TraceEntry) {
	candidate := Candidate{
		Source: CandidateRule, ID: rule.Rule.ID,
		Decision: entry.CandidateDecision, Reason: entry.Reason,
	}
	a.candidates = append(a.candidates, candidate)
	a.matched = append(a.matched, matchedRule{id: rule.Rule.ID, decision: candidate.Decision})
	a.cacheNever = a.cacheNever || rule.Rule.Cache == CacheNever
	a.trace = append(a.trace, entry)
}

func (a *evaluationAccumulator) result() EvaluationResult {
	sortCandidates(a.candidates)
	sortMatchedRules(a.matched)
	decision := a.candidates[0].Decision
	result := EvaluationResult{
		Decision: decision, Reason: a.candidates[0].Reason, ToolID: a.toolID,
		MatchedRuleIDs: matchedRuleIDs(a.matched), Candidates: a.candidates,
		BudgetCandidates: cloneBudgetSnapshot(a.input.Budget).Candidates,
		Completeness:     a.completeness, PolicyDigest: a.input.Request.PolicyDigest,
		LockDigest: a.input.Request.LockDigest, PlanIdentity: a.evaluator.identity,
		SourceIdentity: a.input.SourceIdentity,
		PhaseOutcome:   phaseOutcome(a.input.Request.Phase, decision),
		Inspection:     cloneInspectionEvidence(a.input.Inspection),
	}
	result.Trace, result.TraceComplete, result.TraceOmitted = boundTrace(a.trace)
	result.Cache = a.evaluator.cacheResult(
		a.input, a.requestIdentity, a.cacheNever, decision, result.Completeness, false,
	)
	if decision == DecisionRequireApproval {
		result.RequiredApprovalIdentity = approvalRequirementIdentity(a.input, result)
	}
	return result
}

func (e *Evaluator) normalizeEvaluationInput(input EvaluationInput) (EvaluationInput, *RequestError) {
	request, err := validateAndCloneRequest(input.Request)
	if err != nil {
		requestError, ok := err.(*RequestError)
		if ok {
			return EvaluationInput{}, requestError
		}
		return EvaluationInput{}, &RequestError{Code: ReasonInvalidRequest, Message: "request is invalid"}
	}
	input.Request = request
	if !lowerHex64(input.SourceIdentity) || !validOpaqueIdentity(input.ContextIdentity) ||
		!SafeLabel(input.Principal) {
		return EvaluationInput{}, &RequestError{Code: ReasonIdentityUnavailable, Message: "source or principal identity is unavailable"}
	}
	credentials, listErr := safeStringList(input.CredentialLabels, MaxCredentialLabels)
	if listErr != nil {
		return EvaluationInput{}, &RequestError{Code: ReasonInvalidRequest, Message: "credential labels are invalid"}
	}
	input.CredentialLabels = credentials
	if input.Request.Transport == TransportMCPStdio {
		if !sha256IdentityPattern.MatchString(input.ExecutableDigest) {
			return EvaluationInput{}, &RequestError{Code: ReasonIdentityUnavailable, Message: "executable identity is unavailable"}
		}
	} else if input.ExecutableDigest == "" {
		input.ExecutableDigest = "absent"
	}
	if !input.Approval.Status.Valid() || !validOpaqueIdentity(input.Approval.Identity) ||
		!input.Taint.Status.Valid() || !validOpaqueIdentity(input.Taint.Identity) ||
		!input.Lifecycle.Valid() || input.CachePolicyVersion != CacheIdentityVersion {
		return EvaluationInput{}, &RequestError{Code: ReasonIdentityUnavailable, Message: "mutable authority identity is unavailable"}
	}
	if input.RepositoryEffect != nil {
		candidate := *input.RepositoryEffect
		if !candidate.Decision.Valid() || !candidate.Reason.Valid() || !validOpaqueIdentity(candidate.Identity) {
			return EvaluationInput{}, &RequestError{Code: ReasonInvalidRequest, Message: "repository effect candidate is invalid"}
		}
		ruleIDs, listErr := safeStringList(candidate.RuleIDs, MaxListValues)
		if listErr != nil {
			return EvaluationInput{}, &RequestError{Code: ReasonInvalidRequest, Message: "repository effect rule identities are invalid"}
		}
		candidate.RuleIDs = ruleIDs
		input.RepositoryEffect = &candidate
	}
	inspection, inspectionErr := normalizeInspectionEvidence(
		input.Inspection, e.inspectionExpectation(input.Request), input.Request.Phase,
	)
	if inspectionErr != nil {
		return EvaluationInput{}, inspectionErr
	}
	input.Inspection = inspection
	if budgetErr := e.normalizeBudgetInput(&input); budgetErr != nil {
		return EvaluationInput{}, budgetErr
	}
	snapshot, valid := normalizeIdentitySnapshot(input.ResampledIdentities)
	if !valid {
		return EvaluationInput{}, &RequestError{Code: ReasonIdentityUnavailable, Message: "resampled identity set is incomplete"}
	}
	input.ResampledIdentities = snapshot
	return input, nil
}

func normalizeIdentitySnapshot(snapshot IdentitySnapshot) (IdentitySnapshot, bool) {
	credentials, err := safeStringList(snapshot.CredentialLabels, MaxCredentialLabels)
	if err != nil {
		return IdentitySnapshot{}, false
	}
	valid := sha256IdentityPattern.MatchString(snapshot.PlanIdentity) &&
		lowerHex64(snapshot.SourceIdentity) &&
		lowerHex64(snapshot.PolicyDigest) &&
		lowerHex64(snapshot.LockDigest) &&
		snapshot.AuthorityMode.Valid() &&
		(snapshot.ServerLabel == "" || SafeLabel(snapshot.ServerLabel)) &&
		ValidIdentity(snapshot.ServerFingerprint) &&
		sha256IdentityPattern.MatchString(snapshot.ToolContractDigest) &&
		validExecutableSnapshot(snapshot.ExecutableDigest) &&
		ValidIdentity(snapshot.RepositoryIdentity) &&
		validOpaqueIdentity(snapshot.ContextIdentity) &&
		SafeLabel(snapshot.Principal) &&
		validOpaqueIdentity(snapshot.StateVersion) &&
		validOpaqueIdentity(snapshot.BudgetIdentity) &&
		validOpaqueIdentity(snapshot.ReservationIdentity) &&
		validOpaqueIdentity(snapshot.ApprovalIdentity) &&
		validOpaqueIdentity(snapshot.TaintIdentity) &&
		validOpaqueIdentity(snapshot.RepositoryEffectIdentity) &&
		validOpaqueIdentity(snapshot.InspectionIdentity)
	if !valid {
		return IdentitySnapshot{}, false
	}
	snapshot.CredentialLabels = credentials
	return snapshot, true
}

func (e *Evaluator) expectedIdentities(input EvaluationInput) IdentitySnapshot {
	repositoryEffectIdentity := "absent"
	if input.RepositoryEffect != nil {
		repositoryEffectIdentity = input.RepositoryEffect.Identity
	}
	budgetIdentity := input.Budget.Identity
	reservationIdentity := input.Budget.ReservationIdentity
	if budgetSnapshotZero(input.Budget) {
		budgetIdentity, reservationIdentity = "absent", "absent"
	}
	executableDigest := input.ExecutableDigest
	if executableDigest == "" && input.Request.Transport == TransportHostMCP {
		executableDigest = "absent"
	}
	return IdentitySnapshot{
		PlanIdentity: e.identity, SourceIdentity: input.SourceIdentity,
		PolicyDigest: input.Request.PolicyDigest, LockDigest: input.Request.LockDigest,
		AuthorityMode:       input.Request.AuthorityMode,
		ServerLabel:         input.Request.ServerLabel,
		ServerFingerprint:   input.Request.ServerFingerprint,
		ToolContractDigest:  input.Request.ToolContractDigest,
		ExecutableDigest:    executableDigest,
		RepositoryIdentity:  input.Request.RepositoryIdentity,
		ContextIdentity:     input.ContextIdentity,
		Principal:           input.Principal,
		CredentialLabels:    append([]string(nil), input.CredentialLabels...),
		StateVersion:        input.Request.StateVersion,
		BudgetIdentity:      budgetIdentity,
		ReservationIdentity: reservationIdentity,
		ApprovalIdentity:    input.Approval.Identity, TaintIdentity: input.Taint.Identity,
		RepositoryEffectIdentity: repositoryEffectIdentity,
		InspectionIdentity:       inspectionIdentity(input.Inspection),
	}
}

func (e *Evaluator) verifyResampledIdentities(input EvaluationInput) ReasonCode {
	expected := e.expectedIdentities(input)
	actual := input.ResampledIdentities
	if actual.PlanIdentity != expected.PlanIdentity || actual.SourceIdentity != expected.SourceIdentity ||
		actual.PolicyDigest != expected.PolicyDigest {
		return ReasonPolicyStale
	}
	if actual.LockDigest != expected.LockDigest {
		return ReasonLockMismatch
	}
	if actual.ToolContractDigest != expected.ToolContractDigest {
		return ReasonToolContractStale
	}
	if actual.ExecutableDigest != expected.ExecutableDigest {
		return ReasonIdentityUnavailable
	}
	if actual.AuthorityMode != expected.AuthorityMode ||
		actual.ServerLabel != expected.ServerLabel ||
		actual.ServerFingerprint != expected.ServerFingerprint ||
		actual.RepositoryIdentity != expected.RepositoryIdentity ||
		actual.ContextIdentity != expected.ContextIdentity ||
		actual.Principal != expected.Principal ||
		!equalStrings(actual.CredentialLabels, expected.CredentialLabels) {
		return ReasonIdentityUnavailable
	}
	if actual.StateVersion != expected.StateVersion ||
		actual.BudgetIdentity != expected.BudgetIdentity ||
		actual.ReservationIdentity != expected.ReservationIdentity ||
		actual.ApprovalIdentity != expected.ApprovalIdentity ||
		actual.RepositoryEffectIdentity != expected.RepositoryEffectIdentity ||
		actual.InspectionIdentity != expected.InspectionIdentity {
		return ReasonStateUnavailable
	}
	if actual.TaintIdentity != expected.TaintIdentity {
		return ReasonIdentityUnavailable
	}
	return ""
}

func equalStrings(left, right []string) bool {
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

func validExecutableSnapshot(value string) bool {
	return value == "absent" || sha256IdentityPattern.MatchString(value)
}

func (e *Evaluator) selectTool(request Request) (*Tool, string) {
	tool := Tool{
		Transport: request.Transport, Platform: request.Platform,
		ServerLabel: request.ServerLabel, ServerFingerprint: request.ServerFingerprint,
		Tool: request.Tool,
	}
	index, ok := lookupToolIndex(e.toolByExact, tool)
	if !ok || index < 0 || index >= len(e.plan.Tools) {
		return nil, ""
	}
	return &e.plan.Tools[index], e.plan.Tools[index].ID
}

func (e *Evaluator) baselineCandidate(request Request, tool *Tool) Candidate {
	if tool != nil {
		reason := ReasonDeclaredTool
		if e.plan.Defaults.DeclaredTool == DecisionRequireApproval {
			reason = ReasonApprovalRequired
		}
		return Candidate{
			Source: CandidateBaseline, ID: "declared-tool",
			Decision: e.plan.Defaults.DeclaredTool,
			Reason:   reason,
		}
	}
	if request.Transport == TransportMCPStdio {
		return Candidate{
			Source: CandidateBaseline, ID: "gateway-unmatched",
			Decision: e.plan.Defaults.GatewayUnmatched, Reason: ReasonToolUnclassified,
		}
	}
	return Candidate{
		Source: CandidateBaseline, ID: "host-unmatched",
		Decision: e.plan.Defaults.HostUnmatched, Reason: ReasonHostUnmatched,
	}
}

func selectorMatches(selector Selector, request Request, toolID string) bool {
	return stringListed(selector.ToolIDs, toolID) &&
		transportListed(selector.Transports, request.Transport) &&
		platformListed(selector.Platforms, request.Platform) &&
		stringListed(selector.ServerLabels, request.ServerLabel) &&
		stringListed(selector.ServerFingerprints, request.ServerFingerprint) &&
		stringListed(selector.Tools, request.Tool) &&
		stringListed(selector.ToolContractDigests, request.ToolContractDigest) &&
		phaseListed(selector.Phases, request.Phase)
}

func stringListed(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	return containsSorted(values, value)
}

func transportListed(values []Transport, value Transport) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func platformListed(values []Platform, value Platform) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func phaseListed(values []Phase, value Phase) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func sortCandidates(candidates []Candidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Decision.Strength() != candidates[j].Decision.Strength() {
			return candidates[i].Decision.Strength() > candidates[j].Decision.Strength()
		}
		return candidateOrder(candidates[i]) < candidateOrder(candidates[j])
	})
}

func candidateOrder(candidate Candidate) string {
	switch candidate.Source {
	case CandidateRule:
		return "0:" + candidate.ID
	case CandidateInspection:
		return "1:" + candidate.ID
	case CandidateBudget:
		return "2:" + candidate.ID
	case CandidateRepositoryEffect:
		return "3:" + candidate.ID
	default:
		return "4:" + candidate.ID
	}
}

func sortMatchedRules(rules []matchedRule) {
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].decision.Strength() != rules[j].decision.Strength() {
			return rules[i].decision.Strength() > rules[j].decision.Strength()
		}
		return rules[i].id < rules[j].id
	})
}

func matchedRuleIDs(rules []matchedRule) []string {
	values := make([]string, 0, len(rules))
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if _, duplicate := seen[rule.id]; duplicate {
			continue
		}
		seen[rule.id] = struct{}{}
		values = append(values, rule.id)
	}
	return values
}

func candidateReason(decision Decision) ReasonCode {
	if decision == DecisionRequireApproval {
		return ReasonApprovalRequired
	}
	return ReasonRuleMatched
}

func markEvaluationIncomplete(completeness *Completeness, reason ReasonCode) {
	field := EvidencePhase
	if reason == ReasonContextUntrusted || reason == ReasonIdentityUnavailable {
		field = EvidenceContext
	}
	switch field {
	case EvidenceContext:
		completeness.ContextComplete = false
	default:
		completeness.PhaseComplete = false
	}
	missing := MissingEvidence{Field: field, Reason: reason}
	for _, existing := range completeness.Missing {
		if existing == missing {
			return
		}
	}
	completeness.Missing = append(completeness.Missing, missing)
	sort.Slice(completeness.Missing, func(i, j int) bool {
		if completeness.Missing[i].Field != completeness.Missing[j].Field {
			return completeness.Missing[i].Field < completeness.Missing[j].Field
		}
		return completeness.Missing[i].Reason < completeness.Missing[j].Reason
	})
}

func boundTrace(entries []TraceEntry) ([]TraceEntry, bool, int) {
	if entries == nil {
		return []TraceEntry{}, true, 0
	}
	kept := make([]TraceEntry, 0, min(len(entries), MaxTraceEntries))
	logicalBytes := 2
	for index, entry := range entries {
		body, err := json.Marshal(entry)
		if err != nil || len(kept) == MaxTraceEntries || logicalBytes+len(body)+1 > MaxTraceBytes {
			omitted := len(entries) - index
			return appendTraceOverflow(kept, omitted, logicalBytes)
		}
		kept = append(kept, entry)
		logicalBytes += len(body) + 1
	}
	return kept, true, 0
}

func appendTraceOverflow(kept []TraceEntry, omitted, logicalBytes int) ([]TraceEntry, bool, int) {
	marker := TraceEntry{
		RuleID: "trace-overflow", Selector: SelectorUnmatched,
		Condition: ConditionIndeterminate, Reason: ReasonLimitExceeded,
		Completeness: false, Omitted: omitted,
	}
	body, _ := json.Marshal(marker)
	for len(kept) > 0 && (len(kept) >= MaxTraceEntries || logicalBytes+len(body)+1 > MaxTraceBytes) {
		last, _ := json.Marshal(kept[len(kept)-1])
		logicalBytes -= len(last) + 1
		kept = kept[:len(kept)-1]
		omitted++
		marker.Omitted = omitted
		body, _ = json.Marshal(marker)
	}
	kept = append(kept, marker)
	return kept, false, omitted
}

func phaseOutcome(phase Phase, decision Decision) PhaseOutcome {
	permitted := decision == DecisionAllow || decision == DecisionWarn
	switch phase {
	case PhasePreCall:
		if permitted {
			return OutcomeDispatchEligible
		}
		return OutcomeDispatchBlocked
	case PhasePostResult:
		if permitted {
			return OutcomeDeliveryEligible
		}
		return OutcomeWithheld
	case PhaseProgress:
		if permitted {
			return OutcomeProgressEligible
		}
		return OutcomeSuppressed
	case PhaseObservation:
		return OutcomeRecorded
	default:
		return OutcomeDispatchBlocked
	}
}

// OutcomeFor returns the stable phase outcome for one decision.
func OutcomeFor(phase Phase, decision Decision) PhaseOutcome {
	return phaseOutcome(phase, decision)
}

func failEvaluation(
	e *Evaluator,
	input EvaluationInput,
	code ReasonCode,
	message string,
) EvaluationResult {
	planIdentity := ""
	if e != nil {
		planIdentity = e.identity
	}
	phase := input.Request.Phase
	result := EvaluationResult{
		Decision: DecisionBlock, Reason: code,
		MatchedRuleIDs: []string{}, Candidates: []Candidate{}, Trace: []TraceEntry{},
		BudgetCandidates: cloneBudgetSnapshot(input.Budget).Candidates,
		TraceComplete:    true, Completeness: input.Request.Completeness,
		PolicyDigest: input.Request.PolicyDigest, LockDigest: input.Request.LockDigest,
		PlanIdentity: planIdentity, SourceIdentity: input.SourceIdentity,
		Cache:        CacheResult{Reason: CacheFailureResult},
		PhaseOutcome: phaseOutcome(phase, DecisionBlock),
		Failure:      &Failure{Code: code, Message: message},
		Inspection:   cloneInspectionEvidence(input.Inspection),
	}
	if e != nil {
		result.Cache = e.CacheIdentity(input)
		result.Cache.Eligible = false
		if result.Cache.Reason == CacheEligible {
			result.Cache.Reason = CacheFailureResult
		}
	}
	return result
}

func failureMessage(code ReasonCode) string {
	switch code {
	case ReasonDeadlineExceeded:
		return "evaluation deadline was reached"
	case ReasonCancelled:
		return "evaluation was cancelled"
	case ReasonShutdown:
		return "gateway shutdown prevents evaluation"
	case ReasonContextUntrusted, ReasonInspectionIncomplete:
		return "required evaluation evidence is incomplete"
	case ReasonPolicyStale:
		return "policy identity changed before use"
	case ReasonLockMismatch:
		return "lock identity changed before use"
	case ReasonToolContractStale:
		return "tool contract changed before use"
	case ReasonStateUnavailable:
		return "mutable state identity changed before use"
	case ReasonInternalInvariant:
		return "compiled action invariant failed"
	default:
		return "required identity changed before use"
	}
}

func approvalRequirementIdentity(input EvaluationInput, result EvaluationResult) string {
	binding := struct {
		CallID       string   `json:"call_id"`
		Request      string   `json:"request"`
		Plan         string   `json:"plan"`
		Source       string   `json:"source"`
		Rules        []string `json:"rules"`
		Decision     Decision `json:"decision"`
		StateVersion string   `json:"state_version"`
	}{
		CallID: input.Request.CallID, Request: result.Cache.Identity,
		Plan: result.PlanIdentity, Source: result.SourceIdentity,
		Rules: result.MatchedRuleIDs, Decision: result.Decision,
		StateVersion: input.Request.StateVersion,
	}
	body, _ := json.Marshal(binding)
	return digestBytes(body)
}
