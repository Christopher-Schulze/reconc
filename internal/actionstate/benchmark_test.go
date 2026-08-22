package actionstate

import (
	"context"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func BenchmarkBudgetReserveAndRelease(b *testing.B) {
	fixture := newStoreFixture(b, []action.Budget{storeBudget(
		"transaction-benchmark",
		action.BudgetLimits{CallCount: 1, Concurrent: 1},
		action.BudgetResetNever,
	)})
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		_, reserved := fixture.reserve(b, orderedCapacityCallID(iteration))
		if reserved.Reservation == nil {
			b.Fatal("budget reservation is absent")
		}
		if _, err := fixture.store.Release(
			context.Background(), reserved.Reservation.Identity, reserved.Snapshot.StateVersion,
		); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBudgetStatusLargePersistedState(b *testing.B) {
	fixture := newStoreFixture(b, nil)
	previous, err := fixture.store.initialState()
	if err != nil {
		b.Fatal(err)
	}
	next := cloneState(previous)
	fixture.store.applyClock(&next, fixture.clock.snapshot)
	const retainedTerminalCalls = 32768
	next.TerminalCalls = make([]TerminalCall, retainedTerminalCalls)
	for index := range next.TerminalCalls {
		callID := orderedCapacityCallID(index)
		next.TerminalCalls[index] = TerminalCall{
			CallID: callID,
			ReservationIdentity: fixture.key.Identity(
				DomainBudget, []byte("terminal-benchmark"), []byte(callID),
			),
			Outcome: OutcomeReleased, CompletedAtUnix: fixture.clock.snapshot.Time.Unix(),
		}
	}
	if err := fixture.store.writeState(previous, false, &next); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		status, err := fixture.store.Status(context.Background())
		if err != nil {
			b.Fatal(err)
		}
		if status.TerminalCallCount != retainedTerminalCalls {
			b.Fatalf("terminal calls = %d", status.TerminalCallCount)
		}
	}
}

func BenchmarkTerminalCallLookupMaximumState(b *testing.B) {
	state := State{TerminalCalls: make([]TerminalCall, MaxTerminalCallRecords)}
	for index := range state.TerminalCalls {
		state.TerminalCalls[index].CallID = orderedCapacityCallID(index)
	}
	missing := orderedCapacityCallID(MaxTerminalCallRecords + 1)
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if err := rejectTerminalOrUnsafeRetry(state, missing, "benchmark-owner"); err != nil {
			b.Fatal(err)
		}
	}
}
