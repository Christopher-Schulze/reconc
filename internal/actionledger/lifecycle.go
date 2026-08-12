package actionledger

import (
	"fmt"
	"reflect"
	"sort"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

type DispatchStatus string

const (
	DispatchNotDispatched DispatchStatus = "not_dispatched"
	DispatchDispatched    DispatchStatus = "dispatched"
	DispatchSucceeded     DispatchStatus = "succeeded"
	DispatchFailed        DispatchStatus = "failed"
	DispatchUnknown       DispatchStatus = "unknown"
)

type CallStatus struct {
	CallID           string                  `json:"call_id"`
	RunIdentity      string                  `json:"run_identity,omitempty"`
	SessionIdentity  string                  `json:"session_identity,omitempty"`
	Principal        string                  `json:"principal"`
	Tool             ToolIdentity            `json:"tool_identity"`
	RequestAccepted  bool                    `json:"request_accepted"`
	Evaluated        bool                    `json:"evaluated"`
	Decision         action.Decision         `json:"decision,omitempty"`
	Reason           action.ReasonCode       `json:"reason_code,omitempty"`
	Approval         actionapproval.Status   `json:"approval,omitempty"`
	Dispatch         DispatchStatus          `json:"dispatch"`
	Inspection       action.InspectionStatus `json:"inspection,omitempty"`
	Delivery         DeliveryStatus          `json:"delivery,omitempty"`
	TerminalFailure  bool                    `json:"terminal_failure"`
	TerminalComplete bool                    `json:"terminal_complete"`
	EvidenceComplete bool                    `json:"evidence_complete"`
	HistoryComplete  bool                    `json:"history_complete"`
	FirstSequence    uint64                  `json:"first_sequence"`
	LastSequence     uint64                  `json:"last_sequence"`
}

type lifecycleState struct {
	status             CallStatus
	binding            CallBinding
	preOutcome         action.PhaseOutcome
	budgetStage        BudgetTransitionKind
	budgetReservation  string
	budgetIDs          []string
	dispatchSeen       bool
	downstreamSeen     bool
	downstreamDecision action.Decision
	downstreamReason   action.ReasonCode
	inspectionSeen     bool
	inspectionDecision action.Decision
	terminalSeen       bool
	preDecisionSeen    bool
	budgetSeen         bool
	budgetTerminal     bool
	budgetStopsCall    bool
	preApproval        approvalLifecycle
	postApproval       approvalLifecycle
	postApprovalBudget int64
	historyMissing     bool
	lastTimestamp      time.Time
}

type approvalLifecycle struct {
	status    actionapproval.Status
	terminal  bool
	requestID string
	policyID  string
	reason    action.ReasonCode
}

func BuildCallStatuses(records []Record) ([]CallStatus, error) {
	states := make(map[string]*lifecycleState)
	droppedHistory := len(records) > 0 && records[0].Sequence > 1
	for _, record := range records {
		state := states[record.Call.CallID]
		if state == nil {
			state = &lifecycleState{
				status: CallStatus{
					CallID: record.Call.CallID, Dispatch: DispatchNotDispatched,
					RunIdentity: record.Call.RunIdentity, SessionIdentity: record.Call.SessionIdentity,
					Principal: record.Call.Principal, Tool: record.Call.Tool,
					EvidenceComplete: true, HistoryComplete: true, FirstSequence: record.Sequence,
				},
				binding: record.Call,
			}
			if record.Event != EventRequestAccepted && droppedHistory {
				state.historyMissing = true
				state.status.HistoryComplete = false
				state.status.EvidenceComplete = false
			}
			states[record.Call.CallID] = state
		} else if !reflect.DeepEqual(state.binding, record.Call) {
			return nil, fmt.Errorf("action ledger call binding drifted for %s", record.Call.CallID)
		}
		timestamp, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
		if !state.lastTimestamp.IsZero() && timestamp.Before(state.lastTimestamp) {
			return nil, fmt.Errorf("action ledger call %s timestamp moves backward at sequence %d", record.Call.CallID, record.Sequence)
		}
		if state.terminalSeen {
			return nil, fmt.Errorf("action ledger call %s has an event after its terminal event", record.Call.CallID)
		}
		if err := state.apply(record); err != nil {
			return nil, fmt.Errorf("action ledger call %s sequence %d: %w", record.Call.CallID, record.Sequence, err)
		}
		state.status.LastSequence = record.Sequence
		state.lastTimestamp = timestamp
		if !record.Decision.Completeness.Complete() || selectedEvidenceIncomplete(record.SelectedFields) {
			state.status.EvidenceComplete = false
		}
	}
	out := make([]CallStatus, 0, len(states))
	for _, state := range states {
		state.finish()
		out = append(out, state.status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CallID < out[j].CallID })
	return out, nil
}

func (s *lifecycleState) apply(record Record) error {
	if record.Event != EventRequestAccepted && !s.status.RequestAccepted && !s.historyMissing {
		return fmt.Errorf("event precedes request acceptance")
	}
	if s.budgetStopsCall {
		return fmt.Errorf("event follows a terminal budget transition")
	}
	if s.preApproval.terminal && s.preApproval.status != actionapproval.StatusApproved &&
		record.Event != EventBudgetTransition {
		return fmt.Errorf("event follows a terminal approval transition")
	}
	if s.preApproval.status == actionapproval.StatusPending && record.Event != EventApprovalTransition ||
		s.postApproval.status == actionapproval.StatusPending && record.Event != EventApprovalTransition {
		return fmt.Errorf("event bypasses a pending approval transition")
	}
	if s.postApproval.terminal && s.postApproval.status != actionapproval.StatusApproved &&
		record.Event != EventBudgetTransition && record.Event != EventFinalDelivery {
		return fmt.Errorf("event follows a terminal post-result approval transition")
	}
	if s.preDecisionSeen && s.status.Decision == action.DecisionBlock &&
		record.Event != EventBudgetTransition {
		return fmt.Errorf("event follows a terminal pre-call decision")
	}
	switch record.Event {
	case EventRequestAccepted:
		if s.status.RequestAccepted || s.status.LastSequence != 0 {
			return fmt.Errorf("request acceptance is duplicated or not first")
		}
		s.status.RequestAccepted = true
	case EventPreDecision:
		if record.Decision.Phase == action.PhasePreCall {
			if s.preDecisionSeen || s.dispatchSeen {
				return fmt.Errorf("pre-call decision is duplicated or follows dispatch")
			}
			s.preDecisionSeen = true
			s.status.Evaluated = true
			s.status.Decision = record.Decision.Decision
			s.status.Reason = record.Decision.Reason
			s.preOutcome = record.PreDecision.Outcome
		} else if record.Decision.Phase == action.PhaseProgress {
			if !s.dispatchSeen && !s.historyMissing {
				return fmt.Errorf("progress decision precedes dispatch")
			}
			if s.downstreamSeen {
				return fmt.Errorf("progress decision follows downstream outcome")
			}
		} else if record.Decision.Phase == action.PhaseObservation {
			if s.preDecisionSeen || s.dispatchSeen {
				return fmt.Errorf("observation decision is duplicated or follows dispatch")
			}
			s.preDecisionSeen = true
			s.status.Evaluated = true
			s.status.Decision = record.Decision.Decision
			s.status.Reason = record.Decision.Reason
			s.preOutcome = record.PreDecision.Outcome
			s.terminalSeen = true
		}
	case EventApprovalTransition:
		if err := s.applyApprovalTransition(record); err != nil {
			return err
		}
	case EventBudgetTransition:
		if !s.preDecisionSeen && !s.historyMissing {
			return fmt.Errorf("budget transition precedes evaluation")
		}
		if err := s.applyBudgetTransition(record); err != nil {
			return err
		}
	case EventDownstreamDispatch:
		approvalEligible := s.status.Decision == action.DecisionRequireApproval &&
			s.preApproval.status == actionapproval.StatusApproved
		if s.dispatchSeen || (!s.historyMissing &&
			(!s.preDecisionSeen || s.preOutcome != action.OutcomeDispatchEligible && !approvalEligible)) {
			return fmt.Errorf("dispatch lacks one eligible pre-call decision")
		}
		if s.status.Decision == action.DecisionRequireApproval && s.preApproval.status != actionapproval.StatusApproved {
			return fmt.Errorf("approval-required call dispatched without approval")
		}
		if s.budgetStopsCall {
			return fmt.Errorf("dispatch follows a terminal budget transition")
		}
		if s.budgetSeen {
			if s.budgetStage != BudgetDispatched {
				return fmt.Errorf("dispatch precedes budget dispatch commitment")
			}
			if record.Dispatch.ReservationIdentity != s.budgetReservation {
				return fmt.Errorf("dispatch reservation identity drifted")
			}
		} else if !s.historyMissing && record.Dispatch.ReservationIdentity != "absent" {
			return fmt.Errorf("dispatch references an unrecorded budget reservation")
		}
		s.dispatchSeen = true
		s.status.Dispatch = DispatchDispatched
	case EventDownstreamOutcome:
		if (!s.dispatchSeen && !s.historyMissing) || s.downstreamSeen {
			return fmt.Errorf("downstream outcome lacks one prior dispatch")
		}
		s.downstreamSeen = true
		s.downstreamDecision = record.Decision.Decision
		s.downstreamReason = record.Decision.Reason
		switch record.Downstream.Status {
		case DownstreamSucceeded:
			s.status.Dispatch = DispatchSucceeded
		case DownstreamFailed:
			s.status.Dispatch = DispatchFailed
		case DownstreamUnknown:
			s.status.Dispatch = DispatchUnknown
		}
	case EventResultInspection:
		if (s.status.Dispatch != DispatchSucceeded && !s.historyMissing) || s.inspectionSeen {
			return fmt.Errorf("result inspection lacks one successful downstream outcome")
		}
		s.inspectionSeen = true
		s.inspectionDecision = record.Decision.Decision
		s.status.Inspection = record.Inspection.Status
	case EventFinalDelivery:
		if record.Delivery.Status == DeliverySuppressed {
			if !s.dispatchSeen && !s.historyMissing {
				return fmt.Errorf("suppressed progress precedes dispatch")
			}
			if s.downstreamSeen {
				return fmt.Errorf("suppressed progress follows downstream outcome")
			}
			s.status.Delivery = DeliverySuppressed
			return nil
		}
		if (!s.inspectionSeen && !s.historyMissing) || s.terminalSeen {
			return fmt.Errorf("final delivery lacks one result inspection")
		}
		if s.budgetSeen && !s.budgetTerminal {
			return fmt.Errorf("final delivery precedes terminal budget settlement")
		}
		if s.budgetStopsCall {
			return fmt.Errorf("final delivery follows a terminal non-dispatch budget transition")
		}
		inspectionPermitted := s.inspectionDecision == action.DecisionAllow ||
			s.inspectionDecision == action.DecisionWarn ||
			s.inspectionDecision == action.DecisionRequireApproval &&
				s.postApproval.status == actionapproval.StatusApproved
		if !s.historyMissing && (record.Delivery.Status == DeliveryForwarded) != inspectionPermitted {
			return fmt.Errorf("final delivery contradicts the result inspection decision")
		}
		s.status.Delivery = record.Delivery.Status
		s.terminalSeen = true
	case EventTerminalFailure:
		if s.terminalSeen {
			return fmt.Errorf("terminal failure is duplicated")
		}
		if !record.Failure.DispatchKnown && (s.dispatchSeen || s.downstreamSeen || s.inspectionSeen) {
			return fmt.Errorf("terminal failure contradicts known dispatch state")
		}
		if !record.Failure.DeliveryKnown && s.status.Delivery != "" {
			return fmt.Errorf("terminal failure contradicts known delivery state")
		}
		if !record.Failure.DispatchKnown {
			s.status.Dispatch = DispatchUnknown
		}
		if !record.Failure.DispatchKnown || !record.Failure.DeliveryKnown {
			s.status.EvidenceComplete = false
		}
		s.status.TerminalFailure = true
		s.terminalSeen = true
	}
	return nil
}

func (s *lifecycleState) applyApprovalTransition(record Record) error {
	var lifecycle *approvalLifecycle
	switch record.Decision.Phase {
	case action.PhasePreCall:
		if (!s.historyMissing && (!s.preDecisionSeen || s.status.Decision != action.DecisionRequireApproval)) ||
			s.dispatchSeen || s.preApproval.terminal || s.budgetStopsCall || s.budgetStage == BudgetDispatched {
			return fmt.Errorf("approval transition is outside the pre-dispatch approval window")
		}
		lifecycle = &s.preApproval
	case action.PhasePostResult:
		if (!s.historyMissing && (!s.inspectionSeen || s.inspectionDecision != action.DecisionRequireApproval)) ||
			s.status.Dispatch != DispatchSucceeded || s.terminalSeen || s.postApproval.terminal ||
			s.budgetStopsCall {
			return fmt.Errorf("approval transition is outside the post-result delivery window")
		}
		lifecycle = &s.postApproval
	default:
		return fmt.Errorf("approval transition phase is unsupported")
	}
	if record.Approval.Status == actionapproval.StatusPending && lifecycle.status != "" {
		return fmt.Errorf("pending approval is duplicated")
	}
	if record.Approval.Status != actionapproval.StatusPending && lifecycle.requestID == "" && !s.historyMissing {
		return fmt.Errorf("terminal approval transition lacks one pending transition")
	}
	if lifecycle.requestID != "" &&
		(lifecycle.requestID != record.Approval.RequestID || lifecycle.policyID != record.Approval.AuthorityPolicyID) {
		return fmt.Errorf("approval transition binding drifted")
	}
	lifecycle.requestID = record.Approval.RequestID
	lifecycle.policyID = record.Approval.AuthorityPolicyID
	lifecycle.status = record.Approval.Status
	if record.Approval.Status != actionapproval.StatusPending {
		lifecycle.terminal = true
		lifecycle.reason = record.Decision.Reason
	}
	s.status.Approval = record.Approval.Status
	return nil
}

func (s *lifecycleState) applyBudgetTransition(record Record) error {
	value := *record.Budget
	if s.budgetTerminal {
		return fmt.Errorf("budget transition follows a terminal budget transition")
	}
	if value.Kind == BudgetReserved && record.Decision.Phase == action.PhasePostResult {
		if !s.budgetSeen || s.budgetStage != BudgetDispatched || !s.downstreamSeen ||
			!s.inspectionSeen || s.inspectionDecision != action.DecisionRequireApproval ||
			s.postApproval.status != "" || s.postApprovalBudget != 0 ||
			value.ReservationIdentity != s.budgetReservation ||
			!reflect.DeepEqual(value.BudgetIDs, s.budgetIDs) {
			return fmt.Errorf("post-result approval budget reservation is outside its lifecycle window")
		}
		s.postApprovalBudget = value.ReservedDelta.ApprovalCount
		return nil
	}
	historyStart := !s.budgetSeen && s.historyMissing
	if !s.budgetSeen {
		if !historyStart && value.Kind != BudgetReserved && value.Kind != BudgetDenied {
			return fmt.Errorf("budget lifecycle begins without reservation or denial")
		}
		s.budgetSeen = true
		s.budgetReservation = value.ReservationIdentity
		s.budgetIDs = append([]string(nil), value.BudgetIDs...)
	} else if value.ReservationIdentity != s.budgetReservation || !reflect.DeepEqual(value.BudgetIDs, s.budgetIDs) {
		return fmt.Errorf("budget transition binding drifted")
	}

	switch value.Kind {
	case BudgetReserved:
		if s.budgetStage != "" || s.dispatchSeen || s.status.Approval != "" {
			return fmt.Errorf("budget reservation is duplicated or follows approval or dispatch")
		}
		if !historyStart && (record.Decision.Decision != s.status.Decision ||
			record.Decision.Reason != s.status.Reason) {
			return fmt.Errorf("budget reservation decision does not match pre-call evaluation")
		}
	case BudgetDispatched:
		if !historyStart && s.budgetStage != BudgetReserved || s.dispatchSeen {
			return fmt.Errorf("budget dispatch commitment lacks one pre-dispatch reservation")
		}
		if !historyStart && s.status.Decision == action.DecisionRequireApproval &&
			s.preApproval.status != actionapproval.StatusApproved {
			return fmt.Errorf("budget dispatch commitment precedes required approval")
		}
		dispatchEligible := s.status.Decision == action.DecisionAllow ||
			s.status.Decision == action.DecisionWarn ||
			s.status.Decision == action.DecisionRequireApproval &&
				s.preApproval.status == actionapproval.StatusApproved
		if !historyStart && !dispatchEligible {
			return fmt.Errorf("budget dispatch commitment lacks one dispatch-eligible decision")
		}
	case BudgetReleased:
		if !historyStart && s.budgetStage != BudgetReserved || s.dispatchSeen {
			return fmt.Errorf("budget release lacks one unused reservation")
		}
		s.budgetTerminal = true
		s.budgetStopsCall = true
	case BudgetSettled:
		if !historyStart && (s.budgetStage != BudgetDispatched || !s.downstreamSeen) {
			return fmt.Errorf("budget settlement lacks a dispatched reservation and downstream outcome")
		}
		if !historyStart && (record.Decision.Decision != s.downstreamDecision ||
			record.Decision.Reason != s.downstreamReason) {
			return fmt.Errorf("budget settlement decision does not match downstream outcome")
		}
		wantCommitted := int64(0)
		if s.postApproval.status == actionapproval.StatusApproved {
			wantCommitted = s.postApprovalBudget
		}
		if value.ReservedDelta.ApprovalCount != -s.postApprovalBudget ||
			value.ConsumedDelta.ApprovalCount != wantCommitted {
			return fmt.Errorf("budget settlement does not match post-result approval state")
		}
		s.budgetTerminal = true
	case BudgetIndeterminate:
		if !historyStart && s.budgetStage != BudgetReserved && s.budgetStage != BudgetDispatched {
			return fmt.Errorf("indeterminate budget transition lacks a reservation")
		}
		s.budgetTerminal = true
		s.budgetStopsCall = true
	case BudgetDenied:
		if !historyStart && s.budgetStage != BudgetReserved || s.dispatchSeen {
			return fmt.Errorf("budget denial lacks one live pre-dispatch reservation")
		}
		terminalReason := s.status.Reason
		if s.preApproval.terminal && s.preApproval.status != actionapproval.StatusApproved {
			terminalReason = s.preApproval.reason
		} else if s.status.Decision != action.DecisionBlock {
			return fmt.Errorf("budget denial lacks one terminal pre-dispatch decision")
		}
		if record.Decision.Decision != action.DecisionBlock || record.Decision.Reason != terminalReason {
			return fmt.Errorf("budget denial decision does not match the terminal cause")
		}
		s.budgetTerminal = true
		s.budgetStopsCall = true
	}
	s.budgetStage = value.Kind
	return nil
}

func (s *lifecycleState) finish() {
	terminalDecision := s.preDecisionSeen && s.status.Decision == action.DecisionBlock
	terminalApproval := s.preApproval.terminal && s.preApproval.status != actionapproval.StatusApproved
	terminalDownstream := s.status.Dispatch == DispatchFailed || s.status.Dispatch == DispatchUnknown
	s.status.TerminalComplete = s.terminalSeen || terminalDecision || terminalApproval || terminalDownstream || s.budgetStopsCall
	if s.budgetSeen && !s.budgetTerminal {
		s.status.TerminalComplete = false
	}
}
