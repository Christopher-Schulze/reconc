package actionstate

import (
	"context"
	"sort"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

// Status returns a bounded, payload-free view of the complete local budget
// state. It includes only safe labels, keyed identities, counters, limits, and
// provenance declarations.
func (s *Store) Status(ctx context.Context) (status StateStatus, resultErr error) {
	resultErr = s.withLock(ctx, func() error {
		if err := s.resampleRepositoryIdentity(); err != nil {
			return err
		}
		state, persisted, persistedBytes, err := s.loadStateWithSize()
		if err != nil {
			return err
		}
		if persisted {
			if _, err := s.trustedNow(state); err != nil {
				return err
			}
		}
		status, err = statusFromState(state, persistedBytes)
		if err != nil {
			return err
		}
		return nil
	})
	return status, resultErr
}

func statusFromState(state State, persistedBytes int) (StateStatus, error) {
	reservedByLineage, err := reservedUsageByLineage(state)
	if err != nil {
		return StateStatus{}, err
	}
	status := newStateStatus(state, persistedBytes)
	fillBudgetStatus(&status, state, reservedByLineage)
	fillApprovalStatus(&status, state)
	if err := fillReservationStatus(&status, state); err != nil {
		return StateStatus{}, err
	}
	sort.Slice(status.Remediations, func(i, j int) bool {
		return status.Remediations[i] < status.Remediations[j]
	})
	return status, nil
}

func newStateStatus(state State, stateBytes int) StateStatus {
	return StateStatus{
		StateVersion: state.Digest, Revision: state.Revision, KeyID: state.KeyID,
		RepositoryIdentity: state.RepositoryIdentity, ClockSource: state.ClockSource,
		Budgets:          make([]BudgetStatus, len(state.Budgets)),
		Reservations:     make([]ReservationView, len(state.Reservations)),
		ApprovalRecords:  make([]ApprovalRecordView, len(state.Approvals)),
		LiveReservations: len(state.Reservations), TerminalCallCount: len(state.TerminalCalls),
		Capacity: StateCapacity{
			StateBytes: stateBytes, StateBytesMaximum: MaxStateBytes,
			BudgetRecords: len(state.Budgets), BudgetRecordsMaximum: MaxBudgetRecords,
			Reservations: len(state.Reservations), ReservationsMaximum: MaxReservations,
			TerminalCalls: len(state.TerminalCalls), TerminalCallsMaximum: MaxTerminalCallRecords,
			ApprovalRecords: len(state.Approvals), ApprovalRecordsMaximum: MaxApprovalRecords,
			PendingApprovalsMaximum: MaxPendingApprovals,
			GenerationHistoryMax:    MaxGenerationHistory,
		},
		Remediations: []StateRemediation{},
		Provenance:   IdentityAuthorities(), Complete: true,
	}
}

func fillApprovalStatus(status *StateStatus, state State) {
	for index, record := range state.Approvals {
		status.ApprovalRecords[index] = ApprovalRecordView{
			RequestID: record.Request.RequestID, CallID: record.Request.CallID,
			Status: record.Status, AuthorityPolicy: record.Request.AuthorityPolicyID,
			AuthorityKeyID: record.AuthorityKeyID, ReceiptID: record.ReceiptID,
			ReceiptSignedAt: record.ReceiptSignedAt,
			IssuedAt:        record.Request.IssuedAt, ExpiresAt: record.Request.ExpiresAt,
			UpdatedAtUnix: record.UpdatedAtUnix,
		}
		if record.Status == actionapproval.StatusPending {
			status.PendingApprovals++
		}
	}
}

func fillBudgetStatus(status *StateStatus, state State, reservedByLineage map[string]action.BudgetUsage) {
	for index, record := range state.Budgets {
		status.Budgets[index] = BudgetStatus{
			BudgetID: record.BudgetID, ScopeIdentity: record.ScopeIdentity,
			LineageIdentity: record.LineageIdentity, Scope: cloneBudgetScope(record.Scope),
			Reset: record.Reset, WindowSeconds: record.WindowSeconds,
			Limits: record.Limits, Consumed: record.Consumed,
			Reserved:          reservedByLineage[record.LineageIdentity],
			Generation:        record.Generation,
			GenerationHistory: append([]action.BudgetGeneration(nil), record.GenerationHistory...),
		}
	}
}

func fillReservationStatus(status *StateStatus, state State) error {
	budgetIndex := make(map[string]int, len(status.Budgets))
	for index := range status.Budgets {
		budgetIndex[status.Budgets[index].LineageIdentity] = index
	}
	for reservationPosition, reservation := range state.Reservations {
		view := ReservationView{
			Identity: reservation.Identity, CallID: reservation.CallID, Status: reservation.Status,
			CreatedAtUnix: reservation.CreatedAtUnix, UpdatedAtUnix: reservation.UpdatedAtUnix,
			BudgetIDs:   make([]string, len(reservation.Charges)),
			Remediation: remediationForReservation(reservation.Status),
		}
		for index, charge := range reservation.Charges {
			view.BudgetIDs[index] = charge.BudgetID
		}
		sort.Strings(view.BudgetIDs)
		status.Reservations[reservationPosition] = view
		if !containsRemediation(status.Remediations, view.Remediation) {
			status.Remediations = append(status.Remediations, view.Remediation)
		}
		if reservation.Status == ReservationIndeterminate {
			status.Indeterminate++
		}
		for _, charge := range reservation.Charges {
			index, exists := budgetIndex[charge.LineageIdentity]
			if !exists {
				return stateError(action.ReasonStateCorrupt, "reservation status references an absent budget", nil)
			}
			status.Budgets[index].LiveReservations++
			if reservation.Status == ReservationIndeterminate {
				status.Budgets[index].IndeterminateReservations++
			}
		}
	}
	return nil
}

func remediationForReservation(status ReservationStatus) StateRemediation {
	switch status {
	case ReservationReserved:
		return RemediationReleaseOrDispatch
	case ReservationDispatched:
		return RemediationSettleOrMarkUnknown
	case ReservationIndeterminate:
		return RemediationReconcileIndeterminate
	default:
		return ""
	}
}

func containsRemediation(values []StateRemediation, candidate StateRemediation) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
