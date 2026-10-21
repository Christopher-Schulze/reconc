package actionstate

import (
	"time"

	"reconc.dev/reconc/internal/actionapproval"
)

// StateAdmissionHeadroomBytes keeps room to finalize every active
// reservation: a new reservation or approval is refused while the
// encoded state plus this headroom would exceed MaxStateBytes. The refusal
// clears once retained history ages out of the admission window.
const StateAdmissionHeadroomBytes = 1 << 20

// legacyStateFormatVersion is the format whose call identities were random
// and carried no issuing MAC.
const legacyStateFormatVersion = "1"

// pruneRetainedHistory removes finished history that can no longer reject an
// admissible replay. A terminal call goes once its call identity is past the
// admission window and no retained approval references its reservation. A
// finished approval goes once its reservation is no longer active, its
// request expired before the window, and its call identity is past the
// window. Budget records are never pruned.
func pruneRetainedHistory(state *State, now time.Time) {
	active := make(map[string]bool, len(state.Reservations))
	for _, reservation := range state.Reservations {
		active[reservation.Identity] = true
	}
	approvals := filterApprovals(state.Approvals, func(record ApprovalRecord) bool {
		return finishedApprovalOutsideWindow(record, active, now) && callIdentityExpired(record.Request.CallID, now)
	})
	referenced := approvalReservationReferences(approvals)
	state.Approvals = approvals
	state.TerminalCalls = filterTerminalCalls(state.TerminalCalls, func(call TerminalCall) bool {
		return !referenced[call.ReservationIdentity] && callIdentityExpired(call.CallID, now)
	})
}

// migrateLegacyHistory upgrades a format-1 state. Format-1 call identities
// were random, so one that fails this store's MAC can never be admitted, and
// finished history keyed by it is dropped at once. History that a pending
// approval, an active reservation, or a retained approval still needs stays
// and later follows the regular retention rule.
func (s *Store) migrateLegacyHistory(state *State, now time.Time) {
	if state.FormatVersion != legacyStateFormatVersion {
		return
	}
	state.FormatVersion = StateFormatVersion
	active := make(map[string]bool, len(state.Reservations))
	for _, reservation := range state.Reservations {
		active[reservation.Identity] = true
	}
	approvals := filterApprovals(state.Approvals, func(record ApprovalRecord) bool {
		expires, err := time.Parse(time.RFC3339Nano, record.Request.ExpiresAt)
		return record.Status != actionapproval.StatusPending &&
			(record.ReservationIdentity == "absent" || !active[record.ReservationIdentity]) &&
			err == nil && expires.Before(now) && !s.issuedCallIdentity(record.Request.CallID)
	})
	referenced := approvalReservationReferences(approvals)
	state.Approvals = approvals
	state.TerminalCalls = filterTerminalCalls(state.TerminalCalls, func(call TerminalCall) bool {
		return !referenced[call.ReservationIdentity] && !s.issuedCallIdentity(call.CallID)
	})
}

func finishedApprovalOutsideWindow(record ApprovalRecord, active map[string]bool, now time.Time) bool {
	if record.Status == actionapproval.StatusPending ||
		record.ReservationIdentity != "absent" && active[record.ReservationIdentity] {
		return false
	}
	expires, err := time.Parse(time.RFC3339Nano, record.Request.ExpiresAt)
	return err == nil && expires.Add(CallIdentityAdmissionWindow).Before(now)
}

func approvalReservationReferences(approvals []ApprovalRecord) map[string]bool {
	referenced := make(map[string]bool, len(approvals))
	for _, record := range approvals {
		if record.ReservationIdentity != "absent" {
			referenced[record.ReservationIdentity] = true
		}
	}
	return referenced
}

// filterApprovals returns approvals without the pruned records, in their
// order; it allocates only when a record is pruned, so a caller's slice is
// never modified.
func filterApprovals(approvals []ApprovalRecord, pruned func(ApprovalRecord) bool) []ApprovalRecord {
	for index, record := range approvals {
		if !pruned(record) {
			continue
		}
		kept := append(make([]ApprovalRecord, 0, len(approvals)-1), approvals[:index]...)
		for _, rest := range approvals[index+1:] {
			if !pruned(rest) {
				kept = append(kept, rest)
			}
		}
		return kept
	}
	return approvals
}

// filterTerminalCalls is filterApprovals for terminal calls.
func filterTerminalCalls(calls []TerminalCall, pruned func(TerminalCall) bool) []TerminalCall {
	for index, call := range calls {
		if !pruned(call) {
			continue
		}
		kept := append(make([]TerminalCall, 0, len(calls)-1), calls[:index]...)
		for _, rest := range calls[index+1:] {
			if !pruned(rest) {
				kept = append(kept, rest)
			}
		}
		return kept
	}
	return calls
}
