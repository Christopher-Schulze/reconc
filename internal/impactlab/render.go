package impactlab

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MarshalReport returns deterministic JSON with a trailing newline.
func MarshalReport(report Report) ([]byte, error) {
	if report.FormatVersion != ReportFormatVersion || report.CorpusID == "" {
		return nil, fmt.Errorf("impact report contract is incomplete")
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	if len(body) > MaxCorpusBytes {
		return nil, fmt.Errorf("impact report exceeds %d bytes", MaxCorpusBytes)
	}
	return body, nil
}

// RenderText emits a compact review surface without weakening the explicit
// incomplete-corpus language.
func RenderText(report Report) []byte {
	var output strings.Builder
	fmt.Fprintf(&output, "Policy impact: %d case(s), %d decision change(s), %d new blocking case(s), %d new warning case(s).\n",
		report.Summary.CaseCount, report.Summary.DecisionChanges,
		report.Summary.NewlyBlockingCases, report.Summary.NewlyWarningCases)
	fmt.Fprintf(&output, "Actions changed: %d; resolved violations: %d.\n",
		report.Summary.ActionChanges, report.Summary.ResolvedViolationCount)
	fmt.Fprintf(&output, "Evaluation units: %d -> %d (%+d).\n",
		report.Summary.CurrentEstimatedUnits, report.Summary.CandidateEstimatedUnits,
		report.Summary.EstimatedUnitsDelta)
	fmt.Fprintf(&output, "Action impact: %d case(s), %d decision change(s), allowed=%d, warned=%d, approval=%d, blocked=%d.\n",
		report.Summary.ActionCaseCount, report.Summary.ActionDecisionChanges,
		report.Summary.NewlyAllowedActionCases, report.Summary.NewlyWarnedActionCases,
		report.Summary.NewlyApprovalRequiredActionCases, report.Summary.NewlyBlockedActionCases)
	fmt.Fprintf(&output, "Action deltas: trace=%d, cache=%d, phase=%d, completeness=%d, reason=%d, tool=%d, failure=%d.\n",
		report.Summary.ActionRuleTraceChanges, report.Summary.ActionCacheChanges,
		report.Summary.ActionPhaseOutcomeChanges, report.Summary.ActionCompletenessChanges,
		report.Summary.ActionReasonChanges, report.Summary.ActionToolIdentityChanges,
		report.Summary.ActionFailureChanges)
	fmt.Fprintf(&output, "Corpus: %s; complete=%t; missing=%s; redactions=%d.\n",
		report.CorpusID, report.CorpusCompleteness.CompleteReplay,
		eventClassText(report.CorpusCompleteness.MissingEventClasses),
		report.CorpusCompleteness.RedactionCount)
	fmt.Fprintf(&output, "Corpus-unmatched rules: %s.\n", stringListText(report.CorpusUnmatchedRules))
	fmt.Fprintf(&output, "Action-corpus-unmatched rules: %s.\n", stringListText(report.ActionCorpusUnmatchedRules))
	fmt.Fprintf(&output, "Impact cases: %s.\n", comparisonIDText(report.Cases))
	fmt.Fprintf(&output, "Action delta gate: passed=%t; reviewed=%d/%d; unreviewed=%s.\n",
		report.DeltaGate.Passed, report.DeltaGate.ReviewedCount,
		report.DeltaGate.RequiredCount, stringListText(report.DeltaGate.UnreviewedCases))
	fmt.Fprintln(&output, report.SafetyConclusion)
	return []byte(output.String())
}

func comparisonIDText(values []CaseComparison) string {
	ids := make([]string, len(values))
	for index := range values {
		ids[index] = values[index].ID
	}
	return stringListText(ids)
}

func eventClassText(values []EventClass) string {
	if len(values) == 0 {
		return "none"
	}
	text := make([]string, len(values))
	for index, value := range values {
		text[index] = string(value)
	}
	return strings.Join(text, ",")
}

func stringListText(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}
