package schema_test

import (
	"encoding/json"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/schema"
)

func TestActionLedgerSchemaMatchesStrictPayloadFreeRecord(t *testing.T) {
	document := readCurrentSchemaDocument(t, schema.ActionLedger)
	assertPropertiesMatch(t, schemaRootProperties(t, document), actionledger.Record{})
	assertPropertiesMatch(t, schemaDefinition(t, document, "call"), actionledger.CallBinding{})
	assertPropertiesMatch(t, schemaDefinition(t, document, "decision"), actionledger.DecisionBinding{})
	assertPropertiesMatch(t, schemaDefinition(t, document, "selectedField"), actionledger.SelectedFieldEvidence{})
	assertPropertiesMatch(t, schemaDefinition(t, document, "requestAccepted"), actionledger.RequestAccepted{})
	assertPropertiesMatch(t, schemaDefinition(t, document, "preDecision"), actionledger.PreDecision{})
	assertPropertiesMatch(t, schemaDefinition(t, document, "approvalTransition"), actionledger.ApprovalTransition{})
	assertPropertiesMatch(t, schemaDefinition(t, document, "budgetTransition"), actionledger.BudgetTransition{})
	assertPropertiesMatch(t, schemaDefinition(t, document, "budgetDelta"), actionledger.BudgetDelta{})
	assertPropertiesMatch(t, schemaDefinition(t, document, "downstreamDispatch"), actionledger.DownstreamDispatch{})
	assertPropertiesMatch(t, schemaDefinition(t, document, "downstreamOutcome"), actionledger.DownstreamOutcome{})
	assertPropertiesMatch(t, schemaDefinition(t, document, "resultInspection"), actionledger.ResultInspection{})
	assertPropertiesMatch(t, schemaDefinition(t, document, "finalDelivery"), actionledger.FinalDelivery{})
	assertPropertiesMatch(t, schemaDefinition(t, document, "terminalFailure"), actionledger.TerminalFailure{})

	identity := func(fill string) string {
		return "hmac-sha256:v1:ledger-key:" + strings.Repeat(fill, 64)
	}
	record := actionledger.Record{
		Timestamp: "2026-08-11T12:00:00Z", Event: actionledger.EventRequestAccepted,
		Call: actionledger.CallBinding{
			CallID: "act_" + strings.Repeat("a", 26), RequestIdentity: identity("1"),
			RepositoryIdentity: identity("2"), PolicyDigest: strings.Repeat("3", 64),
			LockDigest: strings.Repeat("4", 64), ServerLabel: "warehouse",
			ServerFingerprint:  identity("5"),
			Tool:               actionledger.ToolIdentity{Mode: action.LedgerDeclarationID, Value: "database-write"},
			ToolContractDigest: "sha256:" + strings.Repeat("6", 64), Principal: "release-operator",
			CredentialLabels: []string{"production-database"}, RunIdentity: identity("7"),
			SessionIdentity: identity("8"), ContextIdentity: identity("9"),
			ContextProvenance: action.ProvenanceOperatorBound,
		},
		Decision: actionledger.DecisionBinding{
			Phase: action.PhasePreCall, RuleIDs: []string{}, Completeness: action.CompleteEvidence(),
		},
		SelectedFields: []actionledger.SelectedFieldEvidence{{
			Source: action.SourceArguments, PointerIdentity: identity("a"), State: action.PointerPresent,
			Kind: action.ValueString, ValueIdentity: identity("b"), ByteLength: 1, ItemCount: 0, Complete: true,
		}},
		RequestAccepted: &actionledger.RequestAccepted{ArgumentBytes: 2, ArgumentItems: 1},
	}
	_, body, err := actionledger.Seal(record, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]interface{}
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	compiled := compileRegisteredSchemas(t)
	contract, ok := schema.CurrentContract(schema.ActionLedger)
	if !ok {
		t.Fatal("action-ledger schema contract is absent")
	}
	if err := compiled[contract.DefaultURL].Validate(value); err != nil {
		t.Fatalf("valid action ledger rejected: %v", err)
	}
	for _, forbidden := range []string{"raw_arguments", "raw_result", "headers", "environment", "_meta"} {
		candidate := cloneJSONValue(t, value).(map[string]interface{})
		candidate[forbidden] = "synthetic-secret"
		if err := compiled[contract.DefaultURL].Validate(candidate); err == nil {
			t.Fatalf("action-ledger schema accepted forbidden field %q", forbidden)
		}
	}
	requestWithDecision := cloneJSONValue(t, value).(map[string]interface{})
	requestDecision := requestWithDecision["decision"].(map[string]interface{})
	requestDecision["decision"] = "allow"
	requestDecision["reason_code"] = "declared_tool"
	if err := compiled[contract.DefaultURL].Validate(requestWithDecision); err == nil {
		t.Fatal("action-ledger schema accepted a decision on request acceptance")
	}
	requestWrongPhase := cloneJSONValue(t, value).(map[string]interface{})
	requestWrongPhase["decision"].(map[string]interface{})["phase"] = "observation"
	if err := compiled[contract.DefaultURL].Validate(requestWrongPhase); err == nil {
		t.Fatal("action-ledger schema accepted a non-pre-call request acceptance")
	}
	for _, timestamp := range []string{"2026-08-11T14:00:00+02:00", "2026-08-11T12:00:00.100Z"} {
		nonCanonicalTimestamp := cloneJSONValue(t, value).(map[string]interface{})
		nonCanonicalTimestamp["timestamp"] = timestamp
		if err := compiled[contract.DefaultURL].Validate(nonCanonicalTimestamp); err == nil {
			t.Fatalf("action-ledger schema accepted non-canonical timestamp %q", timestamp)
		}
	}
	inconsistentCompleteness := cloneJSONValue(t, value).(map[string]interface{})
	inconsistentCompleteness["decision"].(map[string]interface{})["completeness"].(map[string]interface{})["context_complete"] = false
	if err := compiled[contract.DefaultURL].Validate(inconsistentCompleteness); err == nil {
		t.Fatal("action-ledger schema accepted incomplete evidence without a typed missing reason")
	}
	incompleteSelected := cloneJSONValue(t, value).(map[string]interface{})
	incompleteSelected["selected_fields"].([]interface{})[0].(map[string]interface{})["complete"] = false
	delete(incompleteSelected["selected_fields"].([]interface{})[0].(map[string]interface{}), "value_identity")
	if err := compiled[contract.DefaultURL].Validate(incompleteSelected); err == nil {
		t.Fatal("action-ledger schema accepted incomplete selected evidence under complete event evidence")
	}
	incompleteIdentityRecord := record
	incompleteIdentityRecord.SelectedFields = append([]actionledger.SelectedFieldEvidence(nil), record.SelectedFields...)
	incompleteIdentityRecord.SelectedFields[0].Complete = false
	incompleteIdentityRecord.SelectedFields[0].PointerIdentity = ""
	incompleteIdentityRecord.SelectedFields[0].ValueIdentity = ""
	incompleteIdentityRecord.Decision.Completeness.IdentityComplete = false
	incompleteIdentityRecord.Decision.Completeness.Missing = []action.MissingEvidence{{
		Field: action.EvidenceIdentity, Reason: action.ReasonIdentityUnavailable,
	}}
	incompleteIdentityValue := sealedLedgerValue(t, incompleteIdentityRecord)
	if err := compiled[contract.DefaultURL].Validate(incompleteIdentityValue); err != nil {
		t.Fatalf("valid unavailable selected identity rejected: %v", err)
	}
	incompleteIdentityValue["selected_fields"].([]interface{})[0].(map[string]interface{})["pointer_identity"] = identity("a")
	if err := compiled[contract.DefaultURL].Validate(incompleteIdentityValue); err == nil {
		t.Fatal("action-ledger schema accepted keyed identity on incomplete selected evidence")
	}
	incompleteIdentityValue = sealedLedgerValue(t, incompleteIdentityRecord)
	missingIdentity := incompleteIdentityValue["decision"].(map[string]interface{})["completeness"].(map[string]interface{})["missing"].([]interface{})
	missingIdentity[0].(map[string]interface{})["reason"] = "state_unavailable"
	if err := compiled[contract.DefaultURL].Validate(incompleteIdentityValue); err == nil {
		t.Fatal("action-ledger schema accepted incomplete selected evidence without identity_unavailable")
	}
	nullMismatch := cloneJSONValue(t, value).(map[string]interface{})
	nullMismatch["selected_fields"].([]interface{})[0].(map[string]interface{})["state"] = "null"
	if err := compiled[contract.DefaultURL].Validate(nullMismatch); err == nil {
		t.Fatal("action-ledger schema accepted null state with a non-null kind")
	}

	preDecision := record
	preDecision.Event = actionledger.EventPreDecision
	preDecision.RequestAccepted = nil
	preDecision.Decision.Decision = action.DecisionAllow
	preDecision.Decision.Reason = action.ReasonDeclaredTool
	preDecision.PreDecision = &actionledger.PreDecision{Outcome: action.OutcomeDispatchEligible}
	preValue := sealedLedgerValue(t, preDecision)
	if err := compiled[contract.DefaultURL].Validate(preValue); err != nil {
		t.Fatalf("valid pre-decision ledger rejected: %v", err)
	}
	allowWithFailureReason := cloneJSONValue(t, preValue).(map[string]interface{})
	allowWithFailureReason["decision"].(map[string]interface{})["reason_code"] = "cancelled"
	if err := compiled[contract.DefaultURL].Validate(allowWithFailureReason); err == nil {
		t.Fatal("action-ledger schema accepted a permitting decision with a failure reason")
	}
	warnWithDeclaredReason := cloneJSONValue(t, preValue).(map[string]interface{})
	warnWithDeclaredReason["decision"].(map[string]interface{})["decision"] = "warn"
	if err := compiled[contract.DefaultURL].Validate(warnWithDeclaredReason); err != nil {
		t.Fatalf("action-ledger schema rejected a declared-tool warning: %v", err)
	}
	warnWithFailureReason := cloneJSONValue(t, warnWithDeclaredReason).(map[string]interface{})
	warnWithFailureReason["decision"].(map[string]interface{})["reason_code"] = "cancelled"
	if err := compiled[contract.DefaultURL].Validate(warnWithFailureReason); err == nil {
		t.Fatal("action-ledger schema accepted a warning with a failure reason")
	}
	approvalWithRejectionReason := cloneJSONValue(t, preValue).(map[string]interface{})
	approvalDecision := approvalWithRejectionReason["decision"].(map[string]interface{})
	approvalDecision["decision"] = "require_approval"
	approvalDecision["reason_code"] = "approval_rejected"
	approvalWithRejectionReason["pre_decision"].(map[string]interface{})["outcome"] = "dispatch_blocked"
	if err := compiled[contract.DefaultURL].Validate(approvalWithRejectionReason); err == nil {
		t.Fatal("action-ledger schema accepted a pending approval decision with a rejection reason")
	}
	wrongPreCallSource := cloneJSONValue(t, preValue).(map[string]interface{})
	wrongPreCallSource["selected_fields"].([]interface{})[0].(map[string]interface{})["source"] = "result"
	if err := compiled[contract.DefaultURL].Validate(wrongPreCallSource); err == nil {
		t.Fatal("action-ledger schema accepted result evidence in the pre-call phase")
	}
	progressWithSelected := cloneJSONValue(t, preValue).(map[string]interface{})
	progressWithSelected["decision"].(map[string]interface{})["phase"] = "progress"
	progressWithSelected["pre_decision"].(map[string]interface{})["outcome"] = "progress_eligible"
	if err := compiled[contract.DefaultURL].Validate(progressWithSelected); err == nil {
		t.Fatal("action-ledger schema accepted argument evidence in the progress phase")
	}
	preValue["pre_decision"].(map[string]interface{})["outcome"] = "dispatch_blocked"
	if err := compiled[contract.DefaultURL].Validate(preValue); err == nil {
		t.Fatal("action-ledger schema accepted a contradictory pre-decision outcome")
	}
	postResultPreDecision := sealedLedgerValue(t, preDecision)
	postResultPreDecision["decision"].(map[string]interface{})["phase"] = "post_result"
	postResultPreDecision["pre_decision"].(map[string]interface{})["outcome"] = "delivery_eligible"
	postResultPreDecision["selected_fields"].([]interface{})[0].(map[string]interface{})["source"] = "result"
	if err := compiled[contract.DefaultURL].Validate(postResultPreDecision); err == nil {
		t.Fatal("action-ledger schema accepted a pre-decision event for post-result inspection")
	}
	observation := preDecision
	observation.Decision.Phase = action.PhaseObservation
	observation.Decision.Decision = action.DecisionBlock
	observation.Decision.Reason = action.ReasonRuleMatched
	observation.PreDecision.Outcome = action.OutcomeRecorded
	observation.SelectedFields = []actionledger.SelectedFieldEvidence{}
	if err := compiled[contract.DefaultURL].Validate(sealedLedgerValue(t, observation)); err != nil {
		t.Fatalf("valid observation ledger rejected: %v", err)
	}

	approval := record
	approval.Event = actionledger.EventApprovalTransition
	approval.RequestAccepted = nil
	approval.Decision.Decision = action.DecisionRequireApproval
	approval.Decision.Reason = action.ReasonApprovalRequired
	approval.Approval = &actionledger.ApprovalTransition{
		RequestID: "apr_" + strings.Repeat("c", 26),
		Status:    actionapproval.StatusPending, AuthorityPolicyID: "production-writes",
	}
	approvalValue := sealedLedgerValue(t, approval)
	if err := compiled[contract.DefaultURL].Validate(approvalValue); err != nil {
		t.Fatalf("valid approval transition rejected: %v", err)
	}
	genericApprovalRequest := cloneJSONValue(t, approvalValue).(map[string]interface{})
	genericApprovalRequest["approval_transition"].(map[string]interface{})["request_id"] = "request-id"
	if err := compiled[contract.DefaultURL].Validate(genericApprovalRequest); err == nil {
		t.Fatal("action-ledger schema accepted a generic approval request ID")
	}
	genericApprovalReceipt := cloneJSONValue(t, approvalValue).(map[string]interface{})
	genericApprovalReceipt["approval_transition"].(map[string]interface{})["status"] = "approved"
	genericApprovalReceipt["approval_transition"].(map[string]interface{})["authority_key_id"] = "security-primary"
	genericApprovalReceipt["approval_transition"].(map[string]interface{})["receipt_id"] = "receipt-id"
	genericApprovalReceipt["approval_transition"].(map[string]interface{})["receipt_identity"] = "sha256:" + strings.Repeat("e", 64)
	if err := compiled[contract.DefaultURL].Validate(genericApprovalReceipt); err == nil {
		t.Fatal("action-ledger schema accepted a generic approval receipt ID")
	}
	permittingApproval := cloneJSONValue(t, approvalValue).(map[string]interface{})
	permittingApproval["decision"].(map[string]interface{})["decision"] = "allow"
	if err := compiled[contract.DefaultURL].Validate(permittingApproval); err == nil {
		t.Fatal("action-ledger schema accepted a permitting approval transition")
	}
	wrongApprovalReason := cloneJSONValue(t, approvalValue).(map[string]interface{})
	wrongApprovalReason["decision"].(map[string]interface{})["reason_code"] = "approval_rejected"
	if err := compiled[contract.DefaultURL].Validate(wrongApprovalReason); err == nil {
		t.Fatal("action-ledger schema accepted an approval status/reason contradiction")
	}
	partialApprovalReceipt := cloneJSONValue(t, approvalValue).(map[string]interface{})
	partialApprovalReceipt["approval_transition"].(map[string]interface{})["status"] = "expired"
	partialApprovalReceipt["approval_transition"].(map[string]interface{})["authority_key_id"] = "security-primary"
	partialApprovalReceipt["decision"].(map[string]interface{})["reason_code"] = "approval_expired"
	if err := compiled[contract.DefaultURL].Validate(partialApprovalReceipt); err == nil {
		t.Fatal("action-ledger schema accepted partial receipt provenance")
	}

	budget := record
	budget.Event = actionledger.EventBudgetTransition
	budget.RequestAccepted = nil
	budget.Decision.Decision = action.DecisionAllow
	budget.Decision.Reason = action.ReasonDeclaredTool
	budget.Budget = &actionledger.BudgetTransition{
		Kind: actionledger.BudgetReserved, ReservationIdentity: identity("c"),
		StateVersion: identity("d"), BudgetIDs: []string{"database-calls"},
		ReservedDelta: actionledger.BudgetDelta{CallCount: 1},
	}
	budgetValue := sealedLedgerValue(t, budget)
	if err := compiled[contract.DefaultURL].Validate(budgetValue); err != nil {
		t.Fatalf("valid budget reservation rejected: %v", err)
	}
	budgetValue["budget_transition"].(map[string]interface{})["reserved_delta"].(map[string]interface{})["call_count"] = float64(-1)
	if err := compiled[contract.DefaultURL].Validate(budgetValue); err == nil {
		t.Fatal("action-ledger schema accepted a negative reservation delta")
	}
	budgetOverflow := sealedLedgerValue(t, budget)
	budgetOverflow["budget_transition"].(map[string]interface{})["reserved_delta"].(map[string]interface{})["call_count"] = json.Number("9223372036854775808")
	if err := compiled[contract.DefaultURL].Validate(budgetOverflow); err == nil {
		t.Fatal("action-ledger schema accepted a budget delta outside int64")
	}
	denied := budget
	denied.Decision.Decision = action.DecisionBlock
	denied.Decision.Reason = action.ReasonRuleMatched
	denied.Budget = &actionledger.BudgetTransition{
		Kind: actionledger.BudgetDenied, ReservationIdentity: identity("c"),
		StateVersion: identity("d"), BudgetIDs: []string{"database-calls"},
		ReservedDelta: actionledger.BudgetDelta{CallCount: -1},
		ConsumedDelta: actionledger.BudgetDelta{DeniedCount: 1},
	}
	deniedValue := sealedLedgerValue(t, denied)
	if err := compiled[contract.DefaultURL].Validate(deniedValue); err != nil {
		t.Fatalf("valid budget denial rejected: %v", err)
	}
	deniedValue["decision"].(map[string]interface{})["decision"] = "allow"
	if err := compiled[contract.DefaultURL].Validate(deniedValue); err == nil {
		t.Fatal("action-ledger schema accepted a permitting budget denial")
	}
	deniedWithoutReservation := sealedLedgerValue(t, denied)
	deniedWithoutReservation["budget_transition"].(map[string]interface{})["reservation_identity"] = "absent"
	if err := compiled[contract.DefaultURL].Validate(deniedWithoutReservation); err == nil {
		t.Fatal("action-ledger schema accepted a budget denial without its reservation identity")
	}
	released := budget
	released.Decision.Decision = action.DecisionBlock
	released.Decision.Reason = action.ReasonCancelled
	released.Budget = &actionledger.BudgetTransition{
		Kind: actionledger.BudgetReleased, ReservationIdentity: identity("c"),
		StateVersion: identity("d"), BudgetIDs: []string{"database-calls"},
		ReservedDelta: actionledger.BudgetDelta{CallCount: -1},
	}
	releasedValue := sealedLedgerValue(t, released)
	if err := compiled[contract.DefaultURL].Validate(releasedValue); err != nil {
		t.Fatalf("valid budget release rejected: %v", err)
	}
	releasedValue["decision"].(map[string]interface{})["decision"] = "allow"
	if err := compiled[contract.DefaultURL].Validate(releasedValue); err == nil {
		t.Fatal("action-ledger schema accepted a permitting budget release")
	}
	committed := budget
	committed.Budget = &actionledger.BudgetTransition{
		Kind: actionledger.BudgetDispatched, ReservationIdentity: identity("c"),
		StateVersion: identity("d"), BudgetIDs: []string{"database-calls"},
		ReservedDelta: actionledger.BudgetDelta{CallCount: -1},
		ConsumedDelta: actionledger.BudgetDelta{CallCount: 1},
	}
	committedValue := sealedLedgerValue(t, committed)
	if err := compiled[contract.DefaultURL].Validate(committedValue); err != nil {
		t.Fatalf("valid budget dispatch commitment rejected: %v", err)
	}
	committedValue["decision"].(map[string]interface{})["decision"] = "block"
	if err := compiled[contract.DefaultURL].Validate(committedValue); err == nil {
		t.Fatal("action-ledger schema accepted a blocking budget dispatch commitment")
	}

	dispatch := record
	dispatch.Event = actionledger.EventDownstreamDispatch
	dispatch.RequestAccepted = nil
	dispatch.Decision.Decision = action.DecisionAllow
	dispatch.Decision.Reason = action.ReasonDeclaredTool
	dispatch.Dispatch = &actionledger.DownstreamDispatch{ReservationIdentity: "absent"}
	dispatchValue := sealedLedgerValue(t, dispatch)
	if err := compiled[contract.DefaultURL].Validate(dispatchValue); err != nil {
		t.Fatalf("valid no-budget dispatch rejected: %v", err)
	}
	dispatchValue["decision"].(map[string]interface{})["decision"] = "block"
	if err := compiled[contract.DefaultURL].Validate(dispatchValue); err == nil {
		t.Fatal("action-ledger schema accepted a blocking downstream dispatch")
	}

	downstream := record
	downstream.Event = actionledger.EventDownstreamOutcome
	downstream.RequestAccepted = nil
	downstream.Decision.Phase = action.PhasePostResult
	downstream.Decision.Decision = action.DecisionBlock
	downstream.Decision.Reason = action.ReasonDownstreamError
	downstream.SelectedFields = append([]actionledger.SelectedFieldEvidence(nil), record.SelectedFields...)
	downstream.SelectedFields[0].Source = action.SourceResult
	downstream.Downstream = &actionledger.DownstreamOutcome{Status: actionledger.DownstreamFailed}
	downstreamValue := sealedLedgerValue(t, downstream)
	if err := compiled[contract.DefaultURL].Validate(downstreamValue); err != nil {
		t.Fatalf("valid downstream outcome rejected: %v", err)
	}
	downstreamValue["decision"].(map[string]interface{})["decision"] = "allow"
	if err := compiled[contract.DefaultURL].Validate(downstreamValue); err == nil {
		t.Fatal("action-ledger schema accepted a permitting failed-downstream decision")
	}
	succeeded := downstream
	succeeded.Decision.Decision = action.DecisionAllow
	succeeded.Decision.Reason = action.ReasonDeclaredTool
	succeeded.Downstream = &actionledger.DownstreamOutcome{Status: actionledger.DownstreamSucceeded}
	succeededValue := sealedLedgerValue(t, succeeded)
	if err := compiled[contract.DefaultURL].Validate(succeededValue); err != nil {
		t.Fatalf("valid successful downstream outcome rejected: %v", err)
	}
	succeededValue["decision"].(map[string]interface{})["decision"] = "block"
	succeededValue["decision"].(map[string]interface{})["reason_code"] = "result_withheld"
	if err := compiled[contract.DefaultURL].Validate(succeededValue); err == nil {
		t.Fatal("action-ledger schema accepted a blocking successful-downstream decision")
	}

	inspection := record
	inspection.Event = actionledger.EventResultInspection
	inspection.RequestAccepted = nil
	inspection.Decision.Phase = action.PhasePostResult
	inspection.Decision.Decision = action.DecisionWarn
	inspection.Decision.Reason = action.ReasonRuleMatched
	inspection.SelectedFields = append([]actionledger.SelectedFieldEvidence(nil), record.SelectedFields...)
	inspection.SelectedFields[0].Source = action.SourceResult
	inspection.Inspection = &actionledger.ResultInspection{
		Status: action.InspectionMatched, Categories: []action.DetectorCategory{action.DetectorSecret},
		SchemaStatus: action.InspectionSchemaNotDeclared, ScannedBytes: 1, ScannedItems: 0,
	}
	inspectionValue := sealedLedgerValue(t, inspection)
	if err := compiled[contract.DefaultURL].Validate(inspectionValue); err != nil {
		t.Fatalf("valid matched inspection rejected: %v", err)
	}
	inspectionValue["decision"].(map[string]interface{})["decision"] = "allow"
	inspectionValue["decision"].(map[string]interface{})["reason_code"] = "declared_tool"
	if err := compiled[contract.DefaultURL].Validate(inspectionValue); err == nil {
		t.Fatal("action-ledger schema accepted a matched inspection without warn or block")
	}

	incomplete := record
	incomplete.Event = actionledger.EventResultInspection
	incomplete.RequestAccepted = nil
	incomplete.Decision.Phase = action.PhasePostResult
	incomplete.Decision.Decision = action.DecisionBlock
	incomplete.Decision.Reason = action.ReasonInspectionIncomplete
	incomplete.SelectedFields = append([]actionledger.SelectedFieldEvidence(nil), record.SelectedFields...)
	incomplete.SelectedFields[0].Source = action.SourceResult
	incomplete.Decision.Completeness.ContextComplete = false
	incomplete.Decision.Completeness.Missing = []action.MissingEvidence{{
		Field: action.EvidenceContext, Reason: action.ReasonInspectionIncomplete,
	}}
	incomplete.Inspection = &actionledger.ResultInspection{
		Status: action.InspectionIncomplete, Categories: []action.DetectorCategory{},
		SchemaStatus: action.InspectionSchemaNotDeclared, ScannedBytes: 1, ScannedItems: 0,
	}
	incompleteValue := sealedLedgerValue(t, incomplete)
	if err := compiled[contract.DefaultURL].Validate(incompleteValue); err != nil {
		t.Fatalf("valid incomplete inspection rejected: %v", err)
	}
	incompleteValue["decision"].(map[string]interface{})["decision"] = "allow"
	if err := compiled[contract.DefaultURL].Validate(incompleteValue); err == nil {
		t.Fatal("action-ledger schema accepted a permitting incomplete inspection")
	}
	incompleteWrongReason := sealedLedgerValue(t, incomplete)
	incompleteWrongReason["decision"].(map[string]interface{})["reason_code"] = "downstream_error"
	if err := compiled[contract.DefaultURL].Validate(incompleteWrongReason); err == nil {
		t.Fatal("action-ledger schema accepted an unrelated incomplete-inspection reason")
	}

	delivery := record
	delivery.Event = actionledger.EventFinalDelivery
	delivery.RequestAccepted = nil
	delivery.Decision.Phase = action.PhasePostResult
	delivery.Decision.Decision = action.DecisionAllow
	delivery.Decision.Reason = action.ReasonDeclaredTool
	delivery.SelectedFields = append([]actionledger.SelectedFieldEvidence(nil), record.SelectedFields...)
	delivery.SelectedFields[0].Source = action.SourceResult
	delivery.Delivery = &actionledger.FinalDelivery{Status: actionledger.DeliveryForwarded, ByteLength: 1, ItemCount: 1}
	deliveryValue := sealedLedgerValue(t, delivery)
	if err := compiled[contract.DefaultURL].Validate(deliveryValue); err != nil {
		t.Fatalf("valid final delivery rejected: %v", err)
	}
	deliveryWrongPhase := cloneJSONValue(t, deliveryValue).(map[string]interface{})
	deliveryWrongPhase["decision"].(map[string]interface{})["phase"] = "progress"
	if err := compiled[contract.DefaultURL].Validate(deliveryWrongPhase); err == nil {
		t.Fatal("action-ledger schema accepted forwarded delivery in the progress phase")
	}
	deliveryValue["decision"].(map[string]interface{})["decision"] = "block"
	if err := compiled[contract.DefaultURL].Validate(deliveryValue); err == nil {
		t.Fatal("action-ledger schema accepted forwarded delivery with a blocking decision")
	}

	failure := record
	failure.Event = actionledger.EventTerminalFailure
	failure.RequestAccepted = nil
	failure.Decision.Decision = action.DecisionBlock
	failure.Decision.Reason = action.ReasonCancelled
	failure.Failure = &actionledger.TerminalFailure{
		Lifecycle: action.LifecycleCancelled, DispatchKnown: true, DeliveryKnown: true,
	}
	failureValue := sealedLedgerValue(t, failure)
	if err := compiled[contract.DefaultURL].Validate(failureValue); err != nil {
		t.Fatalf("valid terminal failure rejected: %v", err)
	}
	failureValue["decision"].(map[string]interface{})["decision"] = "allow"
	if err := compiled[contract.DefaultURL].Validate(failureValue); err == nil {
		t.Fatal("action-ledger schema accepted a permitting terminal failure")
	}
}

func sealedLedgerValue(t *testing.T, record actionledger.Record) map[string]interface{} {
	t.Helper()
	_, body, err := actionledger.Seal(record, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]interface{}
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
