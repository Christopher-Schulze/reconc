package actionstate

import (
	"context"
	"errors"
	"sort"
	"time"

	"reconc.dev/reconc/internal/action"
)

type Reconciliation struct {
	AuthorizationIdentity string
	Outcome               TerminalOutcome
	ActualResultBytes     uint64
}

func (s *Store) MarkDispatched(ctx context.Context, reservation, expectedVersion string) (version string, resultErr error) {
	resultErr = s.withLock(ctx, func() error {
		previous, state, persisted, clock, err := s.transitionState(expectedVersion)
		if err != nil {
			return err
		}
		index := reservationIndex(state.Reservations, reservation)
		if index < 0 {
			return stateError(action.ReasonReservationIndeterminate, "budget reservation is absent", nil)
		}
		if state.Reservations[index].Status == ReservationDispatched {
			version = state.Digest
			return nil
		}
		if state.Reservations[index].Status != ReservationReserved {
			return stateError(action.ReasonReservationIndeterminate, "budget reservation is not dispatchable", nil)
		}
		if err := requireCurrentReservationContract(state, state.Reservations[index], clock.Time); err != nil {
			return s.persistClockObservationOnFailure(previous, persisted, clock, err)
		}
		if err := commitDispatch(&state, index); err != nil {
			return err
		}
		state.Reservations[index].Status = ReservationDispatched
		state.Reservations[index].UpdatedAtUnix = clock.Time.Unix()
		s.applyClock(&state, clock)
		if err := s.writeStateMustAdvance(previous, state, persisted, &version); err != nil {
			return err
		}
		return nil
	})
	return version, resultErr
}

func (s *Store) Release(ctx context.Context, reservation, expectedVersion string) (version string, resultErr error) {
	resultErr = s.withLock(ctx, func() error {
		previous, state, persisted, clock, err := s.transitionState(expectedVersion)
		if err != nil {
			return err
		}
		index := reservationIndex(state.Reservations, reservation)
		if index < 0 || state.Reservations[index].Status != ReservationReserved {
			return stateError(action.ReasonReservationIndeterminate, "only a pre-dispatch reservation can be released", nil)
		}
		terminal := TerminalCall{
			CallID: state.Reservations[index].CallID, ReservationIdentity: reservation,
			Outcome: OutcomeReleased, CompletedAtUnix: clock.Time.Unix(),
		}
		if err := removeReservationAndRecord(&state, index, terminal); err != nil {
			return err
		}
		s.applyClock(&state, clock)
		if err := s.writeStateMustAdvance(previous, state, persisted, &version); err != nil {
			return err
		}
		return nil
	})
	return version, resultErr
}

func (s *Store) MarkIndeterminate(ctx context.Context, reservation, expectedVersion string) (version string, resultErr error) {
	resultErr = s.withLock(ctx, func() error {
		previous, state, persisted, clock, err := s.transitionState(expectedVersion)
		if err != nil {
			return err
		}
		index := reservationIndex(state.Reservations, reservation)
		if index < 0 {
			return stateError(action.ReasonReservationIndeterminate, "budget reservation is absent", nil)
		}
		if state.Reservations[index].Status == ReservationIndeterminate {
			version = state.Digest
			return nil
		}
		if state.Reservations[index].Status == ReservationReserved {
			if err := commitDispatch(&state, index); err != nil {
				return err
			}
		}
		state.Reservations[index].Status = ReservationIndeterminate
		state.Reservations[index].UpdatedAtUnix = clock.Time.Unix()
		s.applyClock(&state, clock)
		if err := s.writeStateMustAdvance(previous, state, persisted, &version); err != nil {
			return err
		}
		return nil
	})
	return version, resultErr
}

