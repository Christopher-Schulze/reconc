package actionstate

import (
	"context"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func FuzzDecodeState(f *testing.F) {
	fixture := newStoreFixture(f, []action.Budget{storeBudget(
		"fuzz-state", action.BudgetLimits{CallCount: 4, ApprovalCount: 4}, action.BudgetResetNever,
	)})
	fixture.reserve(f, callID("q"))
	var persisted State
	if err := fixture.store.withLock(context.Background(), func() error {
		var loadErr error
		persisted, _, loadErr = fixture.store.loadState()
		return loadErr
	}); err != nil {
		f.Fatal(err)
	}
	valid, err := encodeBoundedJSON(persisted, MaxStateBytes)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{valid, []byte(`{}`), []byte(`{"schema":"reconc.action-state/v1"}`), {0xff}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > MaxStateBytes {
			t.Skip()
		}
		first, err := fixture.store.decodeState(input)
		if err != nil {
			return
		}
		canonical, err := encodeBoundedJSON(first, MaxStateBytes)
		if err != nil {
			t.Fatal(err)
		}
		second, err := fixture.store.decodeState(canonical)
		if err != nil || first.Digest != second.Digest || first.Revision != second.Revision {
			t.Fatalf("accepted action state did not round-trip: %v", err)
		}
	})
}

func FuzzDecodeStateTransaction(f *testing.F) {
	fixture := newStoreFixture(f, []action.Budget{storeBudget(
		"fuzz-transaction", action.BudgetLimits{CallCount: 4}, action.BudgetResetNever,
	)})
	fixture.reserve(f, callID("r"))
	var previous State
	if err := fixture.store.withLock(context.Background(), func() error {
		var loadErr error
		previous, _, loadErr = fixture.store.loadState()
		return loadErr
	}); err != nil {
		f.Fatal(err)
	}
	next := cloneState(previous)
	next.Revision++
	next.Digest = ""
	var err error
	next.Digest, err = fixture.store.stateDigest(next)
	if err != nil {
		f.Fatal(err)
	}
	transaction, err := fixture.store.newTransaction(previous, true, next)
	if err != nil {
		f.Fatal(err)
	}
	valid, err := encodeBoundedCompactJSON(transaction, MaxStateTransaction)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{valid, []byte(`{}`), []byte(`{"schema":"reconc.action-state-transaction/v1"}`), {0xff}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > MaxStateTransaction {
			t.Skip()
		}
		first, err := fixture.store.decodeTransaction(input)
		if err != nil {
			return
		}
		canonical, err := encodeBoundedCompactJSON(first, MaxStateTransaction)
		if err != nil {
			t.Fatal(err)
		}
		second, err := fixture.store.decodeTransaction(canonical)
		if err != nil || first.Digest != second.Digest || first.After.Digest != second.After.Digest {
			t.Fatalf("accepted action-state transaction did not round-trip: %v", err)
		}
	})
}

func FuzzOpenApprovalRequestState(f *testing.F) {
	fixture := newStoreFixture(f, []action.Budget{storeBudget(
		"fuzz-approval-state", action.BudgetLimits{CallCount: 4, ApprovalCount: 4}, action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(f, callID("s"))
	issued := issueFixtureApproval(f, fixture, input, reserved)
	for _, seed := range []string{issued.issue.RequestState, "", "not-base64url", "e30"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > MaxApprovalRequestStateBytes*2 {
			t.Skip()
		}
		first, err := fixture.store.openApprovalRequestState(input)
		if err != nil {
			return
		}
		second, err := fixture.store.openApprovalRequestState(input)
		if err != nil || first != second {
			t.Fatalf("accepted approval request state decoded nondeterministically: %v", err)
		}
	})
}
