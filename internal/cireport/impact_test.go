package cireport

import (
	"bytes"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/impactlab"
	"reconc.dev/reconc/internal/runtime"
)

func TestFromImpactRepresentsEveryActionCaseInEveryCINativeFormat(t *testing.T) {
	report := impactReportWithCases(1)
	model := FromImpact("test", report)
	if model.Decision != "pass" || len(model.Findings) != 1 || model.Findings[0].CaseID != "case-0000" ||
		model.Findings[0].Mode != "unchanged" {
		t.Fatalf("unchanged impact model = %+v", model)
	}
	for _, format := range []Format{FormatJUnit, FormatSARIF, FormatGitHub} {
		first, err := Render(format, model)
		if err != nil {
			t.Fatal(err)
		}
		second, err := Render(format, model)
		if err != nil || !bytes.Equal(first, second) || !bytes.Contains(first, []byte("case-0000")) || len(first) > MaxBytes {
			t.Fatalf("%s impact output is unstable, unbounded, or missing case id: %v\n%s", format, err, first)
		}
	}

	report.Summary.ActionPhaseOutcomeChanges = 1
	if changed := FromImpact("test", report); changed.Decision != "warn" {
		t.Fatalf("phase-only impact decision = %q", changed.Decision)
	}
}

func TestFromImpactCapsCombinedFindingsExactly(t *testing.T) {
	report := impactReportWithCases(maxFindings + 1)
	model := FromImpact("test", report)
	if len(model.Findings) != maxFindings || model.TruncatedFindings != 1 {
		t.Fatalf("bounded impact findings = %d, truncated=%d", len(model.Findings), model.TruncatedFindings)
	}
}

func TestFromImpactNeverTruncatesLateGateErrorBehindNonErrors(t *testing.T) {
	report := impactReportWithCases(maxFindings + 1)
	last := report.Cases[len(report.Cases)-1].Action
	last.Deltas = []impactlab.ActionDeltaKind{
		impactlab.DeltaDecision, impactlab.DeltaNewlyAllowed,
	}
	report.DeltaGate = impactlab.DeltaGate{
		Passed: false, RequiredCount: 1,
		UnreviewedCases: []string{report.Cases[len(report.Cases)-1].ID},
	}
	model := FromImpact("test", report)
	if model.Decision != "block" || len(model.Findings) != maxFindings ||
		model.TruncatedFindings != 2 || !strings.Contains(model.Summary, "unreviewed 1") {
		t.Fatalf("bounded late gate model = %+v", model)
	}
	found := false
	for _, finding := range model.Findings {
		if finding.CaseID == report.Cases[len(report.Cases)-1].ID &&
			finding.DeltaKind == string(impactlab.DeltaNewlyAllowed) &&
			finding.Level == "error" {
			found = true
		}
	}
	if !found {
		t.Fatal("late gate error was truncated behind non-error findings")
	}
}

