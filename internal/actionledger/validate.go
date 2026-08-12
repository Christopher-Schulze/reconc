package actionledger

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

var (
	callIDPattern      = regexp.MustCompile(`^act_[a-z2-7]{26}$`)
	lowerDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const maxLatencyMicros = uint64((10 * time.Minute) / time.Microsecond)

func (r Record) Validate() error {
	if err := r.validateEnvelope(); err != nil {
		return err
	}
	if err := r.Call.validate(); err != nil {
		return fmt.Errorf("call binding: %w", err)
	}
	if err := r.Decision.validate(r.Event); err != nil {
		return fmt.Errorf("decision binding: %w", err)
	}
	if err := validateSelectedFields(r.SelectedFields, r.Decision.Completeness, r.Decision.Phase); err != nil {
		return err
	}
	return r.validateEvent()
}

func (r Record) validateEnvelope() error {
	if r.Schema != Schema || r.FormatVersion != FormatVersion || r.ChainVersion != ChainVersion {
		return fmt.Errorf("ledger schema, format, or chain version is invalid")
	}
	if r.Sequence == 0 || !r.Event.Valid() {
		return fmt.Errorf("ledger sequence or event is invalid")
	}
	if r.Sequence == 1 && r.PreviousDigest != "" || r.Sequence > 1 && !lowerDigestPattern.MatchString(r.PreviousDigest) {
		return fmt.Errorf("ledger previous digest does not match the sequence")
	}
	if r.Digest != "" && !lowerDigestPattern.MatchString(r.Digest) {
		return fmt.Errorf("ledger digest is invalid")
	}
	if r.LatencyMicros > maxLatencyMicros {
		return fmt.Errorf("ledger latency exceeds the hard maximum")
	}
	parsed, err := time.Parse(time.RFC3339Nano, r.Timestamp)
	if err != nil || parsed.IsZero() || r.Timestamp != parsed.UTC().Format(time.RFC3339Nano) {
		return fmt.Errorf("ledger timestamp must be canonical UTC RFC3339Nano")
	}
	return nil
}

func (c CallBinding) validate() error {
	if !callIDPattern.MatchString(c.CallID) || !action.ValidKeyedIdentity(c.RepositoryIdentity) ||
		!lowerDigestPattern.MatchString(c.PolicyDigest) || !lowerDigestPattern.MatchString(c.LockDigest) ||
		!action.SafeLabel(c.ServerLabel) || !action.ValidKeyedIdentity(c.ServerFingerprint) ||
		!action.ValidSHA256Identity(c.ToolContractDigest) || !action.SafeLabel(c.Principal) ||
		!action.ValidKeyedIdentity(c.ContextIdentity) || c.ContextProvenance != action.ProvenanceOperatorBound {
		return fmt.Errorf("required call identity is invalid")
	}
	for _, optional := range []string{c.RequestIdentity, c.RunIdentity, c.SessionIdentity} {
		if optional != "" && !action.ValidKeyedIdentity(optional) {
			return fmt.Errorf("optional call identity is invalid")
		}
	}
	if err := c.Tool.validate(); err != nil {
		return err
	}
	return validateSafeLabels(c.CredentialLabels, action.MaxCredentialLabels, "credential labels")
}

func (t ToolIdentity) validate() error {
	if !t.Mode.Valid() {
		return fmt.Errorf("tool identity mode is invalid")
	}
	switch t.Mode {
	case action.LedgerDeclarationID:
		if !action.SafeLabel(t.Value) {
			return fmt.Errorf("tool declaration identity is invalid")
		}
	case action.LedgerExactName:
		if !validGatewayToolName(t.Value) {
			return fmt.Errorf("exact tool name is invalid")
		}
	case action.LedgerKeyedName:
		if !action.ValidKeyedIdentity(t.Value) {
			return fmt.Errorf("keyed tool name is invalid")
		}
	}
	return nil
}

func validGatewayToolName(value string) bool {
	if value == "" || len(value) > action.MaxGatewayToolNameBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.-", character) {
			return false
		}
	}
	return true
}

