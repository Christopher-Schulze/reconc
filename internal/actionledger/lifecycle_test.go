package actionledger

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

func TestLifecycleStatusCoversEveryTerminalPathWithoutInference(t *testing.T) {
	tests := []struct {
		name   string
		build  func() []Record
		assert func(*testing.T, CallStatus)
	}{
		{
			name:  "allow",
			build: func() []Record { return successfulLifecycle(action.DecisionAllow) },
			assert: func(t *testing.T, status CallStatus) {
				if status.Decision != action.DecisionAllow || status.Dispatch != DispatchSucceeded ||
					status.Delivery != DeliveryForwarded || !status.TerminalComplete {
					t.Fatalf("allow status = %#v", status)
				}
			},
		},
		{
			name:  "warn",
			build: func() []Record { return successfulLifecycle(action.DecisionWarn) },
			assert: func(t *testing.T, status CallStatus) {
				if status.Decision != action.DecisionWarn || status.Dispatch != DispatchSucceeded ||
					status.Delivery != DeliveryForwarded || !status.TerminalComplete {
					t.Fatalf("warn status = %#v", status)
				}
			},
		},
		{
			name: "block",
			build: func() []Record {
				decision := testLedgerRecord(EventPreDecision)
				decision.Decision.Decision = action.DecisionBlock
				decision.Decision.Reason = action.ReasonRuleMatched
				decision.Decision.RuleIDs = []string{"block-production"}
				decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
				return []Record{testLedgerRecord(EventRequestAccepted), decision}
			},
			assert: func(t *testing.T, status CallStatus) {
				if status.Decision != action.DecisionBlock || status.Dispatch != DispatchNotDispatched ||
					!status.TerminalComplete {
					t.Fatalf("block status = %#v", status)
				}
			},
		},
		{
			name:  "approval approved",
			build: approvedLifecycle,
			assert: func(t *testing.T, status CallStatus) {
				if status.Decision != action.DecisionRequireApproval ||
					status.Approval != actionapproval.StatusApproved || status.Dispatch != DispatchSucceeded ||
					status.Delivery != DeliveryForwarded || !status.TerminalComplete {
					t.Fatalf("approved status = %#v", status)
				}
			},
		},
		{
			name:  "approval rejected",
			build: rejectedLifecycle,
			assert: func(t *testing.T, status CallStatus) {
				if status.Approval != actionapproval.StatusRejected ||
					status.Dispatch != DispatchNotDispatched || !status.TerminalComplete {
					t.Fatalf("rejected status = %#v", status)
				}
			},
		},
		{
			name: "missing approval remains incomplete",
			build: func() []Record {
				decision := testLedgerRecord(EventPreDecision)
				decision.Decision.Decision = action.DecisionRequireApproval
				decision.Decision.Reason = action.ReasonApprovalRequired
				decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
				return []Record{testLedgerRecord(EventRequestAccepted), decision}
			},
			assert: func(t *testing.T, status CallStatus) {
				if status.Decision != action.DecisionRequireApproval || status.Approval != "" ||
					status.Dispatch != DispatchNotDispatched || status.TerminalComplete {
					t.Fatalf("missing approval status = %#v", status)
				}
			},
		},
		{
			name: "observation is terminal without dispatch",
			build: func() []Record {
				observation := testLedgerRecord(EventPreDecision)
				observation.Decision.Phase = action.PhaseObservation
				observation.Decision.Decision = action.DecisionBlock
				observation.Decision.Reason = action.ReasonRuleMatched
				observation.PreDecision.Outcome = action.OutcomeRecorded
				observation.SelectedFields = []SelectedFieldEvidence{}
				return []Record{testLedgerRecord(EventRequestAccepted), observation}
			},
			assert: func(t *testing.T, status CallStatus) {
				if !status.Evaluated || status.Decision != action.DecisionBlock ||
					status.Dispatch != DispatchNotDispatched || !status.TerminalComplete {
					t.Fatalf("observation status = %#v", status)
				}
			},
		},
		{
			name: "timeout",
			build: func() []Record {
				failure := testLedgerRecord(EventTerminalFailure)
				failure.Decision.Reason = action.ReasonDeadlineExceeded
				failure.Failure.Lifecycle = action.LifecycleActive
				return []Record{testLedgerRecord(EventRequestAccepted), failure}
			},
			assert: func(t *testing.T, status CallStatus) {
				if !status.TerminalFailure || !status.TerminalComplete || status.Dispatch != DispatchNotDispatched {
					t.Fatalf("timeout status = %#v", status)
				}
			},
		},
		{
			name: "cancellation",
			build: func() []Record {
				return []Record{testLedgerRecord(EventRequestAccepted), testLedgerRecord(EventTerminalFailure)}
			},
			assert: func(t *testing.T, status CallStatus) {
				if !status.TerminalFailure || !status.TerminalComplete {
					t.Fatalf("cancelled status = %#v", status)
				}
			},
		},
		{
			name:  "downstream crash",
			build: func() []Record { return downstreamFailureLifecycle(DownstreamFailed) },
			assert: func(t *testing.T, status CallStatus) {
				if status.Dispatch != DispatchFailed || !status.TerminalComplete || status.Delivery != "" {
					t.Fatalf("failed downstream status = %#v", status)
				}
			},
		},
		{
			name:  "unknown downstream outcome",
			build: func() []Record { return downstreamFailureLifecycle(DownstreamUnknown) },
			assert: func(t *testing.T, status CallStatus) {
				if status.Dispatch != DispatchUnknown || !status.TerminalComplete || status.Delivery != "" {
					t.Fatalf("unknown downstream status = %#v", status)
				}
			},
		},
		{
			name:  "result withheld",
			build: withheldLifecycle,
			assert: func(t *testing.T, status CallStatus) {
				if status.Dispatch != DispatchSucceeded || status.Delivery != DeliveryWithheld ||
					!status.TerminalComplete {
					t.Fatalf("withheld status = %#v", status)
				}
			},
		},
		{
			name: "missing outcome remains incomplete",
			build: func() []Record {
				return []Record{testLedgerRecord(EventRequestAccepted), testLedgerRecord(EventPreDecision), testLedgerRecord(EventDownstreamDispatch)}
			},
			assert: func(t *testing.T, status CallStatus) {
				if status.Dispatch != DispatchDispatched || status.TerminalComplete {
					t.Fatalf("missing outcome status = %#v", status)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records := sealLifecycle(t, test.build())
			statuses, err := BuildCallStatuses(records)
			if err != nil {
				t.Fatal(err)
			}
			if len(statuses) != 1 || !statuses[0].EvidenceComplete || !statuses[0].HistoryComplete {
				t.Fatalf("statuses = %#v", statuses)
			}
			test.assert(t, statuses[0])
		})
	}
}

func successfulLifecycle(decision action.Decision) []Record {
	records := []Record{
		testLedgerRecord(EventRequestAccepted), testLedgerRecord(EventPreDecision),
		testLedgerRecord(EventDownstreamDispatch), testLedgerRecord(EventDownstreamOutcome),
		testLedgerRecord(EventResultInspection), testLedgerRecord(EventFinalDelivery),
	}
	if decision == action.DecisionWarn {
		for index := 1; index < len(records); index++ {
			records[index].Decision.Decision = action.DecisionWarn
			records[index].Decision.Reason = action.ReasonRuleMatched
			records[index].Decision.RuleIDs = []string{"warn-production"}
		}
	}
	return records
}

func approvedLifecycle() []Record {
	records := successfulLifecycle(action.DecisionAllow)
	records[1].Decision.Decision = action.DecisionRequireApproval
	records[1].Decision.Reason = action.ReasonApprovalRequired
	records[1].PreDecision.Outcome = action.OutcomeDispatchBlocked
	pending := testLedgerRecord(EventApprovalTransition)
	approved := testLedgerRecord(EventApprovalTransition)
	approved.Approval.Status = actionapproval.StatusApproved
	approved.Approval.AuthorityKeyID = "security-primary"
	approved.Approval.ReceiptID = "arc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	approved.Approval.ReceiptIdentity = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	return append(records[:2], append([]Record{pending, approved}, records[2:]...)...)
}

func rejectedLifecycle() []Record {
	decision := testLedgerRecord(EventPreDecision)
	decision.Decision.Decision = action.DecisionRequireApproval
	decision.Decision.Reason = action.ReasonApprovalRequired
	decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
	pending := testLedgerRecord(EventApprovalTransition)
	rejected := testLedgerRecord(EventApprovalTransition)
	rejected.Approval.Status = actionapproval.StatusRejected
	rejected.Decision.Reason = action.ReasonApprovalRejected
	rejected.Approval.AuthorityKeyID = "security-primary"
	rejected.Approval.ReceiptID = "arc_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	rejected.Approval.ReceiptIdentity = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	return []Record{testLedgerRecord(EventRequestAccepted), decision, pending, rejected}
}

func downstreamFailureLifecycle(status DownstreamStatus) []Record {
	outcome := testLedgerRecord(EventDownstreamOutcome)
	outcome.Downstream.Status = status
	outcome.Decision.Decision = action.DecisionBlock
	if status == DownstreamFailed {
		outcome.Decision.Reason = action.ReasonDownstreamError
	} else {
		outcome.Decision.Reason = action.ReasonDownstreamUnknown
	}
	return []Record{
		testLedgerRecord(EventRequestAccepted), testLedgerRecord(EventPreDecision),
		testLedgerRecord(EventDownstreamDispatch), outcome,
	}
}

func withheldLifecycle() []Record {
	records := successfulLifecycle(action.DecisionAllow)
	inspection := &records[len(records)-2]
	inspection.Decision.Decision = action.DecisionBlock
	inspection.Decision.Reason = action.ReasonResultWithheld
	inspection.Inspection.Status = action.InspectionMatched
	inspection.Inspection.Categories = []action.DetectorCategory{action.DetectorSecret}
	delivery := &records[len(records)-1]
	delivery.Decision.Decision = action.DecisionBlock
	delivery.Decision.Reason = action.ReasonResultWithheld
	delivery.Delivery.Status = DeliveryWithheld
	delivery.Delivery.ByteLength = 0
	delivery.Delivery.ItemCount = 0
	return records
}

func sealLifecycle(t *testing.T, records []Record) []Record {
	t.Helper()
	sealed := make([]Record, 0, len(records))
	previous := ""
	for index, record := range records {
		entry, _, err := Seal(record, uint64(index+1), previous)
		if err != nil {
			t.Fatalf("Seal(%s) failed: %v", record.Event, err)
		}
		sealed = append(sealed, entry)
		previous = entry.Digest
	}
	return sealed
}

func TestLifecycleAcceptsEveryValidMidCallEventAtAPrunedBoundary(t *testing.T) {
	progress := testLedgerRecord(EventPreDecision)
	progress.Decision.Phase = action.PhaseProgress
	progress.PreDecision.Outcome = action.OutcomeProgressEligible
	progress.SelectedFields = []SelectedFieldEvidence{}
	tests := []struct {
		name   string
		record Record
	}{
		{name: "pre-decision", record: testLedgerRecord(EventPreDecision)},
		{name: "progress", record: progress},
		{name: "approval", record: testLedgerRecord(EventApprovalTransition)},
		{name: "budget", record: testLedgerRecord(EventBudgetTransition)},
		{name: "dispatch", record: testLedgerRecord(EventDownstreamDispatch)},
		{name: "outcome", record: testLedgerRecord(EventDownstreamOutcome)},
		{name: "inspection", record: testLedgerRecord(EventResultInspection)},
		{name: "delivery", record: testLedgerRecord(EventFinalDelivery)},
		{name: "failure", record: testLedgerRecord(EventTerminalFailure)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sealed, _, err := Seal(test.record, 2, strings.Repeat("f", 64))
			if err != nil {
				t.Fatal(err)
			}
			statuses, err := BuildCallStatuses([]Record{sealed})
			if err != nil || len(statuses) != 1 || statuses[0].HistoryComplete ||
				statuses[0].EvidenceComplete {
				t.Fatalf("pruned-boundary status = %#v, %v", statuses, err)
			}
		})
	}
}

