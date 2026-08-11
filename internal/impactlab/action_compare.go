package impactlab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
)

func validateCandidateActionIdentity(candidate Candidate, compiled runtime.CompiledActionRuntime) error {
	if (candidate.Kind != string(policy.SourcePolicyFile) && candidate.Kind != string(policy.SourcePreset)) ||
		!validCaseID(candidate.Name) || unsafeActionMetadata(candidate.Name) ||
		candidate.SourceDigest != compiled.SourceDigest || candidate.LockDigest != compiled.LockDigest ||
		candidate.ActionPlanIdentity != compiled.Evaluator.PlanIdentity() ||
		candidate.ActionToolCount != compiled.ToolCount || candidate.ActionRuleCount != compiled.ActionRuleCount {
		return fmt.Errorf("candidate metadata does not match its compiled action runtime")
	}
	return nil
}

func compareActionCase(replayCase Case, current, candidate runtime.CompiledActionRuntime) (ActionComparison, error) {
	if replayCase.Action == nil {
		return ActionComparison{}, fmt.Errorf("action scenario is absent")
	}
	currentObservation, err := evaluateActionScenario(*replayCase.Action, current)
	if err != nil {
		return ActionComparison{}, err
	}
	if !equalActionAssertion(currentObservation.Outcome, replayCase.Action.Expected) {
		expected, expectedErr := json.Marshal(replayCase.Action.Expected)
		if expectedErr != nil {
			return ActionComparison{}, fmt.Errorf("encode expected action outcome: %w", expectedErr)
		}
		actual, actualErr := json.Marshal(currentObservation.Outcome)
		if actualErr != nil {
			return ActionComparison{}, fmt.Errorf("encode actual action outcome: %w", actualErr)
		}
		return ActionComparison{}, fmt.Errorf("current action outcome violates exact expectation: expected=%s actual=%s", expected, actual)
	}
	candidateObservation, err := evaluateActionScenario(*replayCase.Action, candidate)
	if err != nil {
		return ActionComparison{}, err
	}
	return ActionComparison{
		Current: currentObservation, Candidate: candidateObservation,
		Deltas: actionDeltas(currentObservation, candidateObservation),
	}, nil
}

func evaluateActionScenario(scenario ActionCase, compiled runtime.CompiledActionRuntime) (ActionObservation, error) {
	raw := actionRawRequest(scenario.Request, compiled.SourceDigest, compiled.LockDigest)
	input := action.EvaluationInput{
		SourceIdentity: compiled.SourceDigest, ContextIdentity: scenario.State.ContextIdentity,
		ExecutableDigest: scenario.State.ExecutableDigest,
		Principal:        scenario.State.Principal, CredentialLabels: append([]string(nil), scenario.State.CredentialLabels...),
		Budget:   scenario.State.Budget,
		Approval: scenario.State.Approval, Taint: scenario.State.Taint,
		Inspection: cloneFixtureInspection(scenario.State.Inspection),
		Lifecycle:  scenario.State.Lifecycle, CachePolicyVersion: scenario.State.CachePolicyVersion,
	}
	if scenario.State.RepositoryEffect != nil {
		effect := *scenario.State.RepositoryEffect
		effect.RuleIDs = append([]string(nil), effect.RuleIDs...)
		input.RepositoryEffect = &effect
	}
	request, err := action.NormalizeRequest(raw)
	if err == nil {
		input.Request = request
		input.ResampledIdentities = compiled.Evaluator.IdentitySnapshot(input)
		applyIdentityDrift(&input.ResampledIdentities, scenario.State.ResampleDrift)
	}
	result := compiled.Evaluator.EvaluateRaw(raw, input)
	var approval *ActionApprovalAssertion
	if scenario.Expected.Approval != nil {
		approval = &ActionApprovalAssertion{
			Status: scenario.State.Approval.Status, Identity: scenario.State.Approval.Identity,
			Transition: scenario.State.ApprovalTransition,
		}
		if result.Decision == action.DecisionRequireApproval {
			approval.RequiredApprovalIdentity = result.RequiredApprovalIdentity
		}
	}
	return observationFromResult(result, approval)
}