func (d DecisionBinding) validate(event EventType) error {
	if !d.Phase.Valid() || d.RuleIDs == nil {
		return fmt.Errorf("phase or explicit rule list is invalid")
	}
	if event == EventRequestAccepted {
		if d.Decision != "" || d.Reason != "" || len(d.RuleIDs) != 0 {
			return fmt.Errorf("request acceptance cannot invent a decision")
		}
	} else if !d.Decision.Valid() || !d.Reason.Valid() {
		return fmt.Errorf("event requires a valid decision and reason")
	}
	switch {
	case d.Decision == action.DecisionAllow && d.Reason != action.ReasonDeclaredTool &&
		d.Reason != action.ReasonHostUnmatched && d.Reason != action.ReasonRuleMatched:
		return fmt.Errorf("decision and reason are incompatible")
	case d.Decision == action.DecisionWarn && d.Reason != action.ReasonDeclaredTool &&
		d.Reason != action.ReasonRuleMatched:
		return fmt.Errorf("decision and reason are incompatible")
	case d.Decision == action.DecisionRequireApproval && event != EventApprovalTransition &&
		d.Reason != action.ReasonApprovalRequired:
		return fmt.Errorf("decision and reason are incompatible")
	}
	if err := validateSafeLabels(d.RuleIDs, MaxRuleIDs, "rule IDs"); err != nil {
		return err
	}
	return validateCompleteness(d.Completeness)
}

func validateCompleteness(value action.Completeness) error {
	if value.Missing == nil || len(value.Missing) > 6 {
		return fmt.Errorf("completeness missing evidence must be an explicit bounded array")
	}
	normalized, err := action.NormalizeCompleteness(value)
	if err != nil {
		return fmt.Errorf("completeness is invalid: %w", err)
	}
	for index := range value.Missing {
		if value.Missing[index] != normalized.Missing[index] {
			return fmt.Errorf("completeness missing evidence must be canonically sorted")
		}
	}
	return nil
}

func validateSelectedFields(
	fields []SelectedFieldEvidence,
	completeness action.Completeness,
	phase action.Phase,
) error {
	if fields == nil || len(fields) > MaxSelectedFields {
		return fmt.Errorf("selected fields must be an explicit array of at most %d entries", MaxSelectedFields)
	}
	incomplete := false
	for index, field := range fields {
		if err := field.validate(); err != nil {
			return fmt.Errorf("selected field %d: %w", index, err)
		}
		if phase == action.PhasePreCall && field.Source != action.SourceArguments ||
			phase == action.PhasePostResult && field.Source != action.SourceResult ||
			phase != action.PhasePreCall && phase != action.PhasePostResult {
			return fmt.Errorf("selected field %d source is unavailable in phase %s", index, phase)
		}
		if index > 0 && !selectedFieldLess(fields[index-1], field) {
			return fmt.Errorf("selected fields must be uniquely sorted")
		}
		if !field.Complete && completeness.Complete() {
			return fmt.Errorf("incomplete selected field requires incomplete event evidence")
		}
		incomplete = incomplete || !field.Complete
	}
	if incomplete && (completeness.IdentityComplete || !hasMissingEvidence(
		completeness.Missing, action.EvidenceIdentity, action.ReasonIdentityUnavailable,
	)) {
		return fmt.Errorf("incomplete selected field requires explicit unavailable identity evidence")
	}
	return nil
}

func hasMissingEvidence(values []action.MissingEvidence, field action.EvidenceField, reason action.ReasonCode) bool {
	for _, value := range values {
		if value.Field == field && value.Reason == reason {
			return true
		}
	}
	return false
}