func TestLifecycleRejectsApprovalBindingDrift(t *testing.T) {
	records := approvedLifecycle()
	records[3].Approval.RequestID = "apr_" + strings.Repeat("d", 26)
	sealed := sealLifecycle(t, records)
	if _, err := BuildCallStatuses(sealed); err == nil {
		t.Fatal("BuildCallStatuses() accepted approval request binding drift")
	}
}

func TestLifecycleRejectsApprovalAfterPermittingDecision(t *testing.T) {
	records := []Record{
		testLedgerRecord(EventRequestAccepted),
		testLedgerRecord(EventPreDecision),
		testLedgerRecord(EventApprovalTransition),
	}
	sealed := sealLifecycle(t, records)
	if _, err := BuildCallStatuses(sealed); err == nil {
		t.Fatal("BuildCallStatuses() accepted approval after a permitting pre-decision")
	}
}

func TestLifecycleRejectsTerminalApprovalWithoutPendingTransition(t *testing.T) {
	records := approvedLifecycle()
	records = append(records[:2], records[3:]...)
	if _, err := BuildCallStatuses(sealLifecycle(t, records)); err == nil ||
		!strings.Contains(err.Error(), "lacks one pending transition") {
		t.Fatalf("BuildCallStatuses() error = %v", err)
	}
}