func (s *Store) Settle(
	ctx context.Context,
	reservation, expectedVersion string,
	outcome TerminalOutcome,
	actualResultBytes uint64,
) (version string, resultErr error) {
	if outcome != OutcomeSucceeded && outcome != OutcomeFailed {
		return "", stateError(action.ReasonStateCorrupt, "terminal settlement outcome is invalid", nil)
	}
	resultErr = s.withLock(ctx, func() error {
		previous, state, persisted, clock, err := s.transitionState(expectedVersion)
		if err != nil {
			return err
		}
		index := reservationIndex(state.Reservations, reservation)
		if index < 0 || state.Reservations[index].Status != ReservationDispatched {
			return stateError(action.ReasonReservationIndeterminate, "only a dispatched reservation can settle", nil)
		}
		if exceedsReservedResult(state.Reservations[index], actualResultBytes) {
			state.Reservations[index].Status = ReservationIndeterminate
			state.Reservations[index].UpdatedAtUnix = clock.Time.Unix()
			s.applyClock(&state, clock)
			if err := s.writeStateMustAdvance(previous, state, persisted, &version); err != nil {
				return err
			}
			return stateError(action.ReasonResultWithheld, "downstream result exceeds the reserved result-byte contract", nil)
		}
		if err := commitResult(&state, index, actualResultBytes); err != nil {
			return err
		}
		terminal := TerminalCall{
			CallID: state.Reservations[index].CallID, ReservationIdentity: reservation,
			Outcome: outcome, CompletedAtUnix: clock.Time.Unix(),
		}
		if err := removeReservationAndRecord(&state, index, terminal); err != nil {
			return err
		}
		s.applyClock(&state, clock)
		if err := s.writeStateMustAdvance(previous, state, persisted, &version); err != nil {
			return err
		}
		return nil
	})
	return version, resultErr
}

func (s *Store) ReconcileIndeterminate(
	ctx context.Context,
	reservation, expectedVersion string,
	reconciliation Reconciliation,
) (version string, resultErr error) {
	if reconciliation.Outcome != OutcomeSucceeded && reconciliation.Outcome != OutcomeFailed &&
		reconciliation.Outcome != OutcomeIndeterminateCommitted ||
		reconciliation.Outcome == OutcomeIndeterminateCommitted && reconciliation.ActualResultBytes != 0 {
		return "", stateError(action.ReasonAuthorityUnavailable, "explicit reconciliation authority is invalid", nil)
	}
	resultErr = s.withLock(ctx, func() error {
		previous, state, persisted, clock, err := s.transitionState(expectedVersion)
		if err != nil {
			return err
		}
		index := reservationIndex(state.Reservations, reservation)
		if index < 0 || state.Reservations[index].Status != ReservationIndeterminate {
			return stateError(action.ReasonReservationIndeterminate, "reservation is not indeterminate", nil)
		}
		wantAuthorization, err := NewReconciliationAuthorization(
			s.key, reservation, expectedVersion, reconciliation.Outcome, reconciliation.ActualResultBytes,
		)
		if err != nil || !constantIdentityEqual(reconciliation.AuthorizationIdentity, wantAuthorization) {
			return stateError(action.ReasonAuthorityUnavailable, "reconciliation authority does not bind the exact operation", err)
		}
		if reconciliation.Outcome == OutcomeIndeterminateCommitted {
			if err := commitReservedResults(&state, index); err != nil {
				return err
			}
		} else {
			actual := reconciliation.ActualResultBytes
			if exceedsReservedResult(state.Reservations[index], actual) {
				return stateError(action.ReasonStateCorrupt, "reconciled result exceeds the reserved maximum", nil)
			}
			if err := commitResult(&state, index, actual); err != nil {
				return err
			}
		}
		terminal := TerminalCall{
			CallID: state.Reservations[index].CallID, ReservationIdentity: reservation,
			Outcome: reconciliation.Outcome, CompletedAtUnix: clock.Time.Unix(),
		}
		if err := removeReservationAndRecord(&state, index, terminal); err != nil {
			return err
		}
		s.applyClock(&state, clock)
		if err := s.writeStateMustAdvance(previous, state, persisted, &version); err != nil {
			return err
		}
		return nil
	})
	return version, resultErr
}