func (f SelectedFieldEvidence) validate() error {
	if f.DeclarationIndex >= MaxSelectedFields ||
		f.Source != action.SourceArguments && f.Source != action.SourceResult ||
		f.PointerIdentity != "" && !action.ValidKeyedIdentity(f.PointerIdentity) ||
		f.ByteLength > action.MaxArgumentBytes || f.ItemCount > action.MaxJSONItems {
		return fmt.Errorf("source or pointer identity is invalid")
	}
	available := f.State == action.PointerPresent || f.State == action.PointerNull
	if !validPointerState(f.State) || available != validValueKind(f.Kind) {
		return fmt.Errorf("pointer state and value kind disagree")
	}
	if f.State == action.PointerNull && f.Kind != action.ValueNull ||
		f.State == action.PointerPresent && f.Kind == action.ValueNull {
		return fmt.Errorf("pointer null state and value kind disagree")
	}
	if f.Kind != action.ValueArray && f.Kind != action.ValueObject && f.ItemCount != 0 {
		return fmt.Errorf("scalar selected field carries a non-zero item count")
	}
	if available {
		if f.ByteLength == 0 || f.Complete && !action.ValidKeyedIdentity(f.ValueIdentity) ||
			!f.Complete && f.ValueIdentity != "" {
			return fmt.Errorf("available value evidence is invalid")
		}
	} else if f.ValueIdentity != "" || f.ByteLength != 0 || f.ItemCount != 0 || f.Kind != "" {
		return fmt.Errorf("unavailable field carries value evidence")
	}
	if f.Complete && f.PointerIdentity == "" {
		return fmt.Errorf("complete selected field lacks a keyed pointer identity")
	}
	if !f.Complete && (f.PointerIdentity != "" || f.ValueIdentity != "") {
		return fmt.Errorf("incomplete selected field carries a keyed identity")
	}
	return nil
}

func selectedFieldLess(left, right SelectedFieldEvidence) bool {
	return left.DeclarationIndex < right.DeclarationIndex
}

func validPointerState(value action.PointerState) bool {
	switch value {
	case action.PointerPresent, action.PointerNull, action.PointerMissing,
		action.PointerWrongContainer, action.PointerInvalidIndex:
		return true
	default:
		return false
	}
}

func validValueKind(value action.ValueKind) bool {
	switch value {
	case action.ValueNull, action.ValueBool, action.ValueNumber, action.ValueString,
		action.ValueArray, action.ValueObject:
		return true
	default:
		return false
	}
}

func validateSafeLabels(values []string, maximum int, name string) error {
	if values == nil || len(values) > maximum || !sort.StringsAreSorted(values) {
		return fmt.Errorf("%s must be an explicit sorted array of at most %d values", name, maximum)
	}
	for index, value := range values {
		if !action.SafeLabel(value) || index > 0 && values[index-1] == value {
			return fmt.Errorf("%s contains an invalid or duplicate value", name)
		}
	}
	return nil
}

func (r Record) validateEvent() error {
	if payloadCount(r) != 1 {
		return fmt.Errorf("ledger record must contain exactly one typed event payload")
	}
	switch r.Event {
	case EventRequestAccepted:
		return validateRequestAccepted(r)
	case EventPreDecision:
		return validatePreDecision(r)
	case EventApprovalTransition:
		return validateApprovalTransition(r)
	case EventBudgetTransition:
		return validateBudgetTransition(r)
	case EventDownstreamDispatch:
		return validateDispatch(r)
	case EventDownstreamOutcome:
		return validateDownstream(r)
	case EventResultInspection:
		return validateInspection(r)
	case EventFinalDelivery:
		return validateDelivery(r)
	case EventTerminalFailure:
		return validateFailure(r)
	default:
		return fmt.Errorf("ledger event is invalid")
	}
}

func payloadCount(r Record) int {
	count := 0
	for _, present := range []bool{
		r.RequestAccepted != nil, r.PreDecision != nil, r.Approval != nil,
		r.Budget != nil, r.Dispatch != nil, r.Downstream != nil,
		r.Inspection != nil, r.Delivery != nil, r.Failure != nil,
	} {
		if present {
			count++
		}
	}
	return count
}