func TestLifecycleRejectsEventsAfterPreCallBlock(t *testing.T) {
	decision := testLedgerRecord(EventPreDecision)
	decision.Decision.Decision = action.DecisionBlock
	decision.Decision.Reason = action.ReasonRuleMatched
	decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
	records := []Record{
		testLedgerRecord(EventRequestAccepted), decision, testLedgerRecord(EventDownstreamDispatch),
	}
	if _, err := BuildCallStatuses(sealLifecycle(t, records)); err == nil ||
		!strings.Contains(err.Error(), "terminal pre-call decision") {
		t.Fatalf("BuildCallStatuses() error = %v", err)
	}
}

func TestLifecycleAllowsReservedBudgetDenialAfterPreCallBlock(t *testing.T) {
	decision := testLedgerRecord(EventPreDecision)
	decision.Decision.Decision = action.DecisionBlock
	decision.Decision.Reason = action.ReasonRuleMatched
	decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
	reserved := budgetLifecycleTransition(BudgetReserved)
	reserved.Decision.Decision = action.DecisionBlock
	reserved.Decision.Reason = action.ReasonRuleMatched
	denied := budgetLifecycleTransition(BudgetDenied)
	denied.Decision.Decision = action.DecisionBlock
	denied.Decision.Reason = action.ReasonRuleMatched
	statuses, err := BuildCallStatuses(sealLifecycle(t, []Record{
		testLedgerRecord(EventRequestAccepted), decision, reserved, denied,
	}))
	if err != nil || len(statuses) != 1 || !statuses[0].TerminalComplete ||
		statuses[0].Decision != action.DecisionBlock {
		t.Fatalf("BuildCallStatuses() = %#v, %v", statuses, err)
	}
}

