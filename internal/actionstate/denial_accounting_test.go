package actionstate

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

func TestApprovalDenialAccountingRetainsPartialMultiBudgetConsumption(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{
		storeBudget("first", action.BudgetLimits{CallCount: 8, DeniedCount: 1, ApprovalCount: 8}, action.BudgetResetNever),
		storeBudget("second", action.BudgetLimits{CallCount: 8, DeniedCount: 2, ApprovalCount: 8}, action.BudgetResetNever),
	})
	for index, character := range []string{"a", "b"} {
		input, reserved := fixture.reserve(t, callID(character))
		issued := issueFixtureApproval(t, fixture, input, reserved)
		request := ApprovalFinalizeRequest{RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion, Status: actionapproval.StatusCancelled}
		outcome, err := fixture.store.FinalizeApproval(context.Background(), request)
		want := DenialAccounting{ConsumedCount: uint64(2 - index), CapacityExhausted: index == 1}
		if err != nil && !DenialCountCapacityExhausted(err) || !outcome.Persisted ||
			outcome.Result.DenialAccounting == nil || *outcome.Result.DenialAccounting != want ||
			outcome.DenialCapacityExhausted != want.CapacityExhausted {
			t.Fatalf("partial denial accounting: %+v, %v", outcome, err)
		}
		recovered, err := fixture.store.FinalizeApproval(context.Background(), request)
		if err != nil || recovered.Result.DenialAccounting == nil || *recovered.Result.DenialAccounting != want ||
			recovered.Result.StateVersion != outcome.Result.StateVersion {
			t.Fatalf("partial accounting recovery: %+v, %v", recovered, err)
		}
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil || len(status.Budgets) != 2 || status.LiveReservations != 0 {
		t.Fatalf("partial consumption state: %+v, %v", status, err)
	}
	for _, budget := range status.Budgets {
		want := uint64(1)
		if budget.BudgetID == "second" {
			want = 2
		}
		if budget.Consumed.DeniedCount != want {
			t.Fatalf("budget %s charged %d, want %d", budget.BudgetID, budget.Consumed.DeniedCount, want)
		}
	}
	var charged uint64
	for _, record := range status.ApprovalRecords {
		if record.DenialAccounting == nil {
			t.Fatal("status omitted terminal accounting")
		}
		charged += record.DenialAccounting.ConsumedCount
	}
	if charged != 3 {
		t.Fatalf("status accounting charged %d, want 3", charged)
	}
}

func TestExpiredApprovalRecoveryRetainsDenialAccounting(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget("expiry", action.BudgetLimits{CallCount: 4, DeniedCount: 1}, action.BudgetResetNever)})
	requests := make([]ApprovalFinalizeRequest, 0, 2)
	for _, suffix := range []string{"e", "f"} {
		input, reserved := fixture.reserve(t, callID(suffix))
		issued := issueFixtureApproval(t, fixture, input, reserved)
		requests = append(requests, ApprovalFinalizeRequest{
			RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion, Status: actionapproval.StatusCancelled,
		})
	}
	fixture.clock.set(time.Date(2026, 8, 11, 12, 1, 0, 0, time.UTC), "test-clock")
	reconciled, err := fixture.store.ReconcileExpiredApprovals(context.Background())
	if !DenialCountCapacityExhausted(err) || len(reconciled.Expired) != 2 {
		t.Fatalf("expiry reconciliation: %+v, %v", reconciled, err)
	}
	var charged uint64
	exhausted := 0
	for _, request := range requests {
		recovered, err := fixture.store.FinalizeApproval(context.Background(), request)
		if err != nil || recovered.Result.Status != actionapproval.StatusExpired || recovered.Result.DenialAccounting == nil ||
			recovered.Result.StateVersion != reconciled.StateVersion {
			t.Fatalf("expired recovery: %+v, %v", recovered, err)
		}
		charged += recovered.Result.DenialAccounting.ConsumedCount
		if recovered.DenialCapacityExhausted {
			exhausted++
		}
	}
	if charged != 1 || exhausted != 1 {
		t.Fatalf("expired accounting charged=%d exhausted=%d", charged, exhausted)
	}
}

func TestLegacyApprovalWithoutDenialAccountingRemainsReadable(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget("legacy", action.BudgetLimits{CallCount: 2, DeniedCount: 1}, action.BudgetResetNever)})
	input, reserved := fixture.reserve(t, callID("c"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	request := ApprovalFinalizeRequest{RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion, Status: actionapproval.StatusCancelled}
	if _, err := fixture.store.FinalizeApproval(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	version := ""
	if err := fixture.store.withLock(context.Background(), func() error {
		state, persisted, err := fixture.store.loadState()
		if err != nil {
			return err
		}
		legacy := cloneState(state)
		legacy.Approvals[0].DenialAccounting = nil
		body, err := json.Marshal(legacy)
		if err != nil {
			return err
		}
		if bytes.Contains(body, []byte(`"denial_accounting"`)) {
			t.Fatal("legacy encoding gained a new field")
		}
		return fixture.store.writeStateMustAdvance(state, legacy, persisted, &version)
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(StoreOptions{Home: fixture.home, Repository: fixture.repository, KeyLease: fixture.lease, Clock: fixture.clock, OwnerID: fixture.store.ownerID})
	if err != nil {
		t.Fatal(err)
	}
	result, err := reopened.FinalizeApproval(context.Background(), request)
	if err != nil || !result.Persisted || result.Result.StateVersion != version ||
		result.Result.Status != actionapproval.StatusCancelled || result.Result.DenialAccounting != nil || result.DenialCapacityExhausted {
		t.Fatalf("legacy recovery invented unavailable accounting: %+v, %v", result, err)
	}
}

func TestApprovalDenialAccountingValidationAndCloneIsolation(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget("validation", action.BudgetLimits{CallCount: 2, DeniedCount: 1}, action.BudgetResetNever)})
	input, reserved := fixture.reserve(t, callID("d"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	var pending State
	if err := fixture.store.withLock(context.Background(), func() error {
		var err error
		pending, _, err = fixture.store.loadState()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.FinalizeApproval(context.Background(), ApprovalFinalizeRequest{
		RequestState: issued.issue.RequestState, ExpectedStateVersion: issued.issue.StateVersion, Status: actionapproval.StatusCancelled,
	}); err != nil {
		t.Fatal(err)
	}
	var terminal State
	if err := fixture.store.withLock(context.Background(), func() error {
		var err error
		terminal, _, err = fixture.store.loadState()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	copy := cloneState(terminal)
	copy.Approvals[0].DenialAccounting.ConsumedCount = 0
	if terminal.Approvals[0].DenialAccounting.ConsumedCount != 1 {
		t.Fatal("cloning aliased durable denial accounting")
	}
	for _, test := range []struct {
		name   string
		state  State
		mutate func(*ApprovalRecord)
	}{
		{"pending", pending, func(record *ApprovalRecord) { record.DenialAccounting = &DenialAccounting{CapacityExhausted: true} }},
		{"post-result", terminal, func(record *ApprovalRecord) { record.Request.Phase = action.PhasePostResult }},
		{"oversized", terminal, func(record *ApprovalRecord) { record.DenialAccounting.ConsumedCount = MaxBudgetRecords + 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := cloneState(test.state)
			test.mutate(&invalid.Approvals[0])
			var err error
			invalid.Digest, err = fixture.store.stateDigest(invalid)
			if err != nil {
				t.Fatal(err)
			}
			if err := fixture.store.validateState(invalid, true); err == nil {
				t.Fatal("invalid denial accounting passed state validation")
			}
		})
	}
}
