package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionstate"
	"reconc.dev/reconc/internal/pathidentity"
)

func (g *Gateway) normalizedRequest(
	snapshot PolicySnapshot,
	contract ToolContract,
	callID string,
	stateVersion string,
	phase action.Phase,
	payload json.RawMessage,
) (action.Request, error) {
	requestContext, err := g.requestContext()
	if err != nil {
		return action.Request{}, err
	}
	raw := action.RawRequest{
		FormatVersion:      action.RequestFormatVersion,
		CallID:             callID,
		Transport:          action.TransportMCPStdio,
		ServerLabel:        g.config.ServerLabel,
		ServerFingerprint:  g.server.ServerIdentity,
		Tool:               contract.Name,
		ToolContractDigest: contract.ContractDigest,
		Phase:              phase,
		RepositoryIdentity: g.storage.RepositoryIdentity(),
		PolicyDigest:       snapshot.SourceDigest,
		LockDigest:         snapshot.LockDigest,
		AuthorityMode:      g.config.PolicyAuthority.Mode,
		Context:            requestContext,
		Completeness:       action.CompleteEvidence(),
		Deadline:           action.DeadlineReady,
		StateVersion:       stateVersion,
	}
	switch phase {
	case action.PhasePreCall:
		raw.Arguments = payload
	case action.PhasePostResult:
		raw.Result = payload
	case action.PhaseProgress:
		raw.Progress = payload
	}
	return action.NormalizeRequest(raw)
}

func (g *Gateway) requestContext() ([]action.RawContextValue, error) {
	values := make([]action.RawContextValue, 0, 9)
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "principal", value: g.config.Principal},
		{name: "server_label", value: g.config.ServerLabel},
	} {
		value, err := rawContext(field.name, field.value)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	credentials, err := rawStringListContext("credential_labels", g.config.CredentialLabels)
	if err != nil {
		return nil, err
	}
	values = append(values, credentials)
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "role", value: g.config.Role},
		{name: "environment", value: g.config.Environment},
		{name: "run_id", value: g.config.RunID},
		{name: "session_id", value: g.config.SessionID},
		{name: "approval_authority", value: g.config.ApprovalPolicyID},
	} {
		if field.value != "" {
			value, err := rawContext(field.name, field.value)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
	}
	if g.config.PolicyAuthority.Mode == action.AuthorityOperatorPinned {
		value, err := rawContext("expected_lock_digest", g.config.PolicyAuthority.ExpectedLockDigest)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values, nil
}

func rawContext(name, value string) (action.RawContextValue, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return action.RawContextValue{}, fmt.Errorf("encode operator context %q: %w", name, err)
	}
	return action.RawContextValue{
		Name: name, Value: body, Provenance: action.ProvenanceOperatorBound, Available: true,
	}, nil
}

func rawStringListContext(name string, values []string) (action.RawContextValue, error) {
	copy := append([]string(nil), values...)
	sort.Strings(copy)
	body, err := json.Marshal(copy)
	if err != nil {
		return action.RawContextValue{}, fmt.Errorf("encode operator context %q: %w", name, err)
	}
	return action.RawContextValue{
		Name: name, Value: body, Provenance: action.ProvenanceOperatorBound, Available: true,
	}, nil
}

func (g *Gateway) evidence(
	ctx context.Context,
	snapshot PolicySnapshot,
	request action.Request,
	tool action.Tool,
) (EvidenceSnapshot, error) {
	if g.config.EvidenceProvider == nil {
		if tool.Effect.Kind == action.EffectRepositoryRead || tool.Effect.Kind == action.EffectRepositoryWrite {
			return EvidenceSnapshot{}, fmt.Errorf("repository-effect evidence provider is unavailable")
		}
		return EvidenceSnapshot{
			Taint: action.TaintSnapshot{Status: action.TaintClean, Identity: "taint-clean"},
		}, nil
	}
	evidence, err := g.config.EvidenceProvider.Observe(ctx, snapshot, request, tool)
	if err != nil {
		return EvidenceSnapshot{}, err
	}
	if evidence.RepositoryEffect != nil {
		if err := g.bindRepositoryEffect(
			request, tool, evidence.RepositoryEffect, evidence.RepositoryPaths,
		); err != nil {
			return EvidenceSnapshot{}, err
		}
	}
	if !evidence.Taint.Status.Valid() || evidence.Taint.Identity == "" {
		return EvidenceSnapshot{}, fmt.Errorf("gateway evidence taint is invalid")
	}
	if (tool.Effect.Kind == action.EffectRepositoryRead || tool.Effect.Kind == action.EffectRepositoryWrite) &&
		evidence.RepositoryEffect == nil {
		return EvidenceSnapshot{}, fmt.Errorf("repository-effect evidence is unavailable")
	}
	if tool.Effect.Kind != action.EffectRepositoryRead && tool.Effect.Kind != action.EffectRepositoryWrite &&
		len(evidence.RepositoryPaths) != 0 {
		return EvidenceSnapshot{}, fmt.Errorf("non-repository evidence contains repository path bindings")
	}
	return evidence, nil
}