func TestLifecycleRejectsBudgetDenialWithoutRecordedReservation(t *testing.T) {
	decision := testLedgerRecord(EventPreDecision)
	decision.Decision.Decision = action.DecisionBlock
	decision.Decision.Reason = action.ReasonRuleMatched
	decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
	denied := budgetLifecycleTransition(BudgetDenied)
	denied.Decision.Decision = action.DecisionBlock
	denied.Decision.Reason = action.ReasonRuleMatched
	if _, err := BuildCallStatuses(sealLifecycle(t, []Record{
		testLedgerRecord(EventRequestAccepted), decision, denied,
	})); err == nil || !strings.Contains(err.Error(), "live pre-dispatch reservation") {
		t.Fatalf("BuildCallStatuses() error = %v", err)
	}
}

func TestLifecycleRejectsDeliveryContradictingInspection(t *testing.T) {
	records := successfulLifecycle(action.DecisionAllow)
	delivery := &records[len(records)-1]
	delivery.Decision.Decision = action.DecisionBlock
	delivery.Decision.Reason = action.ReasonResultWithheld
	delivery.Delivery.Status = DeliveryWithheld
	delivery.Delivery.ByteLength = 0
	delivery.Delivery.ItemCount = 0
	sealed := sealLifecycle(t, records)
	if _, err := BuildCallStatuses(sealed); err == nil {
		t.Fatal("BuildCallStatuses() accepted delivery contradicting its result inspection")
	}
}

