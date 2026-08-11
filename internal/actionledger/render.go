package actionledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

func MarshalTail(report TailReport) ([]byte, error) {
	if err := validateTailReport(report); err != nil {
		return nil, fmt.Errorf("action ledger tail report is incomplete")
	}
	return marshalIndented(report)
}

func MarshalStats(report StatsReport) ([]byte, error) {
	if err := validateStatsReport(report); err != nil {
		return nil, fmt.Errorf("action ledger stats report is incomplete")
	}
	return marshalIndented(report)
}

func MarshalVerification(report VerificationReport) ([]byte, error) {
	if !marshalableVerification(report) {
		return nil, fmt.Errorf("action ledger verification report is incomplete")
	}
	return marshalIndented(report)
}

func marshalableVerification(report VerificationReport) bool {
	if report.FormatVersion != FormatVersion {
		return false
	}
	switch report.Integrity {
	case StatusEmpty:
		return reflect.DeepEqual(report, EmptyVerificationReport())
	case StatusVerified:
		return readableVerification(report)
	case StatusInvalid:
		return (report.ArchiveContinuity == StatusEmpty || report.ArchiveContinuity == StatusVerified ||
			report.ArchiveContinuity == StatusInvalid) &&
			(report.DetachedHead == HeadAbsent || report.DetachedHead == HeadMatched ||
				report.DetachedHead == HeadInvalid) &&
			report.ArchiveCount <= MaxArchives &&
			validCompletenessEvaluation(
				report.EventsEvaluated, report.EventsComplete, report.IncompleteEvents,
			) && validCompletenessEvaluation(
			report.CallsEvaluated, report.CallsComplete, report.IncompleteCalls,
		)
	default:
		return false
	}
}

func validCompletenessEvaluation(evaluated, complete bool, incomplete uint64) bool {
	if !evaluated {
		return !complete && incomplete == 0
	}
	return complete == (incomplete == 0)
}