func (g *Gateway) bindRepositoryEffect(
	request action.Request,
	tool action.Tool,
	effect *action.RepositoryEffectCandidate,
	bindings []RepositoryPathBinding,
) error {
	if effect == nil || request.Arguments == nil || !effect.Decision.Valid() ||
		!effect.Reason.Valid() || len(bindings) == 0 {
		return fmt.Errorf("repository-effect evidence is invalid")
	}
	if err := validateRepositoryPathBindings(g.snapshot.Repository, bindings); err != nil {
		return err
	}
	ruleIDs := append([]string(nil), effect.RuleIDs...)
	sort.Strings(ruleIDs)
	pathBindings := cloneRepositoryPathBindings(bindings)
	sort.Slice(pathBindings, func(i, j int) bool {
		if pathBindings[i].Lexical == pathBindings[j].Lexical {
			return pathBindings[i].Identity < pathBindings[j].Identity
		}
		return pathBindings[i].Lexical < pathBindings[j].Lexical
	})
	parts := make([][]byte, 0, len(ruleIDs)+len(pathBindings)*2+7)
	arguments, err := request.Arguments.MarshalJSON()
	if err != nil {
		return fmt.Errorf("canonicalize repository-effect arguments: %w", err)
	}
	parts = append(
		parts,
		[]byte("gateway-effect"),
		[]byte(request.ToolContractDigest),
		[]byte(tool.ID),
		arguments,
		[]byte(effect.Decision),
		[]byte(effect.Reason),
	)
	for index, ruleID := range ruleIDs {
		if !action.SafeLabel(ruleID) || index > 0 && ruleIDs[index-1] == ruleID {
			return fmt.Errorf("repository-effect rule identities are invalid")
		}
		parts = append(parts, []byte(ruleID))
	}
	for _, binding := range pathBindings {
		parts = append(parts, []byte(binding.Lexical), []byte(binding.Identity))
	}
	if effect.Complete {
		parts = append(parts, []byte("complete"))
	} else {
		parts = append(parts, []byte("incomplete"))
	}
	effect.RuleIDs = ruleIDs
	effect.Identity = g.lease.Key.Identity(actionstate.DomainRepository, parts...)
	return nil
}

func cloneRepositoryPathBindings(bindings []RepositoryPathBinding) []RepositoryPathBinding {
	return append([]RepositoryPathBinding(nil), bindings...)
}

func validateRepositoryPathBindings(
	repository string,
	bindings []RepositoryPathBinding,
) error {
	if len(bindings) == 0 || repository == "" {
		return fmt.Errorf("repository path bindings are unavailable")
	}
	seen := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		if !filepath.IsAbs(binding.Lexical) || filepath.Clean(binding.Lexical) != binding.Lexical ||
			!filepath.IsAbs(binding.Identity) || filepath.Clean(binding.Identity) != binding.Identity ||
			!pathWithinRepository(repository, binding.Lexical) ||
			!pathWithinRepository(repository, binding.Identity) {
			return fmt.Errorf("repository path binding is outside its canonical repository")
		}
		key := binding.Lexical + "\x00" + binding.Identity
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("repository path binding is duplicated")
		}
		seen[key] = struct{}{}
		resolved, err := pathidentity.ResolveProspective(binding.Lexical)
		if err != nil || resolved != binding.Identity {
			return fmt.Errorf("repository path identity changed during evidence collection")
		}
	}
	return nil
}

