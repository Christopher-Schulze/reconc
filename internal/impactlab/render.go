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
	fmt.Fprintf(&output, "Corpus: %s; complete=%t; missing=%s; redactions=%d.\n",
		report.CorpusID, report.CorpusCompleteness.CompleteReplay,
		eventClassText(report.CorpusCompleteness.MissingEventClasses),
		report.CorpusCompleteness.RedactionCount)
	fmt.Fprintf(&output, "Corpus-unmatched rules: %s.\n", stringListText(report.CorpusUnmatchedRules))
	fmt.Fprintln(&output, report.SafetyConclusion)
	return []byte(output.String())
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
