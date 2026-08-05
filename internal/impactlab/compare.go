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
	}
	currentMatches, candidateMatches := map[string]int{}, map[string]int{}
	for _, replayCase := range corpus.Cases {
		comparison, currentReport, proposed, err := compareCase(repoRoot, replayCase, current, evaluator)
		if err != nil {
			return Report{}, fmt.Errorf("impact case %q: %w", replayCase.ID, err)
		}
		report.Cases = append(report.Cases, comparison)
		addRuleMatches(currentMatches, currentReport)
		addRuleMatches(candidateMatches, proposed)
		addCaseSummary(&report.Summary, comparison)
	}
	report.Rules = buildRuleImpacts(evaluator.RuleIDs(), currentMatches, candidateMatches)
	for _, impact := range report.Rules {
		if impact.CandidateMatches == 0 {
			report.CorpusUnmatchedRules = append(report.CorpusUnmatchedRules, impact.RuleID)
		}
	}
	report.SafetyConclusion = safetyConclusion(corpus.Completeness)
	return report, nil
}

func compareCase(repoRoot string, replayCase Case, current, candidate *runtime.CompiledPolicyEvaluator) (CaseComparison, runtime.EvaluationTrace, runtime.EvaluationTrace, error) {
	currentReport, currentCost, currentTrace, err := current.CheckWithTrace(repoRoot, replayCase.Inputs)
	if err != nil {
		return CaseComparison{}, runtime.EvaluationTrace{}, runtime.EvaluationTrace{}, err
	}
	candidateReport, candidateCost, candidateTrace, err := candidate.CheckWithTrace(repoRoot, replayCase.Inputs)
	if err != nil {
		return CaseComparison{}, runtime.EvaluationTrace{}, runtime.EvaluationTrace{}, err
	}
	currentActions, currentRedactions := sanitizeActions(repoRoot, currentReport.Actions)
	candidateActions, candidateRedactions := sanitizeActions(repoRoot, candidateReport.Actions)
	comparison := CaseComparison{
		ID: replayCase.ID, CurrentDecision: currentReport.Decision, CandidateDecision: candidateReport.Decision,
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

func addCaseSummary(summary *Summary, comparison CaseComparison) {
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
		return "Complete declared event-class replay; results still describe only this corpus and are not proof that an unmatched rule is dead or safe."
	}
	return "Incomplete replay; missing or redacted event classes prevent safety, dead-rule, or full-impact conclusions. Unmatched means only unmatched in this corpus."
}
