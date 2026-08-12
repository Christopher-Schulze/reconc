package impactlab

import (
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestCompareCurrentReplaysExactActionExpectationsWithoutCandidateSource(t *testing.T) {
	repository, evaluator := makeActionImpactRepo(t, baseActionPolicyRules)
	fixture := newActionFixture(
		"current-pre-allow",
		CaseActionPre,
		`{"target":"staging","operation":"read"}`,
		actionAssertion(
			action.DecisionAllow,
			action.ReasonDeclaredTool,
			"database-write",
			nil,
			action.CacheEligible,
			action.OutcomeDispatchEligible,
			"",
		),
	)
	corpus, err := NewCorpus(repository, []Case{fixture}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	report, err := CompareCurrent(repository, corpus, evaluator)
	if err != nil {
		t.Fatal(err)
	}
	if report.Candidate.Kind != CandidateKindCurrent || report.Candidate.Name != "current-policy" ||
		report.Summary.ActionCaseCount != 1 || report.Summary.ActionDecisionChanges != 0 {
		t.Fatalf("current scenario report = %#v", report)
	}
	if _, err := Compare(repository, corpus, report.Candidate, evaluator, evaluator); err == nil {
		t.Fatal("ordinary candidate comparison accepted the current-evaluator sentinel")
	}
}