func validateRequestAccepted(r Record) error {
	if r.RequestAccepted == nil || r.Decision.Phase != action.PhasePreCall ||
		r.RequestAccepted.ArgumentBytes > action.MaxArgumentBytes ||
		r.RequestAccepted.ArgumentItems > action.MaxJSONItems {
		return fmt.Errorf("request_accepted payload or phase is invalid")
	}
	return nil
}

func validatePreDecision(r Record) error {
	if r.PreDecision == nil || !r.PreDecision.Outcome.Valid() {
		return fmt.Errorf("pre_decision outcome is invalid")
	}
	if r.Decision.Phase != action.PhasePreCall && r.Decision.Phase != action.PhaseProgress &&
		r.Decision.Phase != action.PhaseObservation {
		return fmt.Errorf("pre_decision phase is invalid")
	}
	if r.PreDecision.Outcome != action.OutcomeFor(r.Decision.Phase, r.Decision.Decision) {
		return fmt.Errorf("pre_decision outcome contradicts its phase and decision")
	}
	return nil
}

func validateApprovalTransition(r Record) error {
	value := r.Approval
	if value == nil || !actionapproval.ValidRequestID(value.RequestID) || !value.Status.Valid() ||
		!action.SafeLabel(value.AuthorityPolicyID) || value.AuthorityKeyID != "" && !action.SafeLabel(value.AuthorityKeyID) ||
		value.ReceiptID != "" && !actionapproval.ValidReceiptID(value.ReceiptID) ||
		value.ReceiptIdentity != "" && !action.ValidSHA256Identity(value.ReceiptIdentity) ||
		(r.Decision.Phase != action.PhasePreCall && r.Decision.Phase != action.PhasePostResult) ||
		r.Decision.Decision != action.DecisionRequireApproval {
		return fmt.Errorf("approval_transition payload is invalid")
	}
	hasReceipt := value.AuthorityKeyID != "" && value.ReceiptID != "" && value.ReceiptIdentity != ""
	receiptFields := 0
	for _, field := range []string{value.AuthorityKeyID, value.ReceiptID, value.ReceiptIdentity} {
		if field != "" {
			receiptFields++
		}
	}
	if receiptFields != 0 && receiptFields != 3 {
		return fmt.Errorf("approval transition carries partial receipt provenance")
	}
	if value.Status == actionapproval.StatusApproved || value.Status == actionapproval.StatusRejected {
		if !hasReceipt {
			return fmt.Errorf("signed approval transition lacks receipt provenance")
		}
	} else if value.Status == actionapproval.StatusPending &&
		(value.AuthorityKeyID != "" || value.ReceiptID != "" || value.ReceiptIdentity != "") {
		return fmt.Errorf("pending approval transition carries receipt provenance")
	}
	if r.Decision.Reason != approvalTransitionReason(value.Status) {
		return fmt.Errorf("approval transition status and reason disagree")
	}
	return nil
}

func approvalTransitionReason(status actionapproval.Status) action.ReasonCode {
	switch status {
	case actionapproval.StatusPending, actionapproval.StatusApproved:
		return action.ReasonApprovalRequired
	case actionapproval.StatusRejected:
		return action.ReasonApprovalRejected
	case actionapproval.StatusExpired:
		return action.ReasonApprovalExpired
	case actionapproval.StatusCancelled:
		return action.ReasonCancelled
	case actionapproval.StatusUnavailable:
		return action.ReasonAuthorityUnavailable
	case actionapproval.StatusMalformed:
		return action.ReasonApprovalInvalid
	case actionapproval.StatusReplayed:
		return action.ReasonApprovalReplayed
	default:
		return ""
	}
}

