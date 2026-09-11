package actionstate

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

func TestFinalizeApprovalPersistsThroughDenialCapacityExhaustion(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"finalize-denial",
		action.BudgetLimits{CallCount: 8, DeniedCount: 1, ApprovalCount: 4},
		action.BudgetResetNever,
	)})
	firstInput, firstReserved := fixture.reserve(t, callID("a"))
	firstIssued := issueFixtureApproval(t, fixture, firstInput, firstReserved)
	first, err := fixture.store.FinalizeApproval(context.Background(), ApprovalFinalizeRequest{
		RequestState: firstIssued.issue.RequestState, ExpectedStateVersion: firstIssued.issue.StateVersion,
		Status: actionapproval.StatusCancelled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Persisted || first.DenialCapacityExhausted || first.Result.Status != actionapproval.StatusCancelled {
		t.Fatalf("first cancellation = %#v", first)
	}

	secondInput, secondReserved := fixture.reserve(t, callID("b"))
	secondIssued := issueFixtureApproval(t, fixture, secondInput, secondReserved)
	second, err := fixture.store.FinalizeApproval(context.Background(), ApprovalFinalizeRequest{
		RequestState: secondIssued.issue.RequestState, ExpectedStateVersion: secondIssued.issue.StateVersion,
		Status: actionapproval.StatusCancelled,
	})
	if !DenialCountCapacityExhausted(err) {
		t.Fatalf("second cancellation error = %v", err)
	}
	result, ok := second.TerminalResult()
	if !ok || !second.DenialCapacityExhausted || result.Status != actionapproval.StatusCancelled {
		t.Fatalf("exhausted cancellation = %#v", second)
	}

	recovered, err := fixture.store.FinalizeApproval(context.Background(), ApprovalFinalizeRequest{
		RequestState: secondIssued.issue.RequestState, ExpectedStateVersion: secondIssued.issue.StateVersion,
		Status: actionapproval.StatusCancelled,
	})
	if err != nil {
		t.Fatal(err)
	}
	recoveredResult, ok := recovered.TerminalResult()
	if !ok || recovered.DenialCapacityExhausted || recoveredResult.StateVersion != result.StateVersion ||
		recoveredResult.Status != actionapproval.StatusCancelled {
		t.Fatalf("recovered terminal approval = %#v", recovered)
	}

	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 0 || status.LiveReservations != 0 || status.TerminalCallCount != 2 ||
		status.Budgets[0].Consumed.DeniedCount != 1 {
		t.Fatalf("denial-capacity state = %#v", status)
	}
}

func TestFinalizeApprovalRejectsStalePendingVersionAndRecoversTerminalState(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"finalize-stale",
		action.BudgetLimits{CallCount: 4, DeniedCount: 4, ApprovalCount: 4},
		action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("c"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	stale, err := fixture.store.FinalizeApproval(context.Background(), ApprovalFinalizeRequest{
		RequestState: issued.issue.RequestState, ExpectedStateVersion: "invalid-issuance-version",
		Status: actionapproval.StatusCancelled,
	})
	requireStateCode(t, err, action.ReasonStateUnavailable)
	if stale.Persisted {
		t.Fatalf("stale pending finalization persisted: %#v", stale)
	}

	committed, err := fixture.store.FinalizeApproval(context.Background(), ApprovalFinalizeRequest{
		RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
		Status: actionapproval.StatusCancelled,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, ok := committed.TerminalResult()
	if !ok {
		t.Fatalf("committed cancellation = %#v", committed)
	}

	reopened, err := OpenStore(StoreOptions{
		Home: fixture.home, Repository: fixture.repository, KeyLease: fixture.lease,
		Clock: fixture.clock, OwnerID: fixture.store.ownerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := reopened.FinalizeApproval(context.Background(), ApprovalFinalizeRequest{
		RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
		Status: actionapproval.StatusUnavailable,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := recovered.TerminalResult()
	if !ok || got.Status != actionapproval.StatusCancelled || got.StateVersion != first.StateVersion {
		t.Fatalf("restart recovery = %#v, want %#v", recovered, first)
	}
}

func TestFinalizeApprovalPublishFailureDoesNotTerminalize(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"finalize-publish",
		action.BudgetLimits{CallCount: 2, DeniedCount: 2, ApprovalCount: 2},
		action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("d"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	publish := fixture.store.publish
	fixture.store.publish = func(string, []byte) error { return errors.New("simulated disk full") }
	failed, err := fixture.store.FinalizeApproval(context.Background(), ApprovalFinalizeRequest{
		RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
		Status: actionapproval.StatusCancelled,
	})
	if err == nil || failed.Persisted {
		t.Fatalf("publish failure outcome = %#v, err %v", failed, err)
	}
	fixture.store.publish = publish

	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 1 || status.LiveReservations != 1 || status.TerminalCallCount != 0 {
		t.Fatalf("failed publish mutated terminal state: %#v", status)
	}

	committed, err := fixture.store.FinalizeApproval(context.Background(), ApprovalFinalizeRequest{
		RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
		Status: actionapproval.StatusCancelled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := committed.TerminalResult(); !ok {
		t.Fatalf("retry after publish failure = %#v", committed)
	}
}

func TestFinalizeApprovalConcurrentTerminalizersHaveOneWinner(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"finalize-race",
		action.BudgetLimits{CallCount: 4, DeniedCount: 4, ApprovalCount: 4, Concurrent: 4},
		action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("e"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	outcomes := make([]ApprovalFinalizeOutcome, 8)
	errs := make([]error, 8)
	var wait sync.WaitGroup
	wait.Add(len(outcomes))
	for index := range outcomes {
		go func(index int) {
			defer wait.Done()
			outcomes[index], errs[index] = fixture.store.FinalizeApproval(context.Background(), ApprovalFinalizeRequest{
				RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion,
				Status: actionapproval.StatusCancelled,
			})
		}(index)
	}
	wait.Wait()

	var winner string
	for index, outcome := range outcomes {
		result, ok := outcome.TerminalResult()
		if !ok || errs[index] != nil {
			t.Fatalf("concurrent finalizer %d = %#v, err %v", index, outcome, errs[index])
		}
		if result.Status != actionapproval.StatusCancelled {
			t.Fatalf("concurrent finalizer %d status = %q", index, result.Status)
		}
		if winner == "" {
			winner = result.StateVersion
		} else if result.StateVersion != winner {
			t.Fatalf("concurrent finalizers observed mixed versions %q and %q", winner, result.StateVersion)
		}
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 0 || status.LiveReservations != 0 || status.TerminalCallCount != 1 {
		t.Fatalf("concurrent terminal state = %#v", status)
	}
}

func TestDenialCountCapacityExhaustedMatchesExactDiagnostic(t *testing.T) {
	if DenialCountCapacityExhausted(nil) ||
		DenialCountCapacityExhausted(stateError(action.ReasonBudgetExhausted, "approval-count capacity is exhausted", nil)) {
		t.Fatal("unrelated budget errors were classified as denial-count exhaustion")
	}
	err := stateError(action.ReasonBudgetExhausted, "denial-count capacity is exhausted", nil)
	if !DenialCountCapacityExhausted(err) || !strings.Contains(err.Error(), string(action.ReasonBudgetExhausted)) {
		t.Fatalf("denial-count error = %v", err)
	}
}
