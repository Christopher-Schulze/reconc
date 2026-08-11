package actionledger

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

const testKeyID = "ledger-key"

func testKeyedIdentity(fill string) string {
	return "hmac-sha256:v1:" + testKeyID + ":" + strings.Repeat(fill, 64)
}

func testLedgerRecord(event EventType) Record {
	record := Record{
		Schema: Schema, FormatVersion: FormatVersion, ChainVersion: ChainVersion,
		Sequence: 1, Timestamp: "2026-08-11T12:00:00.123456Z", Event: event,
		Call: CallBinding{
			CallID: "act_" + strings.Repeat("a", 26), RequestIdentity: testKeyedIdentity("1"),
			RepositoryIdentity: testKeyedIdentity("2"), PolicyDigest: strings.Repeat("3", 64),
			LockDigest: strings.Repeat("4", 64), ServerLabel: "warehouse",
			ServerFingerprint:  testKeyedIdentity("5"),
			Tool:               ToolIdentity{Mode: action.LedgerDeclarationID, Value: "database-write"},
			ToolContractDigest: "sha256:" + strings.Repeat("6", 64), Principal: "release-operator",
			CredentialLabels: []string{"production-database"}, RunIdentity: testKeyedIdentity("7"),
			SessionIdentity: testKeyedIdentity("8"), ContextIdentity: testKeyedIdentity("9"),
			ContextProvenance: action.ProvenanceOperatorBound,
		},
		Decision: DecisionBinding{
			Phase: action.PhasePreCall, Decision: action.DecisionAllow,
			Reason: action.ReasonDeclaredTool, RuleIDs: []string{}, Completeness: action.CompleteEvidence(),
		},
		SelectedFields: []SelectedFieldEvidence{{
			Source: action.SourceArguments, PointerIdentity: testKeyedIdentity("a"),
			State: action.PointerPresent, Kind: action.ValueString, ValueIdentity: testKeyedIdentity("b"),
			ByteLength: 7, ItemCount: 0, Complete: true,
		}},
	}
	switch event {
	case EventRequestAccepted:
		record.Decision.Decision = ""
		record.Decision.Reason = ""
		record.RequestAccepted = &RequestAccepted{ArgumentBytes: 7, ArgumentItems: 1}
	case EventPreDecision:
		record.PreDecision = &PreDecision{Outcome: action.OutcomeDispatchEligible}
	case EventApprovalTransition:
		record.Decision.Decision = action.DecisionRequireApproval
		record.Decision.Reason = action.ReasonApprovalRequired
		record.Approval = &ApprovalTransition{
			RequestID: "apr_" + strings.Repeat("c", 26), Status: actionapproval.StatusPending,
			AuthorityPolicyID: "production-writes",
		}
	case EventBudgetTransition:
		record.Budget = &BudgetTransition{
			Kind: BudgetReserved, ReservationIdentity: testKeyedIdentity("c"),
			StateVersion: testKeyedIdentity("d"), BudgetIDs: []string{"database-calls"},
			ReservedDelta: BudgetDelta{CallCount: 1},
		}
	case EventDownstreamDispatch:
		record.Dispatch = &DownstreamDispatch{ReservationIdentity: "absent"}
	case EventDownstreamOutcome:
		record.Decision.Phase = action.PhasePostResult
		record.Downstream = &DownstreamOutcome{Status: DownstreamSucceeded}
	case EventResultInspection:
		record.Decision.Phase = action.PhasePostResult
		record.Inspection = &ResultInspection{
			Status: action.InspectionClean, Categories: []action.DetectorCategory{},
			SchemaStatus: action.InspectionSchemaNotDeclared, ScannedBytes: 7, ScannedItems: 1,
		}
	case EventFinalDelivery:
		record.Decision.Phase = action.PhasePostResult
		record.Delivery = &FinalDelivery{Status: DeliveryForwarded, ByteLength: 7, ItemCount: 1}
	case EventTerminalFailure:
		record.Decision.Decision = action.DecisionBlock
		record.Decision.Reason = action.ReasonCancelled
		record.Failure = &TerminalFailure{Lifecycle: action.LifecycleCancelled, DispatchKnown: true, DeliveryKnown: true}
	}
	if record.Decision.Phase == action.PhasePostResult {
		record.SelectedFields[0].Source = action.SourceResult
	}
	return record
}

