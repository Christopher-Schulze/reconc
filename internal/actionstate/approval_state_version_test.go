package actionstate

import (
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestApprovalIssuanceRebindsLiveReservationToCurrentState(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"approval-concurrency",
		action.BudgetLimits{CallCount: 8, ApprovalCount: 8, Concurrent: 4},
		action.BudgetResetNever,
	)})
	firstInput, first := fixture.reserve(t, callID("p"))
	_, second := fixture.reserve(t, callID("q"))
	if first.Snapshot.StateVersion == second.Snapshot.StateVersion {
		t.Fatal("independent reservation did not advance action state")
	}

	issued := issueFixtureApproval(t, fixture, firstInput, first)
	if issued.issue.Request.StateVersion != second.Snapshot.StateVersion {
		t.Fatalf(
			"approval request state version = %q, want current %q",
			issued.issue.Request.StateVersion,
			second.Snapshot.StateVersion,
		)
	}
	if issued.issue.Request.BudgetReservationID != first.Reservation.Identity {
		t.Fatalf(
			"approval reservation = %q, want %q",
			issued.issue.Request.BudgetReservationID,
			first.Reservation.Identity,
		)
	}
}
