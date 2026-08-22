package actionstate

import (
	"errors"
	"math"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
)

func TestCandidatesFromStateReturnsReservationOverflow(t *testing.T) {
	state := State{Reservations: []Reservation{
		{Charges: []ReservationCharge{{LineageIdentity: "lineage", Reserved: action.BudgetUsage{CallCount: math.MaxUint64}}}},
		{Charges: []ReservationCharge{{LineageIdentity: "lineage", Reserved: action.BudgetUsage{CallCount: 1}}}},
	}}

	_, err := (&Store{}).candidatesFromState(
		state, nil, action.Tool{}, ReserveRequest{}, action.BudgetGeneration{}, time.Time{},
	)
	var stateErr *StateError
	if !errors.As(err, &stateErr) || stateErr.Code != action.ReasonStateCorrupt {
		t.Fatalf("candidatesFromState() error = %v, want %s", err, action.ReasonStateCorrupt)
	}
}
