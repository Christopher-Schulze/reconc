package impactlab

import (
	"fmt"
	"slices"
	"sort"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
)

// Compare evaluates every corpus case against the fresh current policy and one
// already compiled in-memory candidate.
func Compare(repoRoot string, corpus Corpus, candidate Candidate, current, evaluator *runtime.CompiledPolicyEvaluator) (Report, error) {
	if err := validateCorpus(corpus); err != nil {
		return Report{}, err
	}
	if current == nil || evaluator == nil {
		return Report{}, fmt.Errorf("current and candidate evaluators are required")
	}
	currentActions, err := current.ActionRuntime()
	if err != nil {
		return Report{}, fmt.Errorf("prepare current action runtime: %w", err)
	}
	candidateActions, err := evaluator.ActionRuntime()
	if err != nil {
		return Report{}, fmt.Errorf("prepare candidate action runtime: %w", err)
	}
	if err := validateCandidateActionIdentity(candidate, candidateActions); err != nil {
		return Report{}, err
	}
	if candidate.RuleCount != len(evaluator.RuleIDs()) {
		return Report{}, fmt.Errorf("candidate metadata does not match its compiled repository runtime")
	}
	currentScripts := current.RequireScriptRuleIDs()
	candidateScripts := evaluator.RequireScriptRuleIDs()
	if len(currentScripts) > 0 || len(candidateScripts) > 0 {
		return Report{}, fmt.Errorf(
			"side-effect-free replay refuses require_script rules (current=%s, candidate=%s)",
			stringListText(currentScripts), stringListText(candidateScripts),
		)
	}
	report := Report{
		FormatVersion: ReportFormatVersion, CorpusID: corpus.CorpusID,
		CorpusCompleteness: corpus.Completeness, Candidate: candidate,
		Cases: []CaseComparison{}, Rules: []RuleImpact{}, CorpusUnmatchedRules: []string{},
		ActionRules: []RuleImpact{}, ActionCorpusUnmatchedRules: []string{},
		DeltaGate: DeltaGate{UnreviewedCases: []string{}},
	}
	currentMatches, candidateMatches := map[string]int{}, map[string]int{}
	currentActionMatches, candidateActionMatches := map[string]int{}, map[string]int{}
	for _, replayCase := range corpus.Cases {
		identity, identityErr := caseIdentity(replayCase)
		if identityErr != nil {
			return Report{}, identityErr
		}
		comparison := CaseComparison{ID: replayCase.ID, Kind: replayCase.Kind, CaseIdentity: identity}
		switch replayCase.Kind {
		case CaseRepository:
			repository, currentTrace, candidateTrace, compareErr := compareRepositoryCase(repoRoot, *replayCase.Repository, current, evaluator)
			if compareErr != nil {
				return Report{}, fmt.Errorf("impact case %q: %w", replayCase.ID, compareErr)
			}
			comparison.Repository = &repository
			addRuleMatches(currentMatches, currentTrace)
			addRuleMatches(candidateMatches, candidateTrace)
			addRepositorySummary(&report.Summary, repository)
		case CaseActionPre, CaseActionPost:
			actionComparison, compareErr := compareActionCase(replayCase, currentActions, candidateActions)
			if compareErr != nil {
				return Report{}, fmt.Errorf("impact case %q: %w", replayCase.ID, compareErr)
			}
			comparison.Action = &actionComparison
			addActionRuleMatches(currentActionMatches, actionComparison.Current)
			addActionRuleMatches(candidateActionMatches, actionComparison.Candidate)
			addActionSummary(&report.Summary, actionComparison)
		default:
			return Report{}, fmt.Errorf("impact case %q has unsupported kind %q", replayCase.ID, replayCase.Kind)
		}
		report.Cases = append(report.Cases, comparison)
	}
	report.Rules = buildRuleImpacts(evaluator.RuleIDs(), currentMatches, candidateMatches)
	for _, impact := range report.Rules {
		if impact.CandidateMatches == 0 {
			report.CorpusUnmatchedRules = append(report.CorpusUnmatchedRules, impact.RuleID)
		}
	}
	report.ActionRules = buildRuleImpacts(candidateActions.Evaluator.RuleIDs(), currentActionMatches, candidateActionMatches)
	for _, impact := range report.ActionRules {
		if impact.CandidateMatches == 0 {
			report.ActionCorpusUnmatchedRules = append(report.ActionCorpusUnmatchedRules, impact.RuleID)
		}
	}
	initializeDeltaGate(&report)
	report.SafetyConclusion = safetyConclusion(corpus.Completeness)
	return report, nil
}