func allEventTypes() []EventType {
	return []EventType{
		EventRequestAccepted, EventPreDecision, EventApprovalTransition,
		EventBudgetTransition, EventDownstreamDispatch, EventDownstreamOutcome,
		EventResultInspection, EventFinalDelivery, EventTerminalFailure,
	}
}

func TestSealEncodeDecodeEveryEvent(t *testing.T) {
	previous := ""
	for index, event := range allEventTypes() {
		t.Run(string(event), func(t *testing.T) {
			sealed, body, err := Seal(testLedgerRecord(event), uint64(index+1), previous)
			if err != nil {
				t.Fatalf("Seal() error = %v", err)
			}
			decoded, err := Decode(body)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if !reflect.DeepEqual(decoded, sealed) {
				t.Fatalf("Decode() = %#v, want %#v", decoded, sealed)
			}
			encoded, err := Encode(decoded)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			if !bytes.Equal(encoded, body) {
				t.Fatalf("Encode() changed canonical bytes")
			}
			previous = sealed.Digest
		})
	}
}

func TestObservationDecisionSealsAsRecordedWithoutDispatch(t *testing.T) {
	record := testLedgerRecord(EventPreDecision)
	record.Decision.Phase = action.PhaseObservation
	record.Decision.Decision = action.DecisionBlock
	record.Decision.Reason = action.ReasonRuleMatched
	record.PreDecision.Outcome = action.OutcomeRecorded
	record.SelectedFields = []SelectedFieldEvidence{}
	if _, _, err := Seal(record, 1, ""); err != nil {
		t.Fatalf("Seal(observation) error = %v", err)
	}
}

func TestDeclaredToolWarningSealsWithItsBaselineReason(t *testing.T) {
	record := testLedgerRecord(EventPreDecision)
	record.Decision.Decision = action.DecisionWarn
	if _, _, err := Seal(record, 1, ""); err != nil {
		t.Fatalf("declared-tool warning was rejected: %v", err)
	}
}

func TestIncompleteInspectionSealsWithExplicitFailureEvidence(t *testing.T) {
	record := testLedgerRecord(EventResultInspection)
	record.Inspection.Status = action.InspectionIncomplete
	record.Decision.Decision = action.DecisionBlock
	record.Decision.Reason = action.ReasonInspectionIncomplete
	record.Decision.Completeness.ContextComplete = false
	record.Decision.Completeness.Missing = []action.MissingEvidence{{
		Field: action.EvidenceContext, Reason: action.ReasonInspectionIncomplete,
	}}
	if _, _, err := Seal(record, 1, ""); err != nil {
		t.Fatalf("explicit incomplete inspection was rejected: %v", err)
	}
}

func TestMultipleUnavailableSelectedIdentitiesRemainDistinctByPolicyDeclaration(t *testing.T) {
	record := testLedgerRecord(EventPreDecision)
	record.SelectedFields = []SelectedFieldEvidence{
		{DeclarationIndex: 0, Source: action.SourceArguments, State: action.PointerMissing, Complete: false},
		{DeclarationIndex: 1, Source: action.SourceArguments, State: action.PointerMissing, Complete: false},
	}
	record.Decision.Completeness.IdentityComplete = false
	record.Decision.Completeness.Missing = []action.MissingEvidence{{
		Field: action.EvidenceIdentity, Reason: action.ReasonIdentityUnavailable,
	}}
	if _, _, err := Seal(record, 1, ""); err != nil {
		t.Fatalf("Seal() rejected distinct unavailable selected declarations: %v", err)
	}
}

