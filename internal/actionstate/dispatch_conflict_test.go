package actionstate

import (
	"context"
	"errors"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
)

func TestDispatchConflictSentinelIsLimitedToStateVersion(t *testing.T) {
	for _, test := range []struct {
		name           string
		stale, expired bool
	}{
		{"cas-conflict", true, false}, {"expired-window", false, true}, {"missing-reservation", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStoreFixture(t, []action.Budget{storeBudget("dispatch", action.BudgetLimits{RateWindow: 2}, action.BudgetResetFixedWindow)})
			_, reserved := fixture.reserve(t, callID("d"))
			identity, version := reserved.Reservation.Identity, reserved.Snapshot.StateVersion
			if test.stale {
				version = "stale"
			}
			if test.expired {
				fixture.clock.set(time.Date(2026, 8, 11, 12, 1, 0, 0, time.UTC), "test-clock")
			}
			if !test.stale && !test.expired {
				identity = "absent"
			}
			_, err := fixture.store.MarkDispatched(context.Background(), identity, version)
			if err == nil || errors.Is(err, ErrStateVersionChanged) != test.stale {
				t.Fatalf("dispatch conflict classification: %v", err)
			}
		})
	}
}