func compareRepositoryCase(repoRoot string, replayCase RepositoryCase, current, candidate *runtime.CompiledPolicyEvaluator) (RepositoryComparison, runtime.EvaluationTrace, runtime.EvaluationTrace, error) {
	currentReport, currentCost, currentTrace, err := current.CheckWithTrace(repoRoot, replayCase.Inputs)
	if err != nil {
		return RepositoryComparison{}, runtime.EvaluationTrace{}, runtime.EvaluationTrace{}, err
	}
	candidateReport, candidateCost, candidateTrace, err := candidate.CheckWithTrace(repoRoot, replayCase.Inputs)
	if err != nil {
		return RepositoryComparison{}, runtime.EvaluationTrace{}, runtime.EvaluationTrace{}, err
	}
	currentActions, currentRedactions := sanitizeActions(repoRoot, currentReport.Actions)
	candidateActions, candidateRedactions := sanitizeActions(repoRoot, candidateReport.Actions)
	comparison := RepositoryComparison{
		CurrentDecision: currentReport.Decision, CandidateDecision: candidateReport.Decision,
		DecisionChanged: currentReport.Decision != candidateReport.Decision,
		CurrentActions:  currentActions, CandidateActions: candidateActions,
		ActionChanged:        !slices.Equal(currentActions, candidateActions),
		ActionRedactionCount: currentRedactions + candidateRedactions,
		Cost:                 CostDelta{Current: currentCost, Candidate: candidateCost, EstimatedUnits: candidateCost.EstimatedUnits - currentCost.EstimatedUnits},
	}
	comparison.NewlyBlockingRules, comparison.NewlyWarningRules, comparison.ResolvedRules =
		violationDeltas(currentReport.Violations, candidateReport.Violations)
	return comparison, currentTrace, candidateTrace, nil
}

func violationDeltas(current, candidate []runtime.Violation) ([]string, []string, []string) {
	currentModes := violationModes(current)
	candidateModes := violationModes(candidate)
	blocking, warning, resolved := []string{}, []string{}, []string{}
	for id, mode := range candidateModes {
		previous, existed := currentModes[id]
		if mode.IsBlocking() && (!existed || !previous.IsBlocking()) {
			blocking = append(blocking, id)
		}
		if mode == policy.ModeWarn && (!existed || previous != policy.ModeWarn) {
			warning = append(warning, id)
		}
	}
	for id := range currentModes {
		if _, exists := candidateModes[id]; !exists {
			resolved = append(resolved, id)
		}
	}
	sort.Strings(blocking)
	sort.Strings(warning)
	sort.Strings(resolved)
	return blocking, warning, resolved
}

func violationModes(violations []runtime.Violation) map[string]policy.Mode {
	modes := make(map[string]policy.Mode, len(violations))
	for _, violation := range violations {
		modes[violation.RuleID] = violation.Mode
	}
	return modes
}

func addRuleMatches(matches map[string]int, trace runtime.EvaluationTrace) {
	for _, ruleID := range trace.MatchedRuleIDs {
		matches[ruleID]++
	}
}

func buildRuleImpacts(candidateRuleIDs []string, current, candidate map[string]int) []RuleImpact {
	set := map[string]struct{}{}
	for _, id := range candidateRuleIDs {
		set[id] = struct{}{}
	}
	for id := range current {
		set[id] = struct{}{}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	impacts := make([]RuleImpact, 0, len(ids))
	for _, id := range ids {
		impacts = append(impacts, RuleImpact{
			RuleID: id, CurrentMatches: current[id], CandidateMatches: candidate[id],
			MatchDelta: candidate[id] - current[id],
		})
	}
	return impacts
}

func addRepositorySummary(summary *Summary, comparison RepositoryComparison) {
	summary.CaseCount++
	if comparison.DecisionChanged {
		summary.DecisionChanges++
	}
	if len(comparison.NewlyBlockingRules) > 0 {
		summary.NewlyBlockingCases++
	}
	if len(comparison.NewlyWarningRules) > 0 {
		summary.NewlyWarningCases++
	}
	summary.ResolvedViolationCount += len(comparison.ResolvedRules)
	if comparison.ActionChanged {
		summary.ActionChanges++
	}
	summary.CurrentEstimatedUnits += comparison.Cost.Current.EstimatedUnits
	summary.CandidateEstimatedUnits += comparison.Cost.Candidate.EstimatedUnits
	summary.EstimatedUnitsDelta = summary.CandidateEstimatedUnits - summary.CurrentEstimatedUnits
}

func safetyConclusion(completeness Completeness) string {
	if completeness.CompleteReplay {
		return "Complete declared repository and action coverage replay; results still describe only this corpus and are not live-host proof or proof that an unmatched rule is dead or safe."
	}
	return "Incomplete replay; missing repository or action dimensions and redactions prevent safety, dead-rule, live-host, or full-impact conclusions. Unmatched means only unmatched in this corpus."
}