func (s *Store) MarkOwnerAbandoned(
	ctx context.Context,
	ownerID, expectedVersion, authorizationIdentity string,
) (version string, resultErr error) {
	if !validOpaqueStateIdentity(ownerID) {
		return "", stateError(action.ReasonAuthorityUnavailable, "abandonment reconciliation authority is invalid", nil)
	}
	resultErr = s.withLock(ctx, func() error {
		previous, state, persisted, clock, err := s.transitionState(expectedVersion)
		if err != nil {
			return err
		}
		wantAuthorization, err := NewOwnerAbandonmentAuthorization(s.key, ownerID, expectedVersion)
		if err != nil || !constantIdentityEqual(authorizationIdentity, wantAuthorization) {
			return stateError(action.ReasonAuthorityUnavailable, "abandonment authority does not bind the exact operation", err)
		}
		if err := terminalizeAbandonedPendingApprovals(&state, ownerID, clock.Time); err != nil {
			return err
		}
		changed := false
		for index := range state.Reservations {
			reservation := &state.Reservations[index]
			if reservation.OwnerID != ownerID || reservation.Status == ReservationIndeterminate {
				continue
			}
			if reservation.Status == ReservationReserved {
				if err := commitDispatch(&state, index); err != nil {
					return err
				}
			}
			reservation.Status = ReservationIndeterminate
			reservation.UpdatedAtUnix = clock.Time.Unix()
			changed = true
		}
		if !changed {
			version = state.Digest
			return nil
		}
		s.applyClock(&state, clock)
		if err := s.writeStateMustAdvance(previous, state, persisted, &version); err != nil {
			return err
		}
		return nil
	})
	return version, resultErr
}

func (s *Store) transitionState(expected string) (State, State, bool, ClockSnapshot, error) {
	if err := s.resampleRepositoryIdentity(); err != nil {
		return State{}, State{}, false, ClockSnapshot{}, err
	}
	state, persisted, err := s.loadState()
	if err != nil {
		return State{}, State{}, false, ClockSnapshot{}, err
	}
	if expected != state.Digest {
		return State{}, State{}, false, ClockSnapshot{}, stateError(action.ReasonStateUnavailable, "action state changed before transition", nil)
	}
	clock, err := s.trustedNow(state)
	if err != nil {
		return State{}, State{}, false, ClockSnapshot{}, err
	}
	return state, cloneState(state), persisted, clock, nil
}

func (s *Store) writeStateMustAdvance(previous, next State, persisted bool, version *string) error {
	if version == nil {
		return stateError(action.ReasonStateUnavailable, "action state version output is unavailable", nil)
	}
	if err := s.writeState(previous, persisted, &next); err != nil {
		return err
	}
	*version = next.Digest
	return nil
}

func (s *Store) persistClockObservationOnFailure(
	previous State,
	persisted bool,
	clock ClockSnapshot,
	cause error,
) error {
	if cause == nil || clock.Time.UnixNano() <= previous.LastObservedUnixNano {
		return cause
	}
	next := cloneState(previous)
	s.applyClock(&next, clock)
	if err := s.writeState(previous, persisted, &next); err != nil {
		return stateError(
			action.ReasonStateUnavailable,
			"persist trusted clock observation while failing closed",
			errors.Join(cause, err),
		)
	}
	return cause
}

func commitDispatch(state *State, reservationIndex int) error {
	reservation := &state.Reservations[reservationIndex]
	for chargeIndex := range reservation.Charges {
		charge := &reservation.Charges[chargeIndex]
		if charge.DispatchCommitted {
			continue
		}
		if charge.Reserved.ApprovalCount != 0 {
			return stateError(action.ReasonReservationIndeterminate, "approval count must commit before dispatch", nil)
		}
		record := budgetRecordForLineage(state.Budgets, charge.LineageIdentity)
		if record == nil {
			return stateError(action.ReasonStateCorrupt, "reservation budget record is absent", nil)
		}
		consumed, overflow := checkedUsageAdd(record.Consumed, dispatchUsage(charge.Reserved))
		if overflow {
			return stateError(action.ReasonStateCorrupt, "budget counter overflowed during dispatch", nil)
		}
		record.Consumed = consumed
		charge.Reserved = postDispatchReservation(charge.Reserved)
		charge.DispatchCommitted = true
	}
	return nil
}