func marshalIndented(value any) ([]byte, error) {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func validateTailReport(report TailReport) error {
	if report.FormatVersion != TailReportFormat || report.Records == nil ||
		!readableVerification(report.Verification) {
		return fmt.Errorf("tail report shape is invalid")
	}
	if report.Verification.Integrity == StatusEmpty && len(report.Records) != 0 {
		return fmt.Errorf("empty verification carries tail records")
	}
	for index, record := range report.Records {
		if _, err := Encode(record); err != nil {
			return fmt.Errorf("tail record %d is invalid: %w", index, err)
		}
		if report.Verification.Integrity != StatusVerified ||
			record.Sequence < report.Verification.FirstRetainedSequence ||
			record.Sequence > report.Verification.LastRetainedSequence ||
			index > 0 && report.Records[index-1].Sequence >= record.Sequence {
			return fmt.Errorf("tail record sequence is outside the verified retained range")
		}
	}
	return nil
}

func validateStatsReport(report StatsReport) error {
	if report.FormatVersion != StatsReportFormat || report.Calls == nil || report.ByRun == nil ||
		report.BySession == nil || report.ByPrincipal == nil || report.ByTool == nil ||
		!readableVerification(report.Verification) {
		return fmt.Errorf("stats report shape is invalid")
	}
	if report.Verification.Integrity == StatusEmpty && len(report.Calls) != 0 {
		return fmt.Errorf("empty verification carries call statuses")
	}
	wantCounts := LifecycleCounts{}
	for index, status := range report.Calls {
		if err := validateCallStatus(status, report.Verification); err != nil {
			return fmt.Errorf("call status %d is invalid: %w", index, err)
		}
		if index > 0 && report.Calls[index-1].CallID >= status.CallID {
			return fmt.Errorf("call statuses are not uniquely sorted")
		}
		wantCounts.add(status)
	}
	if !reflect.DeepEqual(report.Counts, wantCounts) ||
		!reflect.DeepEqual(report.ByRun, lifecycleGroups(report.Calls, func(status CallStatus) string { return status.RunIdentity })) ||
		!reflect.DeepEqual(report.BySession, lifecycleGroups(report.Calls, func(status CallStatus) string { return status.SessionIdentity })) ||
		!reflect.DeepEqual(report.ByPrincipal, lifecycleGroups(report.Calls, func(status CallStatus) string { return status.Principal })) ||
		!reflect.DeepEqual(report.ByTool, lifecycleGroups(report.Calls, func(status CallStatus) string {
			return canonicalToolIdentity(status.Tool)
		})) {
		return fmt.Errorf("stats aggregates do not match call statuses")
	}
	return nil
}

func validateCallStatus(status CallStatus, verification VerificationReport) error {
	if !callIDPattern.MatchString(status.CallID) || !action.SafeLabel(status.Principal) ||
		status.Tool.validate() != nil || status.FirstSequence == 0 ||
		status.LastSequence < status.FirstSequence || status.Dispatch != DispatchNotDispatched &&
		status.Dispatch != DispatchDispatched && status.Dispatch != DispatchSucceeded &&
		status.Dispatch != DispatchFailed && status.Dispatch != DispatchUnknown {
		return fmt.Errorf("required status identity or range is invalid")
	}
	for _, identity := range []string{status.RunIdentity, status.SessionIdentity} {
		if identity != "" && !action.ValidKeyedIdentity(identity) {
			return fmt.Errorf("status correlation identity is invalid")
		}
	}
	if status.Evaluated != (status.Decision.Valid() && status.Reason.Valid()) ||
		status.Approval != "" && !actionapproval.Status(status.Approval).Valid() ||
		status.Inspection != "" && !status.Inspection.Valid() ||
		status.Delivery != "" && !status.Delivery.Valid() ||
		status.TerminalFailure && !status.TerminalComplete ||
		status.HistoryComplete && !status.RequestAccepted {
		return fmt.Errorf("status lifecycle fields are contradictory")
	}
	if verification.Integrity != StatusVerified ||
		status.FirstSequence < verification.FirstRetainedSequence ||
		status.LastSequence > verification.LastRetainedSequence {
		return fmt.Errorf("status range is outside verified retained evidence")
	}
	return nil
}

func readableVerification(report VerificationReport) bool {
	if report.FormatVersion != FormatVersion {
		return false
	}
	if report.Integrity == StatusEmpty {
		return reflect.DeepEqual(report, EmptyVerificationReport())
	}
	if report.Integrity != StatusVerified || report.ArchiveContinuity != StatusVerified ||
		report.DetachedHead != HeadMatched || report.RecordCount == 0 || report.ArchiveCount > MaxArchives ||
		report.FirstRecordedSequence != 1 || report.FirstRetainedSequence == 0 ||
		report.LastRetainedSequence < report.FirstRetainedSequence ||
		report.RecordCount != report.LastRetainedSequence-report.FirstRetainedSequence+1 ||
		!report.EventsEvaluated || report.EventsComplete != (report.IncompleteEvents == 0) ||
		!report.CallsEvaluated || report.CallsComplete != (report.IncompleteCalls == 0) {
		return false
	}
	if report.DroppedHistory {
		return report.FirstRetainedSequence > 1 && report.DroppedBeforeSequence == report.FirstRetainedSequence
	}
	return report.FirstRetainedSequence == 1 && report.DroppedBeforeSequence == 0
}

func RenderTailText(report TailReport) []byte {
	var output bytes.Buffer
	renderRetainedHistory(&output, report.Verification)
	if len(report.Records) == 0 {
		fmt.Fprintln(&output, "action ledger: no matching retained events")
		return output.Bytes()
	}
	for _, record := range report.Records {
		decision := string(record.Decision.Decision)
		if decision == "" {
			decision = "absent"
		}
		reason := string(record.Decision.Reason)
		if reason == "" {
			reason = "absent"
		}
		fmt.Fprintf(
			&output, "%d %s %s call=%s phase=%s decision=%s reason=%s complete=%t\n",
			record.Sequence, record.Timestamp, record.Event, record.Call.CallID,
			record.Decision.Phase, decision, reason, record.Decision.Completeness.Complete(),
		)
	}
	return output.Bytes()
}

func RenderStatsText(report StatsReport) []byte {
	var output bytes.Buffer
	renderRetainedHistory(&output, report.Verification)
	counts := report.Counts
	fmt.Fprintf(
		&output,
		"action ledger: calls=%d evaluated=%d approved=%d terminal=%d incomplete=%d evidence_complete=%d evidence_incomplete=%d\n",
		counts.Calls, counts.Evaluated, counts.Approved, counts.TerminalComplete,
		counts.IncompleteTerminal, counts.EvidenceComplete, counts.EvidenceIncomplete,
	)
	fmt.Fprintf(
		&output,
		"decisions: allow=%d warn=%d require_approval=%d block=%d\n",
		counts.Allowed, counts.Warned, counts.ApprovalRequired, counts.Blocked,
	)
	fmt.Fprintf(
		&output,
		"dispatch: not_dispatched=%d dispatched=%d succeeded=%d failed=%d unknown=%d\n",
		counts.NotDispatched, counts.Dispatched, counts.DownstreamSucceeded,
		counts.DownstreamFailed, counts.DownstreamUnknown,
	)
	fmt.Fprintf(
		&output,
		"delivery: delivered=%d withheld=%d suppressed=%d retained_whole=%d retained_pruned=%d\n",
		counts.Delivered, counts.Withheld, counts.Suppressed,
		counts.RetainedHistoryWhole, counts.RetainedHistoryPruned,
	)
	return output.Bytes()
}

func renderRetainedHistory(output *bytes.Buffer, report VerificationReport) {
	if report.RecordCount == 0 {
		fmt.Fprintf(output, "retained history: integrity=%s retained=empty dropped_history=false\n", report.Integrity)
		return
	}
	fmt.Fprintf(
		output,
		"retained history: integrity=%s retained=%d..%d dropped_history=%t",
		report.Integrity, report.FirstRetainedSequence, report.LastRetainedSequence, report.DroppedHistory,
	)
	if report.DroppedHistory {
		fmt.Fprintf(output, " dropped_before=%d", report.DroppedBeforeSequence)
	}
	fmt.Fprintln(output)
}

func RenderVerificationText(report VerificationReport) []byte {
	var output bytes.Buffer
	fmt.Fprintf(
		&output,
		"action ledger: integrity=%s archives=%s head=%s records=%d archives_retained=%d events_evaluated=%t events_complete=%t calls_evaluated=%t calls_complete=%t\n",
		report.Integrity, report.ArchiveContinuity, report.DetachedHead,
		report.RecordCount, report.ArchiveCount, report.EventsEvaluated, report.EventsComplete,
		report.CallsEvaluated, report.CallsComplete,
	)
	if report.RecordCount > 0 {
		fmt.Fprintf(
			&output, "sequence: retained=%d..%d first_recorded=%d dropped_history=%t",
			report.FirstRetainedSequence, report.LastRetainedSequence,
			report.FirstRecordedSequence, report.DroppedHistory,
		)
		if report.DroppedHistory {
			fmt.Fprintf(&output, " dropped_before=%d", report.DroppedBeforeSequence)
		}
		fmt.Fprintln(&output)
	}
	return output.Bytes()
}