func validateBudgetTransition(r Record) error {
	value := r.Budget
	if value == nil || !value.Kind.Valid() ||
		value.ReservationIdentity != "absent" && !action.ValidKeyedIdentity(value.ReservationIdentity) ||
		!action.ValidKeyedIdentity(value.StateVersion) {
		return fmt.Errorf("budget_transition identity is invalid")
	}
	if err := validateSafeLabels(value.BudgetIDs, MaxBudgetIDs, "budget IDs"); err != nil {
		return err
	}
	if len(value.BudgetIDs) == 0 {
		return fmt.Errorf("budget transition must identify at least one budget")
	}
	if value.Kind != BudgetDenied &&
		(value.ReservedDelta.DeniedCount != 0 || value.ConsumedDelta.DeniedCount != 0) {
		return fmt.Errorf("non-denial budget transition carries a denied-count delta")
	}
	switch value.Kind {
	case BudgetReleased, BudgetDispatched, BudgetDenied:
		if r.Decision.Phase != action.PhasePreCall {
			return fmt.Errorf("pre-dispatch budget transition has an invalid phase")
		}
	case BudgetReserved:
		if r.Decision.Phase != action.PhasePreCall && r.Decision.Phase != action.PhasePostResult {
			return fmt.Errorf("budget reservation transition has an invalid phase")
		}
	case BudgetSettled:
		if r.Decision.Phase != action.PhasePostResult {
			return fmt.Errorf("settled budget transition has an invalid phase")
		}
	case BudgetIndeterminate:
		if r.Decision.Phase != action.PhasePreCall && r.Decision.Phase != action.PhasePostResult {
			return fmt.Errorf("indeterminate budget transition has an invalid phase")
		}
	}
	if value.Kind == BudgetDenied {
		if value.ReservationIdentity == "absent" || !value.ReservedDelta.nonPositive() ||
			!value.ConsumedDelta.denialAndApprovalOnly() ||
			value.ReservedDelta.zero() && value.ConsumedDelta.zero() ||
			r.Decision.Decision != action.DecisionBlock {
			return fmt.Errorf("denied budget transition carries invalid deltas")
		}
		return nil
	}
	if value.ReservationIdentity == "absent" {
		return fmt.Errorf("budget transition lacks a reservation identity")
	}
	switch value.Kind {
	case BudgetReserved:
		if !value.ReservedDelta.nonNegativeNonZero() || !value.ConsumedDelta.zero() {
			return fmt.Errorf("reservation budget deltas are invalid")
		}
		if r.Decision.Phase == action.PhasePostResult &&
			(!value.ReservedDelta.positiveApprovalOnly() ||
				r.Decision.Decision != action.DecisionRequireApproval) {
			return fmt.Errorf("post-result approval reservation deltas are invalid")
		}
	case BudgetReleased:
		if !value.ReservedDelta.nonPositiveNonZero() || !value.ConsumedDelta.approvalOnly() ||
			r.Decision.Decision != action.DecisionBlock {
			return fmt.Errorf("release budget deltas are invalid")
		}
	case BudgetDispatched:
		if !value.ReservedDelta.nonPositive() || !value.ConsumedDelta.nonNegative() ||
			r.Decision.Decision != action.DecisionAllow && r.Decision.Decision != action.DecisionWarn &&
				r.Decision.Decision != action.DecisionRequireApproval {
			return fmt.Errorf("dispatch-committed budget deltas are invalid")
		}
	case BudgetSettled:
		if !value.ReservedDelta.nonPositive() || !value.ConsumedDelta.nonNegative() {
			return fmt.Errorf("committed budget deltas are invalid")
		}
	case BudgetIndeterminate:
		if !value.ReservedDelta.zero() || !value.ConsumedDelta.zero() ||
			r.Decision.Decision != action.DecisionBlock || r.Decision.Reason != action.ReasonReservationIndeterminate {
			return fmt.Errorf("indeterminate budget transition cannot invent a delta")
		}
	}
	return nil
}

func (d BudgetDelta) values() [8]int64 {
	return [8]int64{
		d.CallCount, d.DeniedCount, d.ApprovalCount, d.ArgumentBytes,
		d.ResultBytes, d.CostUnits, d.Concurrent, d.RateWindow,
	}
}

func (d BudgetDelta) zero() bool {
	for _, value := range d.values() {
		if value != 0 {
			return false
		}
	}
	return true
}

func (d BudgetDelta) nonNegative() bool {
	for _, value := range d.values() {
		if value < 0 {
			return false
		}
	}
	return true
}

