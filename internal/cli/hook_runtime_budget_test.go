package cli

import (
	"testing"
	"time"
)

func TestHookWaitBudgetLeavesTimeToReportBeforeTheHostKills(t *testing.T) {
	for budget, want := range map[time.Duration]time.Duration{
		5 * time.Second:  4500 * time.Millisecond,
		10 * time.Second: 9 * time.Second,
		30 * time.Second: 27 * time.Second,
		2 * time.Second:  1500 * time.Millisecond,
	} {
		if got := hookWaitBudget(budget); got != want {
			t.Errorf("hookWaitBudget(%s) = %s, want %s", budget, got, want)
		}
	}
}