func commitResult(state *State, reservationIndex int, actual uint64) error {
	reservation := &state.Reservations[reservationIndex]
	for _, charge := range reservation.Charges {
		record := budgetRecordForLineage(state.Budgets, charge.LineageIdentity)
		if record == nil {
			return stateError(action.ReasonStateCorrupt, "reservation budget record is absent", nil)
		}
		resultUsage := action.BudgetUsage{}
		if charge.Reserved.ResultBytes != 0 {
			resultUsage.ResultBytes = actual
		}
		consumed, overflow := checkedUsageAdd(record.Consumed, resultUsage)
		if overflow {
			return stateError(action.ReasonStateCorrupt, "budget counter overflowed during settlement", nil)
		}
		record.Consumed = consumed
	}
	return nil
}

func commitReservedResults(state *State, reservationIndex int) error {
	reservation := &state.Reservations[reservationIndex]
	for _, charge := range reservation.Charges {
		record := budgetRecordForLineage(state.Budgets, charge.LineageIdentity)
		if record == nil {
			return stateError(action.ReasonStateCorrupt, "reservation budget record is absent", nil)
		}
		if charge.Reserved.ResultBytes == 0 {
			continue
		}
		consumed, overflow := checkedUsageAdd(
			record.Consumed,
			action.BudgetUsage{ResultBytes: charge.Reserved.ResultBytes},
		)
		if overflow {
			return stateError(action.ReasonStateCorrupt, "budget counter overflowed during settlement", nil)
		}
		record.Consumed = consumed
	}
	return nil
}

func removeReservationAndRecord(state *State, index int, terminal TerminalCall) error {
	if len(state.TerminalCalls) >= MaxTerminalCallRecords {
		return stateError(action.ReasonStateUnavailable, "terminal call record capacity is exhausted", nil)
	}
	state.Reservations = append(state.Reservations[:index], state.Reservations[index+1:]...)
	state.TerminalCalls = append(state.TerminalCalls, terminal)
	sort.Slice(state.TerminalCalls, func(i, j int) bool { return state.TerminalCalls[i].CallID < state.TerminalCalls[j].CallID })
	return nil
}

func exceedsReservedResult(reservation Reservation, actual uint64) bool {
	for _, charge := range reservation.Charges {
		if charge.Reserved.ResultBytes != 0 && actual > charge.Reserved.ResultBytes {
			return true
		}
	}
	return false
}

func reservationIndex(reservations []Reservation, identity string) int {
	index := sort.Search(len(reservations), func(index int) bool { return reservations[index].Identity >= identity })
	if index == len(reservations) || reservations[index].Identity != identity {
		return -1
	}
	return index
}

func requireCurrentReservationContract(state State, reservation Reservation, now time.Time) error {
	for _, charge := range reservation.Charges {
		record := budgetRecordForLineage(state.Budgets, charge.LineageIdentity)
		if record == nil || record.BudgetID != charge.BudgetID ||
			record.ScopeIdentity != charge.ScopeIdentity || record.Generation != charge.Generation {
			return stateError(action.ReasonStateUnavailable, "reservation governing generation changed before use", nil)
		}
		if record.Reset == action.BudgetResetFixedWindow {
			seconds := int64(record.WindowSeconds)
			if seconds <= 0 || now.Unix()/seconds*seconds != record.Scope.WindowStartUnix {
				return stateError(action.ReasonStateUnavailable, "reservation fixed window elapsed before use", nil)
			}
		}
	}
	return nil
}