func pathWithinRepository(repository, path string) bool {
	relative, err := filepath.Rel(repository, path)
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (g *Gateway) evaluationInput(
	snapshot PolicySnapshot,
	request action.Request,
	budget action.BudgetSnapshot,
	approval action.ApprovalSnapshot,
	evidence EvidenceSnapshot,
) action.EvaluationInput {
	input := action.EvaluationInput{
		Request:            request,
		SourceIdentity:     snapshot.SourceDigest,
		ContextIdentity:    g.boundContext.ContextIdentity,
		ExecutableDigest:   g.server.ExecutableDigest,
		Principal:          g.boundContext.Principal,
		CredentialLabels:   credentialLabels(g.boundContext.Credentials),
		Budget:             budget,
		Approval:           approval,
		Taint:              evidence.Taint,
		RepositoryEffect:   cloneRepositoryEffect(evidence.RepositoryEffect),
		Lifecycle:          action.LifecycleActive,
		CachePolicyVersion: action.CacheIdentityVersion,
	}
	input.ResampledIdentities = snapshot.Evaluator.IdentitySnapshot(input)
	return input
}

func cloneRepositoryEffect(input *action.RepositoryEffectCandidate) *action.RepositoryEffectCandidate {
	if input == nil {
		return nil
	}
	out := *input
	out.RuleIDs = append([]string(nil), input.RuleIDs...)
	return &out
}

func credentialLabels(credentials []actionstate.CredentialBinding) []string {
	labels := make([]string, len(credentials))
	for index, credential := range credentials {
		labels[index] = credential.Label
	}
	return labels
}

func (g *Gateway) evaluate(
	evaluator *action.Evaluator,
	input action.EvaluationInput,
) (action.EvaluationResult, bool) {
	g.toolsMu.RLock()
	var cache *action.DecisionCache
	if g.published != nil {
		cache = g.published.cache
	}
	g.toolsMu.RUnlock()
	prepared := evaluator.Prepare(input)
	if cache != nil {
		if cached, ok, _ := cache.LookupPrepared(prepared); ok {
			return cached, true
		}
	}
	started := time.Now()
	result := prepared.Evaluate()
	if time.Since(started) > EvaluationTimeout {
		return gatewayFailureResult(input, action.ReasonDeadlineExceeded), false
	}
	if cache != nil {
		cache.StorePrepared(prepared, result)
	}
	return result, false
}

func gatewayFailureResult(input action.EvaluationInput, reason action.ReasonCode) action.EvaluationResult {
	completeness := phaseIncomplete(input.Request.Completeness, reason)
	return action.EvaluationResult{
		Decision: action.DecisionBlock, Reason: reason,
		MatchedRuleIDs: []string{}, Candidates: []action.Candidate{},
		BudgetCandidates: append([]action.BudgetCandidate(nil), input.Budget.Candidates...),
		Trace:            []action.TraceEntry{}, TraceComplete: true,
		Completeness: completeness,
		PolicyDigest: input.Request.PolicyDigest, LockDigest: input.Request.LockDigest,
		PlanIdentity: input.ResampledIdentities.PlanIdentity, SourceIdentity: input.SourceIdentity,
		Cache:        action.CacheResult{Reason: action.CacheFailureResult},
		PhaseOutcome: action.OutcomeFor(input.Request.Phase, action.DecisionBlock),
		Failure:      &action.Failure{Code: reason, Message: "gateway evidence failed closed"},
		Inspection:   input.Inspection,
	}
}

func phaseIncomplete(value action.Completeness, reason action.ReasonCode) action.Completeness {
	value.PhaseComplete = false
	value.Missing = append(
		value.Missing,
		action.MissingEvidence{Field: action.EvidencePhase, Reason: reason},
	)
	normalized, err := action.NormalizeCompleteness(value)
	if err == nil {
		return normalized
	}
	fallback := action.CompleteEvidence()
	fallback.PhaseComplete = false
	fallback.Missing = []action.MissingEvidence{{Field: action.EvidencePhase, Reason: reason}}
	return fallback
}

func samePolicy(left, right PolicySnapshot) bool {
	return left.Repository == right.Repository &&
		left.SourceDigest == right.SourceDigest &&
		left.LockDigest == right.LockDigest &&
		left.Evaluator != nil && right.Evaluator != nil &&
		left.Evaluator.PlanIdentity() == right.Evaluator.PlanIdentity()
}

func (g *Gateway) resampleCallBoundary(
	ctx context.Context,
	snapshot PolicySnapshot,
	contract ToolContract,
	generation uint64,
	repositoryPaths []RepositoryPathBinding,
) error {
	fresh, err := g.freshSnapshot(ctx)
	if err != nil || !samePolicy(snapshot, fresh) {
		return fmt.Errorf("action policy changed before dispatch")
	}
	server, err := actionstate.ObserveServer(
		g.lease.Key,
		g.server.ExecutablePath,
		g.config.Arguments,
		g.server.WorkingDirectory,
		g.bindings,
	)
	if err != nil || !reflect.DeepEqual(server, g.server) {
		return fmt.Errorf("downstream server identity changed before dispatch")
	}
	if !g.generationCurrent(generation, contract) {
		return fmt.Errorf("downstream tool contract changed before dispatch")
	}
	for _, binding := range repositoryPaths {
		resolved, err := pathidentity.ResolveProspective(binding.Lexical)
		if err != nil || resolved != binding.Identity {
			return fmt.Errorf("repository path identity changed before dispatch")
		}
	}
	return nil
}

func gatewayReason(err error, fallback action.ReasonCode) action.ReasonCode {
	if errors.Is(err, context.Canceled) {
		return action.ReasonCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return action.ReasonDeadlineExceeded
	}
	var stateErr *actionstate.StateError
	if errors.As(err, &stateErr) && stateErr.Code.Valid() {
		return stateErr.Code
	}
	var approvalErr *actionapproval.ApprovalError
	if errors.As(err, &approvalErr) && approvalErr.Code.Valid() {
		return approvalErr.Code
	}
	return fallback
}