func TestLifecycleRejectsProgressAfterDownstreamOutcome(t *testing.T) {
	progressDecision := testLedgerRecord(EventPreDecision)
	progressDecision.Decision.Phase = action.PhaseProgress
	progressDecision.PreDecision.Outcome = action.OutcomeProgressEligible
	progressDecision.SelectedFields = []SelectedFieldEvidence{}
	suppressedProgress := testLedgerRecord(EventFinalDelivery)
	suppressedProgress.Decision.Phase = action.PhaseProgress
	suppressedProgress.Decision.Decision = action.DecisionBlock
	suppressedProgress.Decision.Reason = action.ReasonRuleMatched
	suppressedProgress.Delivery.Status = DeliverySuppressed
	suppressedProgress.SelectedFields = []SelectedFieldEvidence{}

	tests := []struct {
		name  string
		event Record
		want  string
	}{
		{name: "decision", event: progressDecision, want: "progress decision follows downstream outcome"},
		{name: "delivery", event: suppressedProgress, want: "suppressed progress follows downstream outcome"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records := successfulLifecycle(action.DecisionAllow)[:4]
			records = append(records, test.event)
			if _, err := BuildCallStatuses(sealLifecycle(t, records)); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("BuildCallStatuses() error = %v", err)
			}
		})
	}
}

func TestLifecycleRejectsTimestampRegressionWithinOneCall(t *testing.T) {
	records := []Record{testLedgerRecord(EventRequestAccepted), testLedgerRecord(EventPreDecision)}
	records[0].Timestamp = "2026-08-11T12:00:01Z"
	records[1].Timestamp = "2026-08-11T12:00:00Z"
	sealed := sealLifecycle(t, records)
	if _, err := BuildCallStatuses(sealed); err == nil || !strings.Contains(err.Error(), "timestamp moves backward") {
		t.Fatalf("BuildCallStatuses() error = %v", err)
	}
}

func TestLifecycleSurfacesUnknownTerminalDispatchWithoutInference(t *testing.T) {
	failure := testLedgerRecord(EventTerminalFailure)
	failure.Failure.DispatchKnown = false
	failure.Failure.DeliveryKnown = false
	failure.Decision.Completeness.StateComplete = false
	failure.Decision.Completeness.Missing = []action.MissingEvidence{{
		Field: action.EvidenceState, Reason: action.ReasonStateUnavailable,
	}}
	statuses, err := BuildCallStatuses(sealLifecycle(t, []Record{
		testLedgerRecord(EventRequestAccepted), failure,
	}))
	if err != nil || len(statuses) != 1 || statuses[0].Dispatch != DispatchUnknown ||
		!statuses[0].TerminalComplete || statuses[0].EvidenceComplete {
		t.Fatalf("BuildCallStatuses() = %#v, %v", statuses, err)
	}
}

func TestLifecycleEnforcesBudgetSettlementAndIdentity(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		statuses, err := BuildCallStatuses(sealLifecycle(t, successfulBudgetLifecycle()))
		if err != nil || len(statuses) != 1 || !statuses[0].TerminalComplete {
			t.Fatalf("budget lifecycle = %#v, %v", statuses, err)
		}
	})
	t.Run("unsettled failed downstream remains incomplete", func(t *testing.T) {
		records := successfulBudgetLifecycle()[:6]
		outcome := &records[len(records)-1]
		outcome.Downstream.Status = DownstreamFailed
		outcome.Decision.Decision = action.DecisionBlock
		outcome.Decision.Reason = action.ReasonDownstreamError
		statuses, err := BuildCallStatuses(sealLifecycle(t, records))
		if err != nil || len(statuses) != 1 || statuses[0].TerminalComplete {
			t.Fatalf("unsettled failure = %#v, %v", statuses, err)
		}
	})
	t.Run("reservation drift", func(t *testing.T) {
		records := successfulBudgetLifecycle()
		records[3].Budget.ReservationIdentity = testKeyedIdentity("d")
		if _, err := BuildCallStatuses(sealLifecycle(t, records)); err == nil {
			t.Fatal("BuildCallStatuses() accepted a drifted budget reservation")
		}
	})
	t.Run("unrecorded reservation", func(t *testing.T) {
		records := successfulLifecycle(action.DecisionAllow)
		records[2].Dispatch.ReservationIdentity = testKeyedIdentity("c")
		if _, err := BuildCallStatuses(sealLifecycle(t, records)); err == nil {
			t.Fatal("BuildCallStatuses() accepted an unrecorded budget reservation")
		}
	})
}