func (d BudgetDelta) nonPositive() bool {
	for _, value := range d.values() {
		if value > 0 {
			return false
		}
	}
	return true
}

func (d BudgetDelta) nonNegativeNonZero() bool {
	return d.nonNegative() && !d.zero()
}

func (d BudgetDelta) nonPositiveNonZero() bool {
	return d.nonPositive() && !d.zero()
}

func (d BudgetDelta) denialAndApprovalOnly() bool {
	return d.DeniedCount >= 0 && d.ApprovalCount >= 0 && d.CallCount == 0 &&
		d.ArgumentBytes == 0 && d.ResultBytes == 0 && d.CostUnits == 0 &&
		d.Concurrent == 0 && d.RateWindow == 0
}

func (d BudgetDelta) approvalOnly() bool {
	return d.ApprovalCount >= 0 && d.CallCount == 0 && d.DeniedCount == 0 &&
		d.ArgumentBytes == 0 && d.ResultBytes == 0 && d.CostUnits == 0 &&
		d.Concurrent == 0 && d.RateWindow == 0
}

func (d BudgetDelta) positiveApprovalOnly() bool {
	return d.approvalOnly() && d.ApprovalCount > 0
}

func validateDispatch(r Record) error {
	if r.Dispatch == nil ||
		r.Dispatch.ReservationIdentity != "absent" && !action.ValidKeyedIdentity(r.Dispatch.ReservationIdentity) ||
		r.Decision.Phase != action.PhasePreCall ||
		r.Decision.Decision != action.DecisionAllow && r.Decision.Decision != action.DecisionWarn &&
			r.Decision.Decision != action.DecisionRequireApproval {
		return fmt.Errorf("downstream_dispatch payload is invalid")
	}
	return nil
}

func validateDownstream(r Record) error {
	if r.Downstream == nil || !r.Downstream.Status.Valid() || r.Decision.Phase != action.PhasePostResult {
		return fmt.Errorf("downstream_outcome payload is invalid")
	}
	switch r.Downstream.Status {
	case DownstreamSucceeded:
		if r.Decision.Decision != action.DecisionAllow && r.Decision.Decision != action.DecisionWarn ||
			r.Decision.Reason == action.ReasonDownstreamUnavailable ||
			r.Decision.Reason == action.ReasonDownstreamError ||
			r.Decision.Reason == action.ReasonDownstreamUnknown ||
			r.Decision.Reason == action.ReasonProtocolError {
			return fmt.Errorf("successful downstream outcome carries a failure reason")
		}
	case DownstreamFailed:
		if r.Decision.Decision != action.DecisionBlock ||
			r.Decision.Reason != action.ReasonDownstreamUnavailable &&
				r.Decision.Reason != action.ReasonDownstreamError &&
				r.Decision.Reason != action.ReasonProtocolError {
			return fmt.Errorf("failed downstream outcome lacks a failure reason")
		}
	case DownstreamUnknown:
		if r.Decision.Decision != action.DecisionBlock ||
			r.Decision.Reason != action.ReasonDownstreamUnknown &&
				r.Decision.Reason != action.ReasonCancelled &&
				r.Decision.Reason != action.ReasonDeadlineExceeded &&
				r.Decision.Reason != action.ReasonShutdown {
			return fmt.Errorf("unknown downstream outcome lacks an uncertainty reason")
		}
	}
	return nil
}