func observationFromResult(result action.EvaluationResult, approval *ActionApprovalAssertion) (ActionObservation, error) {
	failureCode := action.ReasonCode("")
	if result.Failure != nil {
		failureCode = result.Failure.Code
	}
	outcome := ActionAssertion{
		Decision: result.Decision, Reason: result.Reason, ToolID: result.ToolID,
		MatchedRuleIDs: append([]string{}, result.MatchedRuleIDs...),
		Cache:          ActionCacheAssertion{Eligible: result.Cache.Eligible, Reason: result.Cache.Reason},
		Completeness:   result.Completeness, PhaseOutcome: result.PhaseOutcome, FailureCode: failureCode,
		Approval: approval,
	}
	outcome.Completeness.Missing = append([]action.MissingEvidence{}, result.Completeness.Missing...)
	identity, err := actionAssertionIdentity(outcome)
	if err != nil {
		return ActionObservation{}, err
	}
	return ActionObservation{
		Outcome: outcome, Trace: append([]action.TraceEntry(nil), result.Trace...),
		TraceComplete: result.TraceComplete, TraceOmitted: result.TraceOmitted,
		Identity: identity,
	}, nil
}

func actionAssertionIdentity(assertion ActionAssertion) (string, error) {
	body, err := json.Marshal(assertion)
	if err != nil {
		return "", fmt.Errorf("encode action assertion identity: %w", err)
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func applyIdentityDrift(snapshot *action.IdentitySnapshot, components []ActionIdentityComponent) {
	for _, component := range components {
		switch component {
		case IdentityPlan:
			snapshot.PlanIdentity = driftSHAIdentity
		case IdentitySource:
			snapshot.SourceIdentity = driftDigest
		case IdentityPolicy:
			snapshot.PolicyDigest = driftDigest
		case IdentityLock:
			snapshot.LockDigest = driftDigest
		case IdentityAuthority:
			if snapshot.AuthorityMode == action.AuthorityOperatorPinned {
				snapshot.AuthorityMode = action.AuthorityRepositoryManaged
			} else {
				snapshot.AuthorityMode = action.AuthorityOperatorPinned
			}
		case IdentityServer:
			snapshot.ServerFingerprint = driftHMACIdentity
		case IdentityToolContract:
			snapshot.ToolContractDigest = driftSHAIdentity
		case IdentityExecutable:
			snapshot.ExecutableDigest = driftSHAIdentity
		case IdentityRepository:
			snapshot.RepositoryIdentity = driftHMACIdentity
		case IdentityContext:
			snapshot.ContextIdentity = "context-drift"
		case IdentityPrincipal:
			snapshot.Principal = "drifted"
		case IdentityCredentials:
			snapshot.CredentialLabels = []string{"drifted"}
		case IdentityState:
			snapshot.StateVersion = "state-drift"
		case IdentityBudget:
			snapshot.BudgetIdentity = "budget-drift"
		case IdentityReservation:
			snapshot.ReservationIdentity = "reservation-drift"
		case IdentityApproval:
			snapshot.ApprovalIdentity = "approval-drift"
		case IdentityTaint:
			snapshot.TaintIdentity = "taint-drift"
		case IdentityRepositoryEffect:
			snapshot.RepositoryEffectIdentity = "repository-effect-drift"
		case IdentityInspection:
			snapshot.InspectionIdentity = "inspection-drift"
		}
	}
}

func actionDeltas(current, candidate ActionObservation) []ActionDeltaKind {
	deltas := []ActionDeltaKind{}
	if current.Outcome.Decision != candidate.Outcome.Decision {
		deltas = append(deltas, DeltaDecision)
		currentPermits := actionOutcomePermits(current.Outcome.PhaseOutcome)
		candidatePermits := actionOutcomePermits(candidate.Outcome.PhaseOutcome)
		if candidate.Outcome.Decision.Strength() < current.Outcome.Decision.Strength() {
			deltas = append(deltas, DeltaNewlyAllowed)
		}
		switch candidate.Outcome.Decision {
		case action.DecisionWarn:
			deltas = append(deltas, DeltaNewlyWarned)
		case action.DecisionRequireApproval:
			deltas = append(deltas, DeltaNewlyApprovalRequired)
		}
		if candidate.Outcome.Decision == action.DecisionBlock || currentPermits && !candidatePermits {
			deltas = append(deltas, DeltaNewlyBlocked)
		}
	}
	if current.Outcome.Reason != candidate.Outcome.Reason {
		deltas = append(deltas, DeltaReason)
	}
	if current.Outcome.ToolID != candidate.Outcome.ToolID {
		deltas = append(deltas, DeltaToolIdentity)
	}
	if current.Outcome.FailureCode != candidate.Outcome.FailureCode {
		deltas = append(deltas, DeltaFailure)
	}
	if !slices.Equal(current.Outcome.MatchedRuleIDs, candidate.Outcome.MatchedRuleIDs) ||
		!slices.Equal(current.Trace, candidate.Trace) || current.TraceComplete != candidate.TraceComplete ||
		current.TraceOmitted != candidate.TraceOmitted {
		deltas = append(deltas, DeltaRuleTrace)
	}
	if current.Outcome.Cache != candidate.Outcome.Cache {
		deltas = append(deltas, DeltaCache)
	}
	if current.Outcome.PhaseOutcome != candidate.Outcome.PhaseOutcome {
		deltas = append(deltas, DeltaPhaseOutcome)
	}
	if !equalActionCompleteness(current.Outcome.Completeness, candidate.Outcome.Completeness) {
		deltas = append(deltas, DeltaCompleteness)
	}
	if !equalActionApprovalAssertion(current.Outcome.Approval, candidate.Outcome.Approval) {
		deltas = append(deltas, DeltaApproval)
	}
	return deltas
}

func actionOutcomePermits(outcome action.PhaseOutcome) bool {
	switch outcome {
	case action.OutcomeDispatchEligible, action.OutcomeDeliveryEligible,
		action.OutcomeProgressEligible, action.OutcomeRecorded:
		return true
	default:
		return false
	}
}

func equalActionAssertion(left, right ActionAssertion) bool {
	return left.Decision == right.Decision && left.Reason == right.Reason && left.ToolID == right.ToolID &&
		slices.Equal(left.MatchedRuleIDs, right.MatchedRuleIDs) && left.Cache == right.Cache &&
		equalActionCompleteness(left.Completeness, right.Completeness) &&
		left.PhaseOutcome == right.PhaseOutcome && left.FailureCode == right.FailureCode &&
		equalActionApprovalAssertion(left.Approval, right.Approval)
}

func equalActionApprovalAssertion(left, right *ActionApprovalAssertion) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Status == right.Status && left.Identity == right.Identity &&
		left.RequiredApprovalIdentity == right.RequiredApprovalIdentity && left.Transition == right.Transition
}

func addActionRuleMatches(matches map[string]int, observation ActionObservation) {
	for _, ruleID := range observation.Outcome.MatchedRuleIDs {
		matches[ruleID]++
	}
}

func addActionSummary(summary *Summary, comparison ActionComparison) {
	summary.ActionCaseCount++
	if comparison.Current.Outcome.Decision != comparison.Candidate.Outcome.Decision {
		summary.ActionDecisionChanges++
	}
	for _, delta := range comparison.Deltas {
		switch delta {
		case DeltaNewlyAllowed:
			summary.NewlyAllowedActionCases++
		case DeltaNewlyWarned:
			summary.NewlyWarnedActionCases++
		case DeltaNewlyApprovalRequired:
			summary.NewlyApprovalRequiredActionCases++
		case DeltaNewlyBlocked:
			summary.NewlyBlockedActionCases++
		case DeltaRuleTrace:
			summary.ActionRuleTraceChanges++
		case DeltaCache:
			summary.ActionCacheChanges++
		case DeltaPhaseOutcome:
			summary.ActionPhaseOutcomeChanges++
		case DeltaCompleteness:
			summary.ActionCompletenessChanges++
		case DeltaReason:
			summary.ActionReasonChanges++
		case DeltaToolIdentity:
			summary.ActionToolIdentityChanges++
		case DeltaFailure:
			summary.ActionFailureChanges++
		case DeltaApproval:
			summary.ActionApprovalChanges++
		}
	}
}

func initializeDeltaGate(report *Report) {
	seen := map[string]struct{}{}
	for index := range report.Cases {
		comparison := &report.Cases[index]
		if comparison.Action == nil {
			continue
		}
		for _, delta := range comparison.Action.Deltas {
			if delta != DeltaNewlyAllowed && delta != DeltaNewlyBlocked {
				continue
			}
			report.DeltaGate.RequiredCount++
			if _, duplicate := seen[comparison.ID]; !duplicate {
				seen[comparison.ID] = struct{}{}
				report.DeltaGate.UnreviewedCases = append(report.DeltaGate.UnreviewedCases, comparison.ID)
			}
		}
	}
	report.DeltaGate.Passed = report.DeltaGate.RequiredCount == 0
}

const (
	driftDigest       = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	driftSHAIdentity  = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	driftHMACIdentity = "hmac-sha256:v1:drift:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
)