func TestDecodeRejectsTamperUnknownAndNonCanonicalJSON(t *testing.T) {
	_, body, err := Seal(testLedgerRecord(EventPreDecision), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body []byte
	}{
		{name: "tamper", body: bytes.Replace(body, []byte(`"release-operator"`), []byte(`"release-operat0r"`), 1)},
		{name: "unknown", body: append(append([]byte{}, body[:len(body)-1]...), []byte(`,"raw_arguments":"secret"}`)...)},
		{name: "noncanonical", body: append([]byte(" "), body...)},
		{name: "trailing", body: append(append([]byte{}, body...), []byte(`{}`)...)},
		{name: "oversized", body: bytes.Repeat([]byte{'x'}, MaxRecordBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Decode(test.body); err == nil {
				t.Fatalf("Decode() accepted %s input", test.name)
			}
		})
	}
}

func TestRecordValidationRejectsContradictoryEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{
			name: "missing completeness reason",
			mutate: func(record *Record) {
				record.Decision.Completeness.ContextComplete = false
			},
		},
		{
			name: "untrusted principal provenance",
			mutate: func(record *Record) {
				record.Call.ContextProvenance = action.ProvenanceAdapterAsserted
			},
		},
		{
			name: "permitting decision carries failure reason",
			mutate: func(record *Record) {
				record.Decision.Reason = action.ReasonCancelled
			},
		},
		{
			name: "warning carries failure reason",
			mutate: func(record *Record) {
				record.Decision.Decision = action.DecisionWarn
				record.Decision.Reason = action.ReasonCancelled
			},
		},
		{
			name: "approval decision carries rejection reason outside transition",
			mutate: func(record *Record) {
				record.Decision.Decision = action.DecisionRequireApproval
				record.Decision.Reason = action.ReasonApprovalRejected
				record.PreDecision.Outcome = action.OutcomeDispatchBlocked
			},
		},
		{
			name: "pending approval with receipt",
			mutate: func(record *Record) {
				record.Approval.AuthorityKeyID = "security-primary"
				record.Approval.ReceiptID = "arc_" + strings.Repeat("d", 26)
				record.Approval.ReceiptIdentity = "sha256:" + strings.Repeat("e", 64)
			},
		},
		{
			name: "approval request has a generic opaque ID",
			mutate: func(record *Record) {
				record.Approval.RequestID = "request-id"
			},
		},
		{
			name: "approval receipt has a generic opaque ID",
			mutate: func(record *Record) {
				record.Approval.Status = actionapproval.StatusApproved
				record.Approval.AuthorityKeyID = "security-primary"
				record.Approval.ReceiptID = "receipt-id"
				record.Approval.ReceiptIdentity = "sha256:" + strings.Repeat("e", 64)
			},
		},
		{
			name: "approval transition carries partial receipt provenance",
			mutate: func(record *Record) {
				record.Approval.Status = actionapproval.StatusExpired
				record.Approval.AuthorityKeyID = "security-primary"
				record.Decision.Reason = action.ReasonApprovalExpired
			},
		},
		{
			name: "approval transition status and reason disagree",
			mutate: func(record *Record) {
				record.Approval.Status = actionapproval.StatusRejected
				record.Approval.AuthorityKeyID = "security-primary"
				record.Approval.ReceiptID = "arc_" + strings.Repeat("d", 26)
				record.Approval.ReceiptIdentity = "sha256:" + strings.Repeat("e", 64)
			},
		},
		{
			name: "approval transition carries permitting decision",
			mutate: func(record *Record) {
				record.Decision.Decision = action.DecisionAllow
				record.Decision.Reason = action.ReasonDeclaredTool
			},
		},
		{
			name: "negative reservation",
			mutate: func(record *Record) {
				record.Budget.ReservedDelta.CallCount = -1
			},
		},
		{
			name: "budget denial carries permitting decision",
			mutate: func(record *Record) {
				record.Budget.Kind = BudgetDenied
				record.Budget.ReservedDelta = BudgetDelta{CallCount: -1}
				record.Budget.ConsumedDelta = BudgetDelta{DeniedCount: 1}
			},
		},
		{
			name: "budget transition omits budget identity",
			mutate: func(record *Record) {
				record.Budget.BudgetIDs = []string{}
			},
		},
		{
			name: "budget denial omits reservation identity",
			mutate: func(record *Record) {
				record.Budget.Kind = BudgetDenied
				record.Budget.ReservationIdentity = "absent"
				record.Budget.ReservedDelta = BudgetDelta{CallCount: -1}
				record.Budget.ConsumedDelta = BudgetDelta{DeniedCount: 1}
				record.Decision.Decision = action.DecisionBlock
				record.Decision.Reason = action.ReasonRuleMatched
			},
		},
		{
			name: "non-denial transition carries denied count",
			mutate: func(record *Record) {
				record.Budget.ReservedDelta.DeniedCount = 1
			},
		},
		{
			name: "budget release carries permitting decision",
			mutate: func(record *Record) {
				record.Budget.Kind = BudgetReleased
				record.Budget.ReservedDelta = BudgetDelta{CallCount: -1}
			},
		},
		{
			name: "budget dispatch commitment carries blocking decision",
			mutate: func(record *Record) {
				record.Budget.Kind = BudgetDispatched
				record.Budget.ReservedDelta = BudgetDelta{CallCount: -1}
				record.Budget.ConsumedDelta = BudgetDelta{CallCount: 1}
				record.Decision.Decision = action.DecisionBlock
				record.Decision.Reason = action.ReasonRuleMatched
			},
		},
		{
			name: "budget settlement uses pre-call phase",
			mutate: func(record *Record) {
				record.Budget.Kind = BudgetSettled
				record.Budget.ReservedDelta = BudgetDelta{}
				record.Budget.ConsumedDelta = BudgetDelta{ResultBytes: 1}
			},
		},
		{
			name: "successful downstream with failure reason",
			mutate: func(record *Record) {
				record.Decision.Reason = action.ReasonDownstreamError
			},
		},
		{
			name: "successful downstream carries blocking decision",
			mutate: func(record *Record) {
				record.Decision.Decision = action.DecisionBlock
				record.Decision.Reason = action.ReasonResultWithheld
			},
		},
		{
			name: "latency over hard maximum",
			mutate: func(record *Record) {
				record.LatencyMicros = maxLatencyMicros + 1
			},
		},
		{
			name: "selected field bytes over hard maximum",
			mutate: func(record *Record) {
				record.SelectedFields[0].ByteLength = action.MaxArgumentBytes + 1
			},
		},
		{
			name: "selected field items over hard maximum",
			mutate: func(record *Record) {
				record.SelectedFields[0].ItemCount = action.MaxJSONItems + 1
			},
		},
		{
			name: "accepted request bytes over hard maximum",
			mutate: func(record *Record) {
				record.RequestAccepted.ArgumentBytes = action.MaxArgumentBytes + 1
			},
		},
		{
			name: "accepted request items over hard maximum",
			mutate: func(record *Record) {
				record.RequestAccepted.ArgumentItems = action.MaxJSONItems + 1
			},
		},
		{
			name: "inspection bytes over hard maximum",
			mutate: func(record *Record) {
				record.Inspection.ScannedBytes = action.MaxArgumentBytes + 1
			},
		},
		{
			name: "inspection items over hard maximum",
			mutate: func(record *Record) {
				record.Inspection.ScannedItems = action.MaxJSONItems + 1
			},
		},
		{
			name: "unsupported content over hard maximum",
			mutate: func(record *Record) {
				record.Inspection.UnsupportedContent = action.MaxJSONItems + 1
			},
		},
		{
			name: "delivery bytes over hard maximum",
			mutate: func(record *Record) {
				record.Delivery.ByteLength = action.MaxArgumentBytes + 1
			},
		},
		{
			name: "delivery items over hard maximum",
			mutate: func(record *Record) {
				record.Delivery.ItemCount = action.MaxJSONItems + 1
			},
		},
		{
			name: "null pointer with non-null kind",
			mutate: func(record *Record) {
				record.SelectedFields[0].State = action.PointerNull
			},
		},
		{
			name: "present pointer with null kind",
			mutate: func(record *Record) {
				record.SelectedFields[0].Kind = action.ValueNull
			},
		},
		{
			name: "scalar selected field with item count",
			mutate: func(record *Record) {
				record.SelectedFields[0].ItemCount = 1
			},
		},
		{
			name: "incomplete selected field retains keyed identity",
			mutate: func(record *Record) {
				record.SelectedFields[0].Complete = false
				record.SelectedFields[0].ValueIdentity = ""
				record.Decision.Completeness.IdentityComplete = false
				record.Decision.Completeness.Missing = []action.MissingEvidence{{
					Field: action.EvidenceIdentity, Reason: action.ReasonIdentityUnavailable,
				}}
			},
		},
		{
			name: "incomplete selected field lacks unavailable identity reason",
			mutate: func(record *Record) {
				record.SelectedFields[0].Complete = false
				record.SelectedFields[0].PointerIdentity = ""
				record.SelectedFields[0].ValueIdentity = ""
				record.Decision.Completeness.IdentityComplete = false
				record.Decision.Completeness.Missing = []action.MissingEvidence{{
					Field: action.EvidenceIdentity, Reason: action.ReasonStateUnavailable,
				}}
			},
		},
		{
			name: "pre decision outcome contradicts decision",
			mutate: func(record *Record) {
				record.Decision.Decision = action.DecisionBlock
				record.Decision.Reason = action.ReasonRuleMatched
			},
		},
		{
			name: "pre decision uses post-result phase",
			mutate: func(record *Record) {
				record.Decision.Phase = action.PhasePostResult
				record.PreDecision.Outcome = action.OutcomeDeliveryEligible
			},
		},
		{
			name: "selected field source disagrees with phase",
			mutate: func(record *Record) {
				record.SelectedFields[0].Source = action.SourceResult
			},
		},
		{
			name: "failed downstream permits delivery",
			mutate: func(record *Record) {
				record.Downstream.Status = DownstreamFailed
				record.Decision.Reason = action.ReasonDownstreamError
			},
		},
		{
			name: "clean inspection carries category",
			mutate: func(record *Record) {
				record.Inspection.Categories = []action.DetectorCategory{action.DetectorSecret}
			},
		},
		{
			name: "incomplete inspection claims complete evidence",
			mutate: func(record *Record) {
				record.Inspection.Status = action.InspectionIncomplete
			},
		},
		{
			name: "incomplete inspection carries permitting decision",
			mutate: func(record *Record) {
				record.Inspection.Status = action.InspectionIncomplete
				record.Decision.Completeness.ContextComplete = false
				record.Decision.Completeness.Missing = []action.MissingEvidence{{
					Field: action.EvidenceContext, Reason: action.ReasonInspectionIncomplete,
				}}
			},
		},
		{
			name: "incomplete inspection carries unrelated failure reason",
			mutate: func(record *Record) {
				record.Inspection.Status = action.InspectionIncomplete
				record.Decision.Decision = action.DecisionBlock
				record.Decision.Reason = action.ReasonDownstreamError
				record.Decision.Completeness.ContextComplete = false
				record.Decision.Completeness.Missing = []action.MissingEvidence{{
					Field: action.EvidenceContext, Reason: action.ReasonDownstreamError,
				}}
			},
		},
		{
			name: "matched inspection carries allow decision",
			mutate: func(record *Record) {
				record.Inspection.Status = action.InspectionMatched
				record.Inspection.Categories = []action.DetectorCategory{action.DetectorSecret}
			},
		},
		{
			name: "failed schema claims clean inspection",
			mutate: func(record *Record) {
				record.Inspection.SchemaStatus = action.InspectionSchemaInvalid
			},
		},
		{
			name: "forwarded delivery carries blocked decision",
			mutate: func(record *Record) {
				record.Decision.Decision = action.DecisionBlock
				record.Decision.Reason = action.ReasonResultWithheld
			},
		},
		{
			name: "withheld delivery carries permitting decision",
			mutate: func(record *Record) {
				record.Delivery.Status = DeliveryWithheld
			},
		},
		{
			name: "terminal failure carries permitting decision",
			mutate: func(record *Record) {
				record.Decision.Decision = action.DecisionAllow
				record.Decision.Reason = action.ReasonDeclaredTool
				record.Failure.Lifecycle = action.LifecycleActive
			},
		},
		{
			name: "active terminal failure carries cancellation reason",
			mutate: func(record *Record) {
				record.Failure.Lifecycle = action.LifecycleActive
			},
		},
		{
			name: "unknown terminal lifecycle claims complete state evidence",
			mutate: func(record *Record) {
				record.Failure.DispatchKnown = false
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := EventPreDecision
			switch test.name {
			case "pending approval with receipt", "approval transition carries partial receipt provenance",
				"approval transition status and reason disagree", "approval transition carries permitting decision",
				"approval request has a generic opaque ID", "approval receipt has a generic opaque ID":
				event = EventApprovalTransition
			case "negative reservation", "budget denial carries permitting decision",
				"budget transition omits budget identity", "budget denial omits reservation identity",
				"non-denial transition carries denied count", "budget release carries permitting decision",
				"budget dispatch commitment carries blocking decision",
				"budget settlement uses pre-call phase":
				event = EventBudgetTransition
			case "successful downstream with failure reason", "successful downstream carries blocking decision":
				event = EventDownstreamOutcome
			case "accepted request bytes over hard maximum", "accepted request items over hard maximum":
				event = EventRequestAccepted
			case "inspection bytes over hard maximum", "inspection items over hard maximum",
				"unsupported content over hard maximum":
				event = EventResultInspection
			case "delivery bytes over hard maximum", "delivery items over hard maximum":
				event = EventFinalDelivery
			case "pre decision outcome contradicts decision", "pre decision uses post-result phase",
				"selected field source disagrees with phase":
				event = EventPreDecision
			case "failed downstream permits delivery":
				event = EventDownstreamOutcome
			case "clean inspection carries category", "incomplete inspection claims complete evidence",
				"incomplete inspection carries permitting decision", "matched inspection carries allow decision",
				"failed schema claims clean inspection", "incomplete inspection carries unrelated failure reason":
				event = EventResultInspection
			case "forwarded delivery carries blocked decision", "withheld delivery carries permitting decision":
				event = EventFinalDelivery
			case "terminal failure carries permitting decision", "active terminal failure carries cancellation reason",
				"unknown terminal lifecycle claims complete state evidence":
				event = EventTerminalFailure
			}
			record := testLedgerRecord(event)
			test.mutate(&record)
			if _, _, err := Seal(record, 1, ""); err == nil {
				t.Fatalf("Seal() accepted contradictory evidence")
			}
		})
	}
}

