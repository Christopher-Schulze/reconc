package actionstate

import (
	"context"

	"reconc.dev/reconc/internal/action"
)

// RecordDenied records a final pre-dispatch block and atomically releases all
// dispatch capacity held by the reservation. An exhausted denial counter stays
// at its limit, the call is still terminalized, and the returned reason is
// budget_exhausted.
func (s *Store) RecordDenied(
	ctx context.Context,
	reservationIdentity string,
	expectedVersion string,
) (version string, resultErr error) {
	resultErr = s.withLock(ctx, func() error {
		previous, next, persisted, clock, err := s.transitionState(expectedVersion)
		if err != nil {
			return err
		}
		index := reservationIndex(next.Reservations, reservationIdentity)
		if index < 0 || next.Reservations[index].Status != ReservationReserved {
			return stateError(action.ReasonReservationIndeterminate, "denial requires a live pre-dispatch reservation", nil)
		}
		if err := requireCurrentReservationContract(next, next.Reservations[index], clock.Time); err != nil {
			return s.persistClockObservationOnFailure(previous, persisted, clock, err)
		}
		exhausted, err := recordDenialCharges(&next, index)
		if err != nil {
			return err
		}
		terminal := TerminalCall{
			CallID:              next.Reservations[index].CallID,
			ReservationIdentity: reservationIdentity,
			Outcome:             OutcomeBlocked, CompletedAtUnix: clock.Time.Unix(),
		}
		if err := removeReservationAndRecord(&next, index, terminal); err != nil {
			return err
		}
		s.applyClock(&next, clock)
		if err := s.writeStateMustAdvance(previous, next, persisted, &version); err != nil {
			return err
		}
		if exhausted {
			return stateError(action.ReasonBudgetExhausted, "denial-count capacity is exhausted", nil)
		}
		return nil
	})
	return version, resultErr
}

func reserveApprovalCharges(state *State, reservationIndex int) (bool, error) {
	reservation := &state.Reservations[reservationIndex]
	reservedByLineage, err := reservedUsageByLineage(*state)
	if err != nil {
		return false, err
	}
	relevant, alreadyReserved := 0, 0
	for chargeIndex := range reservation.Charges {
		charge := &reservation.Charges[chargeIndex]
		record := budgetRecordForLineage(state.Budgets, charge.LineageIdentity)
		if record == nil {
			return false, stateError(action.ReasonStateCorrupt, "approval budget record is absent", nil)
		}
		if record.Limits.ApprovalCount == 0 {
			continue
		}
		relevant++
		if charge.Reserved.ApprovalCount == 1 {
			alreadyReserved++
		} else if charge.Reserved.ApprovalCount != 0 {
			return false, stateError(action.ReasonStateCorrupt, "approval reservation is malformed", nil)
		}
	}
	if relevant == 0 || alreadyReserved == relevant {
		return false, nil
	}
	if alreadyReserved != 0 {
		return false, stateError(action.ReasonStateCorrupt, "approval reservation is only partially applied", nil)
	}
	for _, charge := range reservation.Charges {
		record := budgetRecordForLineage(state.Budgets, charge.LineageIdentity)
		if record.Limits.ApprovalCount == 0 {
			continue
		}
		required := action.BudgetUsage{ApprovalCount: 1}
		if !action.BudgetCapacityAvailable(
			record.Limits, record.Consumed, reservedByLineage[charge.LineageIdentity], required, false,
		) {
			return false, stateError(action.ReasonBudgetExhausted, "approval-count capacity is exhausted", nil)
		}
	}
	for chargeIndex := range reservation.Charges {
		charge := &reservation.Charges[chargeIndex]
		record := budgetRecordForLineage(state.Budgets, charge.LineageIdentity)
		if record.Limits.ApprovalCount != 0 {
			charge.Reserved.ApprovalCount = 1
		}
	}
	return true, nil
}

func commitApprovalCharges(state *State, reservationIndex int) (bool, error) {
	reservation := &state.Reservations[reservationIndex]
	relevant := 0
	for chargeIndex := range reservation.Charges {
		charge := &reservation.Charges[chargeIndex]
		record := budgetRecordForLineage(state.Budgets, charge.LineageIdentity)
		if record == nil {
			return false, stateError(action.ReasonStateCorrupt, "approval budget record is absent", nil)
		}
		if record.Limits.ApprovalCount == 0 {
			continue
		}
		relevant++
		if charge.Reserved.ApprovalCount != 1 {
			return false, stateError(action.ReasonReservationIndeterminate, "approval count was not reserved", nil)
		}
	}
	if relevant == 0 {
		return false, nil
	}
	for chargeIndex := range reservation.Charges {
		charge := &reservation.Charges[chargeIndex]
		record := budgetRecordForLineage(state.Budgets, charge.LineageIdentity)
		if record.Limits.ApprovalCount == 0 {
			continue
		}
		consumed, overflow := checkedUsageAdd(record.Consumed, action.BudgetUsage{ApprovalCount: 1})
		if overflow {
			return false, stateError(action.ReasonStateCorrupt, "approval counter overflowed", nil)
		}
		if charge.CommittedApprovals == ^uint64(0) {
			return false, stateError(action.ReasonStateCorrupt, "reservation approval counter overflowed", nil)
		}
		record.Consumed = consumed
		charge.Reserved.ApprovalCount = 0
		charge.CommittedApprovals++
	}
	return true, nil
}

func releaseApprovalCharges(state *State, reservationIndex int) (bool, error) {
	reservation := &state.Reservations[reservationIndex]
	released := false
	for chargeIndex := range reservation.Charges {
		charge := &reservation.Charges[chargeIndex]
		record := budgetRecordForLineage(state.Budgets, charge.LineageIdentity)
		if record == nil {
			return false, stateError(action.ReasonStateCorrupt, "approval budget record is absent", nil)
		}
		if record.Limits.ApprovalCount == 0 {
			continue
		}
		if charge.Reserved.ApprovalCount != 1 {
			return false, stateError(action.ReasonReservationIndeterminate, "approval count was not reserved", nil)
		}
		charge.Reserved.ApprovalCount = 0
		released = true
	}
	return released, nil
}

func recordDenialCharges(state *State, reservationIndex int) (bool, error) {
	reservation := &state.Reservations[reservationIndex]
	reservedByLineage, err := reservedUsageByLineage(*state)
	if err != nil {
		return false, err
	}
	exhausted := false
	for _, charge := range reservation.Charges {
		record := budgetRecordForLineage(state.Budgets, charge.LineageIdentity)
		if record == nil {
			return false, stateError(action.ReasonStateCorrupt, "denial budget record is absent", nil)
		}
		if record.Limits.DeniedCount == 0 {
			continue
		}
		required := action.BudgetUsage{DeniedCount: 1}
		if !action.BudgetCapacityAvailable(
			record.Limits, record.Consumed, reservedByLineage[charge.LineageIdentity], required, false,
		) {
			exhausted = true
			continue
		}
		consumed, overflow := checkedUsageAdd(record.Consumed, required)
		if overflow {
			exhausted = true
			continue
		}
		record.Consumed = consumed
	}
	return exhausted, nil
}
