package actionstate

import (
	"context"
	"fmt"
	"math"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestIndeterminateCommittedChargesEachReservedResultCap(t *testing.T) {
	tests := []struct {
		name string
		caps []uint64
	}{
		{name: "narrow then wide", caps: []uint64{10, 100, 0}},
		{name: "wide then narrow", caps: []uint64{100, 10, 0}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, reserved, _ := newIndeterminateResultFixture(t)
			var version string
			version, wantByBudget := rewriteIndeterminateResultState(
				t, fixture, reserved.Reservation.Identity, test.caps, nil,
			)
			cold, err := OpenStore(StoreOptions{
				Home: fixture.home, Repository: fixture.repository, KeyLease: fixture.lease,
				Clock: fixture.clock, OwnerID: "owner-primary",
			})
			if err != nil {
				t.Fatal(err)
			}
			authorization, err := NewReconciliationAuthorization(
				fixture.key, reserved.Reservation.Identity, version, OutcomeIndeterminateCommitted, 0,
			)
			if err != nil {
				t.Fatal(err)
			}
			settledVersion, err := cold.ReconcileIndeterminate(
				context.Background(), reserved.Reservation.Identity, version,
				Reconciliation{AuthorizationIdentity: authorization, Outcome: OutcomeIndeterminateCommitted},
			)
			if err != nil {
				t.Fatal(err)
			}
			status, err := fixture.store.Status(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if status.StateVersion != settledVersion || status.LiveReservations != 0 ||
				status.Indeterminate != 0 || status.TerminalCallCount != 1 {
				t.Fatalf("indeterminate committed status = %#v", status)
			}
			for _, budget := range status.Budgets {
				if budget.Consumed.ResultBytes != wantByBudget[budget.BudgetID] {
					t.Fatalf("budget %s consumed %d result bytes, want %d", budget.BudgetID,
						budget.Consumed.ResultBytes, wantByBudget[budget.BudgetID])
				}
			}
			if _, err := cold.ReconcileIndeterminate(
				context.Background(), reserved.Reservation.Identity, version,
				Reconciliation{AuthorizationIdentity: authorization, Outcome: OutcomeIndeterminateCommitted},
			); err == nil {
				t.Fatal("reconciliation retry charged the removed reservation")
			} else {
				requireStateCode(t, err, action.ReasonStateUnavailable)
			}
			afterRetry, err := cold.Status(context.Background())
			if err != nil || afterRetry.StateVersion != settledVersion {
				t.Fatalf("reconciliation retry changed state: %#v, %v", afterRetry, err)
			}
		})
	}
}

func TestKnownIndeterminateResultSettlementRemainsExact(t *testing.T) {
	fixture, reserved, _ := newIndeterminateResultFixture(t)
	version, capsByBudget := rewriteIndeterminateResultState(
		t, fixture, reserved.Reservation.Identity, []uint64{10, 100, 0}, nil,
	)
	overCapAuthorization, err := NewReconciliationAuthorization(
		fixture.key, reserved.Reservation.Identity, version, OutcomeSucceeded, 11,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ReconcileIndeterminate(
		context.Background(), reserved.Reservation.Identity, version,
		Reconciliation{AuthorizationIdentity: overCapAuthorization, Outcome: OutcomeSucceeded, ActualResultBytes: 11},
	); err == nil {
		t.Fatal("known result exceeded a narrower reservation")
	} else {
		requireStateCode(t, err, action.ReasonStateCorrupt)
	}
	unchanged, err := fixture.store.CurrentStateVersion(context.Background())
	if err != nil || unchanged != version {
		t.Fatalf("rejected known result changed state version = %q, %v", unchanged, err)
	}
	authorization, err := NewReconciliationAuthorization(
		fixture.key, reserved.Reservation.Identity, version, OutcomeSucceeded, 5,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ReconcileIndeterminate(
		context.Background(), reserved.Reservation.Identity, version,
		Reconciliation{AuthorizationIdentity: authorization, Outcome: OutcomeSucceeded, ActualResultBytes: 5},
	); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range status.Budgets {
		want := uint64(0)
		if capsByBudget[budget.BudgetID] != 0 {
			want = 5
		}
		if budget.Consumed.ResultBytes != want {
			t.Fatalf("known result budget %s consumed %d bytes, want %d", budget.BudgetID,
				budget.Consumed.ResultBytes, want)
		}
	}
}

func TestIndeterminateCommittedResultCounterBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		consumed uint64
		wantErr  bool
	}{
		{name: "exact uint64 boundary", consumed: math.MaxUint64 - 1},
		{name: "overflow", consumed: math.MaxUint64, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, reserved, _ := newIndeterminateResultFixture(t)
			version, budgetCaps := rewriteIndeterminateResultState(
				t, fixture, reserved.Reservation.Identity, []uint64{1, 0, 0}, map[int]uint64{0: test.consumed},
			)
			authorization, err := NewReconciliationAuthorization(
				fixture.key, reserved.Reservation.Identity, version, OutcomeIndeterminateCommitted, 0,
			)
			if err != nil {
				t.Fatal(err)
			}
			settledVersion, err := fixture.store.ReconcileIndeterminate(
				context.Background(), reserved.Reservation.Identity, version,
				Reconciliation{AuthorizationIdentity: authorization, Outcome: OutcomeIndeterminateCommitted},
			)
			if test.wantErr {
				requireStateCode(t, err, action.ReasonStateCorrupt)
				if settledVersion != "" {
					t.Fatalf("overflow returned state version %q", settledVersion)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			status, statusErr := fixture.store.Status(context.Background())
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			if test.wantErr {
				if status.StateVersion != version || status.LiveReservations != 1 || status.Indeterminate != 1 {
					t.Fatalf("overflow changed durable state = %#v", status)
				}
				return
			}
			for _, budget := range status.Budgets {
				if budgetCaps[budget.BudgetID] == 1 && budget.Consumed.ResultBytes != math.MaxUint64 {
					t.Fatalf("boundary result counter = %d, want %d", budget.Consumed.ResultBytes, uint64(math.MaxUint64))
				}
			}
		})
	}
}

func newIndeterminateResultFixture(t testing.TB) (*storeFixture, ReserveResult, string) {
	t.Helper()
	limits := action.BudgetLimits{CallCount: 4, ResultBytes: 1000, Concurrent: 4}
	fixture := newStoreFixture(t, []action.Budget{
		storeBudget("never-result", limits, action.BudgetResetNever),
		storeBudget("run-result", limits, action.BudgetResetOperatorRun),
		storeBudget("session-result", limits, action.BudgetResetOperatorSession),
	})
	_, reserved := fixture.reserve(t, callID("m"))
	version, err := fixture.store.MarkIndeterminate(
		context.Background(), reserved.Reservation.Identity, reserved.Snapshot.StateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, reserved, version
}

func rewriteIndeterminateResultState(
	t testing.TB,
	fixture *storeFixture,
	reservationIdentity string,
	caps []uint64,
	consumedByCharge map[int]uint64,
) (string, map[string]uint64) {
	t.Helper()
	version := ""
	capsByBudget := make(map[string]uint64, len(caps))
	err := fixture.store.withLock(context.Background(), func() error {
		previous, persisted, err := fixture.store.loadState()
		if err != nil {
			return err
		}
		next := cloneState(previous)
		position := reservationIndex(next.Reservations, reservationIdentity)
		if position < 0 {
			return fmt.Errorf("result-cap fixture reservation is absent")
		}
		if len(next.Reservations[position].Charges) != len(caps) {
			return fmt.Errorf("result-cap fixture reservation has %d charges; want %d",
				len(next.Reservations[position].Charges), len(caps))
		}
		for index, cap := range caps {
			charge := &next.Reservations[position].Charges[index]
			charge.Reserved.ResultBytes = cap
			capsByBudget[charge.BudgetID] = cap
			consumed, ok := consumedByCharge[index]
			if !ok {
				continue
			}
			record := budgetRecordForLineage(next.Budgets, charge.LineageIdentity)
			if record == nil {
				return fmt.Errorf("result-cap fixture budget %s is absent", charge.BudgetID)
			}
			record.Consumed.ResultBytes = consumed
		}
		if err := fixture.store.writeState(previous, persisted, &next); err != nil {
			return err
		}
		version = next.Digest
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return version, capsByBudget
}