func TestLifecycleBindsPostResultApprovalBudgetAndDelivery(t *testing.T) {
	tests := []struct {
		name     string
		status   actionapproval.Status
		delivery DeliveryStatus
	}{
		{name: "approved", status: actionapproval.StatusApproved, delivery: DeliveryForwarded},
		{name: "rejected", status: actionapproval.StatusRejected, delivery: DeliveryWithheld},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inspection := testLedgerRecord(EventResultInspection)
			inspection.Decision.Decision = action.DecisionRequireApproval
			inspection.Decision.Reason = action.ReasonApprovalRequired
			inspection.Inspection.Status = action.InspectionClean

			approvalBudget := budgetLifecycleTransition(BudgetReserved)
			approvalBudget.Decision.Phase = action.PhasePostResult
			approvalBudget.Decision.Decision = action.DecisionRequireApproval
			approvalBudget.Decision.Reason = action.ReasonApprovalRequired
			approvalBudget.SelectedFields[0].Source = action.SourceResult
			approvalBudget.Budget.ReservedDelta = BudgetDelta{ApprovalCount: 1}

			pending := testLedgerRecord(EventApprovalTransition)
			pending.Decision.Phase = action.PhasePostResult
			pending.SelectedFields[0].Source = action.SourceResult
			terminal := testLedgerRecord(EventApprovalTransition)
			terminal.Decision.Phase = action.PhasePostResult
			terminal.SelectedFields[0].Source = action.SourceResult
			terminal.Approval.Status = test.status
			terminal.Approval.AuthorityKeyID = "security-primary"
			terminal.Approval.ReceiptID = "arc_" + strings.Repeat("c", 26)
			terminal.Approval.ReceiptIdentity = "sha256:" + strings.Repeat("c", 64)
			if test.status == actionapproval.StatusRejected {
				terminal.Decision.Reason = action.ReasonApprovalRejected
			}

			settled := budgetLifecycleTransition(BudgetSettled)
			settled.Budget.ReservedDelta.ApprovalCount = -1
			if test.status == actionapproval.StatusApproved {
				settled.Budget.ConsumedDelta.ApprovalCount = 1
			}

			delivery := testLedgerRecord(EventFinalDelivery)
			if test.status == actionapproval.StatusApproved {
				delivery.Decision.Decision = action.DecisionRequireApproval
				delivery.Decision.Reason = action.ReasonApprovalRequired
			} else {
				delivery.Decision.Decision = action.DecisionBlock
				delivery.Decision.Reason = action.ReasonApprovalRejected
				delivery.Delivery.Status = DeliveryWithheld
				delivery.Delivery.ByteLength = 0
				delivery.Delivery.ItemCount = 0
			}

			records := successfulBudgetLifecycle()[:6]
			records = append(records, inspection, approvalBudget, pending, terminal, settled, delivery)
			statuses, err := BuildCallStatuses(sealLifecycle(t, records))
			if err != nil || len(statuses) != 1 || !statuses[0].TerminalComplete ||
				statuses[0].Approval != test.status || statuses[0].Delivery != test.delivery {
				t.Fatalf("post-result approval lifecycle = %#v, %v", statuses, err)
			}
		})
	}
}

func TestLifecycleRejectsApprovalAndBudgetOrderingBypasses(t *testing.T) {
	approvalDecision := func() Record {
		record := testLedgerRecord(EventPreDecision)
		record.Decision.Decision = action.DecisionRequireApproval
		record.Decision.Reason = action.ReasonApprovalRequired
		record.PreDecision.Outcome = action.OutcomeDispatchBlocked
		return record
	}
	tests := []struct {
		name    string
		records []Record
		want    string
	}{
		{
			name: "approval after terminal budget denial",
			records: func() []Record {
				reserved := budgetLifecycleTransition(BudgetReserved)
				reserved.Decision.Decision = action.DecisionRequireApproval
				reserved.Decision.Reason = action.ReasonApprovalRequired
				denied := budgetLifecycleTransition(BudgetDenied)
				denied.Decision.Decision = action.DecisionBlock
				denied.Decision.Reason = action.ReasonApprovalRejected
				rejected := rejectedLifecycle()
				return []Record{
					rejected[0], rejected[1], reserved, rejected[2], rejected[3], denied,
					testLedgerRecord(EventApprovalTransition),
				}
			}(),
			want: "terminal budget transition",
		},
		{
			name: "reservation after approval begins",
			records: []Record{
				testLedgerRecord(EventRequestAccepted), approvalDecision(),
				testLedgerRecord(EventApprovalTransition), budgetLifecycleTransition(BudgetReserved),
			},
			want: "event bypasses a pending approval transition",
		},
		{
			name: "dispatch commitment before required approval",
			records: func() []Record {
				reserved := budgetLifecycleTransition(BudgetReserved)
				reserved.Decision.Decision = action.DecisionRequireApproval
				reserved.Decision.Reason = action.ReasonApprovalRequired
				return []Record{
					testLedgerRecord(EventRequestAccepted), approvalDecision(),
					reserved, budgetLifecycleTransition(BudgetDispatched),
				}
			}(),
			want: "precedes required approval",
		},
		{
			name: "dispatch commitment after blocking decision",
			records: func() []Record {
				decision := testLedgerRecord(EventPreDecision)
				decision.Decision.Decision = action.DecisionBlock
				decision.Decision.Reason = action.ReasonRuleMatched
				decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
				reserved := budgetLifecycleTransition(BudgetReserved)
				reserved.Decision.Decision = action.DecisionBlock
				reserved.Decision.Reason = action.ReasonRuleMatched
				return []Record{
					testLedgerRecord(EventRequestAccepted), decision, reserved,
					budgetLifecycleTransition(BudgetDispatched),
				}
			}(),
			want: "dispatch-eligible decision",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildCallStatuses(sealLifecycle(t, test.records))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("BuildCallStatuses() error = %v", err)
			}
		})
	}
}