func validateInspection(r Record) error {
	value := r.Inspection
	if value == nil || r.Decision.Phase != action.PhasePostResult || !value.Status.Valid() ||
		!value.SchemaStatus.Valid() || value.Categories == nil ||
		len(value.Categories) > action.MaxDetectorCategories ||
		value.ScannedBytes > action.MaxArgumentBytes || value.ScannedItems > action.MaxJSONItems ||
		value.UnsupportedContent > action.MaxJSONItems {
		return fmt.Errorf("result_inspection payload is invalid")
	}
	if !sort.SliceIsSorted(value.Categories, func(i, j int) bool { return value.Categories[i] < value.Categories[j] }) {
		return fmt.Errorf("result inspection categories are unsorted")
	}
	for index, category := range value.Categories {
		if !category.Valid() || index > 0 && value.Categories[index-1] == category {
			return fmt.Errorf("result inspection category is invalid or duplicated")
		}
	}
	if value.Status == action.InspectionMatched && len(value.Categories) == 0 ||
		value.Status != action.InspectionMatched && len(value.Categories) != 0 {
		return fmt.Errorf("result inspection status and categories disagree")
	}
	if value.Status == action.InspectionIncomplete && r.Decision.Completeness.Complete() {
		return fmt.Errorf("incomplete result inspection claims complete evidence")
	}
	if value.Status == action.InspectionIncomplete &&
		(r.Decision.Decision != action.DecisionBlock || !ledgerInspectionFailureReason(r.Decision.Reason)) {
		return fmt.Errorf("incomplete result inspection lacks a blocking failure decision")
	}
	if value.Status == action.InspectionMatched &&
		!(r.Decision.Decision == action.DecisionWarn && r.Decision.Reason == action.ReasonRuleMatched ||
			r.Decision.Decision == action.DecisionBlock && r.Decision.Reason == action.ReasonResultWithheld) {
		return fmt.Errorf("matched result inspection has an invalid outcome")
	}
	if (value.SchemaStatus == action.InspectionSchemaInvalid ||
		value.SchemaStatus == action.InspectionSchemaRequired) &&
		value.Status != action.InspectionIncomplete {
		return fmt.Errorf("failed result schema status lacks incomplete inspection state")
	}
	return nil
}

func ledgerInspectionFailureReason(reason action.ReasonCode) bool {
	switch reason {
	case action.ReasonInspectionIncomplete, action.ReasonUnsupportedContent, action.ReasonSchemaInvalid,
		action.ReasonLimitExceeded, action.ReasonInvalidUTF8, action.ReasonCancelled,
		action.ReasonDeadlineExceeded:
		return true
	default:
		return false
	}
}

func validateDelivery(r Record) error {
	if r.Delivery == nil || !r.Delivery.Status.Valid() ||
		r.Delivery.ByteLength > action.MaxArgumentBytes || r.Delivery.ItemCount > action.MaxJSONItems {
		return fmt.Errorf("final_delivery payload is invalid")
	}
	if r.Delivery.Status == DeliverySuppressed && r.Decision.Phase != action.PhaseProgress ||
		r.Delivery.Status != DeliverySuppressed && r.Decision.Phase != action.PhasePostResult {
		return fmt.Errorf("final_delivery phase and status disagree")
	}
	permitted := r.Decision.Decision == action.DecisionAllow || r.Decision.Decision == action.DecisionWarn ||
		r.Decision.Decision == action.DecisionRequireApproval
	if r.Delivery.Status == DeliveryForwarded && !permitted ||
		r.Delivery.Status != DeliveryForwarded && permitted {
		return fmt.Errorf("final_delivery status contradicts its decision")
	}
	return nil
}

func validateFailure(r Record) error {
	if r.Failure == nil || !r.Failure.Lifecycle.Valid() || r.Decision.Decision != action.DecisionBlock {
		return fmt.Errorf("terminal_failure payload is invalid")
	}
	if (!r.Failure.DispatchKnown || !r.Failure.DeliveryKnown) && r.Decision.Completeness.StateComplete {
		return fmt.Errorf("terminal failure with unknown lifecycle state claims complete state evidence")
	}
	if r.Failure.Lifecycle == action.LifecycleCancelled && r.Decision.Reason != action.ReasonCancelled ||
		r.Failure.Lifecycle == action.LifecycleShutdown && r.Decision.Reason != action.ReasonShutdown ||
		r.Failure.Lifecycle == action.LifecycleActive &&
			(r.Decision.Reason == action.ReasonCancelled || r.Decision.Reason == action.ReasonShutdown) {
		return fmt.Errorf("terminal failure lifecycle and reason disagree")
	}
	return nil
}