func TestFromImpactRepresentsRepositoryCasesAndExactReviewState(t *testing.T) {
	report := impactReportWithCases(0)
	report.Cases = []impactlab.CaseComparison{{
		ID: "repository-case", Kind: impactlab.CaseRepository,
		Repository: &impactlab.RepositoryComparison{
			CurrentDecision: runtime.DecisionPass, CandidateDecision: runtime.DecisionPass,
			CurrentActions: []string{}, CandidateActions: []string{},
		},
	}}
	model := FromImpact("test", report)
	if len(model.Findings) != 1 || model.Findings[0].CaseID != "repository-case" || model.Findings[0].Mode != "unchanged" {
		t.Fatalf("unchanged repository model = %+v", model)
	}
	report.Cases[0].Repository.ActionChanged = true
	report.Summary.ActionChanges = 1
	model = FromImpact("test", report)
	if model.Decision != "warn" || len(model.Findings) != 1 || model.Findings[0].Mode != "action_changed" {
		t.Fatalf("changed repository model = %+v", model)
	}

	report = impactReportWithCases(1)
	report.Cases[0].Action.Deltas = []impactlab.ActionDeltaKind{impactlab.DeltaReason}
	report.Summary.ActionReasonChanges = 1
	nonGated := FromImpact("test", report)
	if len(nonGated.Findings) != 1 || nonGated.Findings[0].ReviewRequired || nonGated.Findings[0].Reviewed {
		t.Fatalf("non-gated review state = %+v", nonGated.Findings)
	}
	github, err := Render(FormatGitHub, nonGated)
	if err != nil || !bytes.Contains(github, []byte("| n/a |")) {
		t.Fatalf("non-gated GitHub review = %v\n%s", err, github)
	}

	report.Cases[0].Action.Deltas = []impactlab.ActionDeltaKind{impactlab.DeltaNewlyAllowed}
	report.DeltaGate = impactlab.DeltaGate{Passed: false, RequiredCount: 1, UnreviewedCases: []string{"case-0000"}}
	gated := FromImpact("test", report)
	if gated.Decision != "block" || len(gated.Findings) != 1 || !gated.Findings[0].ReviewRequired ||
		gated.Findings[0].Reviewed || gated.Findings[0].Level != "error" {
		t.Fatalf("gated review state = %+v", gated)
	}
	github, err = Render(FormatGitHub, gated)
	if err != nil || !bytes.Contains(github, []byte("| unreviewed |")) {
		t.Fatalf("gated GitHub review = %v\n%s", err, github)
	}

	report.Cases[0].Action.Deltas = []impactlab.ActionDeltaKind{
		impactlab.DeltaNewlyAllowed, impactlab.DeltaReason,
	}
	report.Cases[0].Action.Reviewed = true
	report.DeltaGate = impactlab.DeltaGate{Passed: true, RequiredCount: 1, ReviewedCount: 1, UnreviewedCases: []string{}}
	mixed := FromImpact("test", report)
	if len(mixed.Findings) != 2 {
		t.Fatalf("mixed action findings = %+v", mixed.Findings)
	}
	for _, finding := range mixed.Findings {
		if finding.DeltaKind == string(impactlab.DeltaReason) && (finding.ReviewRequired || finding.Reviewed) {
			t.Fatalf("non-gated collateral delta inherited review state: %+v", finding)
		}
	}
}

func impactReportWithCases(count int) impactlab.Report {
	assertion := impactlab.ActionAssertion{
		Decision: action.DecisionAllow, Reason: action.ReasonDeclaredTool, ToolID: "tool",
		MatchedRuleIDs: []string{}, Cache: impactlab.ActionCacheAssertion{Eligible: true, Reason: action.CacheEligible},
		Completeness: action.CompleteEvidence(), PhaseOutcome: action.OutcomeDispatchEligible,
	}
	cases := make([]impactlab.CaseComparison, count)
	for index := range cases {
		id := "case-" + fixedCIReportDecimal(index)
		observation := impactlab.ActionObservation{
			Outcome: assertion, Trace: []action.TraceEntry{}, TraceComplete: true,
			Identity: "sha256:" + strings.Repeat("a", 64),
		}
		cases[index] = impactlab.CaseComparison{
			ID: id, Kind: impactlab.CaseActionPre, CaseIdentity: "sha256:" + strings.Repeat("b", 64),
			Action: &impactlab.ActionComparison{
				Current: observation, Candidate: observation, Deltas: []impactlab.ActionDeltaKind{}, Reviewed: false,
			},
		}
	}
	return impactlab.Report{
		FormatVersion: impactlab.ReportFormatVersion, CorpusID: "sha256:" + strings.Repeat("c", 64),
		Candidate: impactlab.Candidate{ActionPlanIdentity: "sha256:" + strings.Repeat("d", 64), LockDigest: strings.Repeat("e", 64)},
		Cases:     cases, DeltaGate: impactlab.DeltaGate{Passed: true, UnreviewedCases: []string{}},
		SafetyConclusion: "Offline action scenarios are complete only for declared cases.",
	}
}

func fixedCIReportDecimal(value int) string {
	digits := []byte{'0', '0', '0', '0'}
	for index := len(digits) - 1; index >= 0; index-- {
		digits[index] += byte(value % 10)
		value /= 10
	}
	return string(digits)
}
