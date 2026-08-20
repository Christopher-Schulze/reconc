package impactlab

import (
	"fmt"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestActionDeltasClassifyEveryDecisionAndPhaseTransition(t *testing.T) {
	decisions := []action.Decision{
		action.DecisionAllow,
		action.DecisionWarn,
		action.DecisionRequireApproval,
		action.DecisionBlock,
	}
	outcomes := []action.PhaseOutcome{
		action.OutcomeDispatchEligible,
		action.OutcomeDispatchBlocked,
	}
	for _, currentDecision := range decisions {
		for _, candidateDecision := range decisions {
			for _, currentOutcome := range outcomes {
				for _, candidateOutcome := range outcomes {
					name := fmt.Sprintf("%s_to_%s/%s_to_%s", currentDecision, candidateDecision, currentOutcome, candidateOutcome)
					t.Run(name, func(t *testing.T) {
						current := ActionObservation{Outcome: ActionAssertion{
							Decision: currentDecision, PhaseOutcome: currentOutcome,
						}}
						candidate := ActionObservation{Outcome: ActionAssertion{
							Decision: candidateDecision, PhaseOutcome: candidateOutcome,
						}}
						want := expectedDecisionAndPhaseDeltas(
							currentDecision, candidateDecision, currentOutcome, candidateOutcome,
						)
						if got := actionDeltas(current, candidate); !slicesEqualActionDelta(got, want) {
							t.Fatalf("actionDeltas() = %v, want %v", got, want)
						}
					})
				}
			}
		}
	}
}

func TestWarnAndApprovalPhaseChangesDoNotEnterBlockReviewAccounting(t *testing.T) {
	tests := []struct {
		name      string
		candidate action.Decision
		semantic  ActionDeltaKind
	}{
		{name: "warn", candidate: action.DecisionWarn, semantic: DeltaNewlyWarned},
		{name: "approval", candidate: action.DecisionRequireApproval, semantic: DeltaNewlyApprovalRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			comparison := ActionComparison{
				Current: ActionObservation{Outcome: ActionAssertion{
					Decision: action.DecisionAllow, PhaseOutcome: action.OutcomeDispatchEligible,
				}},
				Candidate: ActionObservation{Outcome: ActionAssertion{
					Decision: test.candidate, PhaseOutcome: action.OutcomeDispatchBlocked,
				}},
			}
			comparison.Deltas = actionDeltas(comparison.Current, comparison.Candidate)
			want := []ActionDeltaKind{DeltaDecision, test.semantic, DeltaPhaseOutcome}
			if !slicesEqualActionDelta(comparison.Deltas, want) {
				t.Fatalf("deltas = %v, want %v", comparison.Deltas, want)
			}
			report := Report{Cases: []CaseComparison{{
				ID: "case-" + test.name, Kind: CaseActionPre, Action: &comparison,
			}}}
			addActionSummary(&report.Summary, comparison)
			initializeDeltaGate(&report)
			if !report.DeltaGate.Passed || report.DeltaGate.RequiredCount != 0 ||
				report.Summary.NewlyBlockedActionCases != 0 {
				t.Fatalf("review accounting = summary:%+v gate:%+v", report.Summary, report.DeltaGate)
			}
			if test.candidate == action.DecisionWarn && report.Summary.NewlyWarnedActionCases != 1 {
				t.Fatalf("warn summary = %+v", report.Summary)
			}
			if test.candidate == action.DecisionRequireApproval && report.Summary.NewlyApprovalRequiredActionCases != 1 {
				t.Fatalf("approval summary = %+v", report.Summary)
			}
		})
	}
}

func expectedDecisionAndPhaseDeltas(
	current action.Decision,
	candidate action.Decision,
	currentOutcome action.PhaseOutcome,
	candidateOutcome action.PhaseOutcome,
) []ActionDeltaKind {
	deltas := []ActionDeltaKind{}
	if current != candidate {
		deltas = append(deltas, DeltaDecision)
		if candidate.Strength() < current.Strength() {
			deltas = append(deltas, DeltaNewlyAllowed)
		}
		switch candidate {
		case action.DecisionWarn:
			deltas = append(deltas, DeltaNewlyWarned)
		case action.DecisionRequireApproval:
			deltas = append(deltas, DeltaNewlyApprovalRequired)
		case action.DecisionBlock:
			deltas = append(deltas, DeltaNewlyBlocked)
		}
	}
	if currentOutcome != candidateOutcome {
		deltas = append(deltas, DeltaPhaseOutcome)
	}
	return deltas
}