func TestLifecycleRejectsBudgetDecisionEvidenceDrift(t *testing.T) {
	t.Run("reservation", func(t *testing.T) {
		reserved := budgetLifecycleTransition(BudgetReserved)
		reserved.Decision.Decision = action.DecisionWarn
		reserved.Decision.Reason = action.ReasonRuleMatched
		if _, err := BuildCallStatuses(sealLifecycle(t, []Record{
			testLedgerRecord(EventRequestAccepted), testLedgerRecord(EventPreDecision), reserved,
		})); err == nil || !strings.Contains(err.Error(), "does not match pre-call evaluation") {
			t.Fatalf("BuildCallStatuses() error = %v", err)
		}
	})
	t.Run("settlement", func(t *testing.T) {
		records := successfulBudgetLifecycle()
		settlement := &records[6]
		settlement.Decision.Decision = action.DecisionWarn
		settlement.Decision.Reason = action.ReasonRuleMatched
		if _, err := BuildCallStatuses(sealLifecycle(t, records)); err == nil ||
			!strings.Contains(err.Error(), "does not match downstream outcome") {
			t.Fatalf("BuildCallStatuses() error = %v", err)
		}
	})
}

func successfulBudgetLifecycle() []Record {
	dispatch := testLedgerRecord(EventDownstreamDispatch)
	dispatch.Dispatch.ReservationIdentity = testKeyedIdentity("c")
	return []Record{
		testLedgerRecord(EventRequestAccepted),
		testLedgerRecord(EventPreDecision),
		budgetLifecycleTransition(BudgetReserved),
		budgetLifecycleTransition(BudgetDispatched),
		dispatch,
		testLedgerRecord(EventDownstreamOutcome),
		budgetLifecycleTransition(BudgetSettled),
		testLedgerRecord(EventResultInspection),
		testLedgerRecord(EventFinalDelivery),
	}
}

func budgetLifecycleTransition(kind BudgetTransitionKind) Record {
	record := testLedgerRecord(EventBudgetTransition)
	record.Budget.Kind = kind
	record.Budget.ReservedDelta = BudgetDelta{}
	record.Budget.ConsumedDelta = BudgetDelta{}
	switch kind {
	case BudgetReserved:
		record.Budget.ReservedDelta.CallCount = 1
	case BudgetReleased:
		record.Budget.ReservedDelta.CallCount = -1
	case BudgetDispatched:
		record.Budget.ReservedDelta.CallCount = -1
		record.Budget.ConsumedDelta.CallCount = 1
	case BudgetSettled:
		record.Decision.Phase = action.PhasePostResult
		record.SelectedFields[0].Source = action.SourceResult
		record.Budget.ConsumedDelta.ResultBytes = 7
	case BudgetIndeterminate:
		record.Decision.Phase = action.PhasePostResult
		record.SelectedFields[0].Source = action.SourceResult
		record.Decision.Decision = action.DecisionBlock
		record.Decision.Reason = action.ReasonReservationIndeterminate
	case BudgetDenied:
		record.Budget.ReservedDelta.CallCount = -1
		record.Budget.ConsumedDelta.DeniedCount = 1
		record.Decision.Decision = action.DecisionBlock
		record.Decision.Reason = action.ReasonRuleMatched
	}
	return record
}