func TestLedgerDomainTypesCannotCarryRawPayloads(t *testing.T) {
	visited := map[reflect.Type]bool{}
	var inspect func(reflect.Type)
	inspect = func(value reflect.Type) {
		for value.Kind() == reflect.Pointer || value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
			if value.Kind() == reflect.Slice && value.Elem().Kind() == reflect.Uint8 {
				t.Fatalf("ledger type %s can carry raw bytes", value)
			}
			value = value.Elem()
		}
		if visited[value] {
			return
		}
		visited[value] = true
		switch value.Kind() {
		case reflect.Interface, reflect.Map:
			t.Fatalf("ledger type %s can carry arbitrary metadata", value)
		case reflect.Struct:
			if value.PkgPath() != "reconc.dev/reconc/internal/actionledger" {
				return
			}
			for index := 0; index < value.NumField(); index++ {
				inspect(value.Field(index).Type)
			}
		}
	}
	inspect(reflect.TypeOf(Record{}))

	record := testLedgerRecord(EventPreDecision)
	_, body, err := Seal(record, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"synthetic-secret", "raw_arguments", "raw_result", "authorization_header",
		"environment_value", "stderr", "prompt", "_meta",
	} {
		if bytes.Contains(body, []byte(forbidden)) {
			t.Fatalf("serialized ledger contains forbidden payload %q", forbidden)
		}
	}
}

func FuzzDecode(f *testing.F) {
	_, body, err := Seal(testLedgerRecord(EventPreDecision), 1, "")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(body)
	f.Add([]byte(`{"schema":"reconc.action-ledger-event/v1"}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = Decode(input)
	})
}
