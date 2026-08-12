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
