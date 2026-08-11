package impactlab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode"
	"unicode/utf8"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actioninspect"
)

func validateActionCase(kind CaseKind, scenario ActionCase) (int, error) {
	scanner, err := actioninspect.NewTextScanner()
	if err != nil {
		return 0, fmt.Errorf("prepare action privacy scanner: %w", err)
	}
	return validateActionCaseWithScanner(scanner, kind, scenario)
}

func validateActionCaseWithScanner(
	scanner *actioninspect.TextScanner,
	kind CaseKind,
	scenario ActionCase,
) (int, error) {
	if !action.SafeLabel(scenario.ToolID) || scenario.RedactionCount < 0 ||
		unsafeActionMetadata(scenario.ToolID) ||
		scenario.Request.Context == nil || scenario.State.CredentialLabels == nil ||
		scenario.Expected.MatchedRuleIDs == nil || scenario.Expected.Completeness.Missing == nil ||
		scenario.SelectedValues == nil {
		return 0, fmt.Errorf("action case has an invalid tool id, redaction count, or null collection")
	}
	if scenario.RedactionCount != len(scenario.SelectedValues) {
		return 0, fmt.Errorf("action redaction count does not match selected-value summaries")
	}
	if kind == CaseActionPre && scenario.Request.Phase != action.PhasePreCall ||
		kind == CaseActionPost && scenario.Request.Phase != action.PhasePostResult {
		return 0, fmt.Errorf("action case kind does not match its request phase")
	}
	if err := validateActionAssertion(scenario.Request.Phase, scenario.ToolID, scenario.Expected); err != nil {
		return 0, fmt.Errorf("expected outcome: %w", err)
	}
	if err := validateActionState(scenario.State, scenario.Request.Phase); err != nil {
		return 0, err
	}
	if scenario.Expected.Approval == nil &&
		(scenario.Expected.Decision == action.DecisionRequireApproval ||
			scenario.State.Approval.Status != action.ApprovalNone ||
			scenario.State.ApprovalTransition != "") {
		return 0, fmt.Errorf("approval-relevant action case requires an exact approval assertion")
	}
	if scenario.Expected.Approval != nil &&
		(scenario.Expected.Approval.Status != scenario.State.Approval.Status ||
			scenario.Expected.Approval.Identity != scenario.State.Approval.Identity ||
			scenario.Expected.Approval.Transition != scenario.State.ApprovalTransition) {
		return 0, fmt.Errorf("approval assertion does not match evaluator-visible state")
	}
	if scenario.State.Budget.StateVersion != scenario.Request.StateVersion {
		return 0, fmt.Errorf("action budget state version does not match the request")
	}
	if err := validateActionRequestFixture(scanner, scenario.Request, scenario.Expected); err != nil {
		return 0, err
	}
	for index, selected := range scenario.SelectedValues {
		if err := validateActionValueSummary(selected); err != nil {
			return 0, fmt.Errorf("selected_values[%d]: %w", index, err)
		}
		if index > 0 && actionValueSummaryKey(scenario.SelectedValues[index-1]) >= actionValueSummaryKey(selected) {
			return 0, fmt.Errorf("selected-value summaries must be unique and sorted")
		}
	}
	items := len(scenario.Request.Context) + len(scenario.State.CredentialLabels) +
		len(scenario.Expected.MatchedRuleIDs) + len(scenario.Expected.Completeness.Missing) +
		len(scenario.SelectedValues)
	if scenario.Expected.Ledger != nil {
		items += len(scenario.Expected.Ledger.SelectedFields) + 1
	}
	if scenario.State.Inspection != nil {
		items += len(scenario.State.Inspection.RuleIDs) + len(scenario.State.Inspection.Categories) +
			len(scenario.State.Inspection.PackIdentities) + len(scenario.State.Inspection.Fields) +
			len(scenario.State.Inspection.UnsupportedContent) + 1
	}
	if scenario.State.RepositoryEffect != nil {
		items += len(scenario.State.RepositoryEffect.RuleIDs) + 1
	}
	return items, nil
}

func validateActionRequestFixture(
	scanner *actioninspect.TextScanner,
	request ActionRequestFixture,
	expected ActionAssertion,
) error {
	oversized := len(request.Arguments)+len(request.Result)+len(request.Progress) > action.MaxArgumentBytes
	if oversized && expected.FailureCode != action.ReasonLimitExceeded {
		return fmt.Errorf("action request phase values exceed %d bytes", action.MaxArgumentBytes)
	}
	valueBytes := len(request.Arguments) + len(request.Result) + len(request.Progress)
	if len(request.Context) > action.MaxContextValues && expected.FailureCode != action.ReasonLimitExceeded {
		return fmt.Errorf("action context exceeds %d values", action.MaxContextValues)
	}
	for index, entry := range request.Context {
		if len(entry.Value) > action.MaxArgumentBytes-valueBytes && expected.FailureCode != action.ReasonLimitExceeded {
			return fmt.Errorf("action request values exceed %d bytes", action.MaxArgumentBytes)
		}
		valueBytes += len(entry.Value)
		if entry.Name == "" || len(entry.Name) > action.MaxPointerBytes || !utf8.ValidString(entry.Name) ||
			containsUnsafeControl(entry.Name) || unsafeActionMetadata(entry.Name) || !entry.Provenance.Valid() {
			return fmt.Errorf("action context[%d] metadata is invalid", index)
		}
	}
	for _, value := range []string{request.ServerLabel, request.Tool, request.StateVersion} {
		if value != "" && unsafeActionMetadata(value) {
			return fmt.Errorf("action request metadata contains private content")
		}
	}
	if _, err := action.NormalizeCompleteness(request.Completeness); err != nil {
		return fmt.Errorf("action request completeness is invalid: %w", err)
	}
	if err := validateActionPrivateRequest(scanner, request); err != nil {
		return err
	}
	raw := actionRawRequest(request, samplePolicyDigest, sampleLockDigest)
	_, err := action.NormalizeRequest(raw)
	if err == nil {
		return nil
	}
	requestError, ok := err.(*action.RequestError)
	if !ok || expected.FailureCode == "" || expected.FailureCode != requestError.Code {
		return fmt.Errorf("action request normalization fails with %v but expectation does not bind that failure", err)
	}
	return nil
}

func validateActionState(state ActionStateFixture, phase action.Phase) error {
	if state.ResampleDrift == nil || len(state.CredentialLabels) > action.MaxCredentialLabels ||
		state.Budget.Candidates == nil || !validFixtureIdentity(state.ContextIdentity) ||
		!action.ValidSHA256Identity(state.ExecutableDigest) || !action.SafeLabel(state.Principal) ||
		!state.Approval.Status.Valid() || !validFixtureIdentity(state.Approval.Identity) ||
		!state.Taint.Status.Valid() || !validFixtureIdentity(state.Taint.Identity) ||
		!state.Lifecycle.Valid() || state.CachePolicyVersion != action.CacheIdentityVersion {
		return fmt.Errorf("action state identity is invalid")
	}
	if !validApprovalTransition(state.Approval.Status, state.ApprovalTransition) {
		return fmt.Errorf("action approval transition does not match its evaluator snapshot")
	}
	for _, value := range []string{
		state.ContextIdentity, state.ExecutableDigest, state.Principal, state.Approval.Identity,
		state.Taint.Identity,
	} {
		if unsafeActionMetadata(value) {
			return fmt.Errorf("action state metadata contains private content")
		}
	}
	if !validFixtureIdentity(state.Budget.StateVersion) ||
		!validFixtureIdentity(state.Budget.Identity) ||
		!validFixtureIdentity(state.Budget.ReservationIdentity) || !state.Budget.Complete {
		return fmt.Errorf("action budget state is invalid or incomplete")
	}
	for index, label := range state.CredentialLabels {
		if !action.SafeLabel(label) || unsafeActionMetadata(label) ||
			index > 0 && state.CredentialLabels[index-1] >= label {
			return fmt.Errorf("credential labels must be valid, unique, and sorted")
		}
	}
	for index, component := range state.ResampleDrift {
		if !component.Valid() || index > 0 && state.ResampleDrift[index-1] >= component {
			return fmt.Errorf("resampled identity drift must be valid, unique, and sorted")
		}
	}
	if err := validateFixtureInspection(state.Inspection, phase); err != nil {
		return err
	}
	if state.RepositoryEffect == nil {
		return nil
	}
	effect := state.RepositoryEffect
	if !effect.Decision.Valid() || !effect.Reason.Valid() || !validFixtureIdentity(effect.Identity) || effect.RuleIDs == nil {
		return fmt.Errorf("repository-effect candidate is invalid")
	}
	if unsafeActionMetadata(effect.Identity) {
		return fmt.Errorf("repository-effect metadata contains private content")
	}
	for index, id := range effect.RuleIDs {
		if !action.SafeLabel(id) || unsafeActionMetadata(id) ||
			index > 0 && effect.RuleIDs[index-1] >= id {
			return fmt.Errorf("repository-effect rule ids must be valid, unique, and sorted")
		}
	}
	return nil
}

func validateFixtureInspection(evidence *action.InspectionEvidence, phase action.Phase) error {
	if evidence == nil {
		return nil
	}
	if !evidence.Status.Valid() || !action.ValidKeyedIdentity(evidence.Identity) ||
		!evidence.SchemaStatus.Valid() || evidence.RuleIDs == nil || evidence.Categories == nil ||
		evidence.PackIdentities == nil || evidence.Fields == nil || evidence.UnsupportedContent == nil ||
		len(evidence.RuleIDs) > action.MaxDetectors || len(evidence.Categories) > action.MaxDetectorCategories ||
		len(evidence.PackIdentities) == 0 || len(evidence.PackIdentities) > action.MaxDetectors ||
		len(evidence.Fields) > action.MaxJSONItems || len(evidence.UnsupportedContent) > action.MaxJSONItems {
		return fmt.Errorf("action inspection evidence has invalid metadata or null collections")
	}
	if err := validateFixtureInspectionOutcome(evidence, phase); err != nil {
		return err
	}
	if err := validateFixtureInspectionLists(evidence); err != nil {
		return err
	}
	return validateFixtureInspectionFields(evidence, phase)
}

func validateFixtureInspectionOutcome(evidence *action.InspectionEvidence, phase action.Phase) error {
	if evidence.SchemaStatus == action.InspectionSchemaValid || evidence.SchemaStatus == action.InspectionSchemaInvalid {
		if !action.ValidSHA256Identity(evidence.SchemaIdentity) {
			return fmt.Errorf("action inspection schema identity is invalid")
		}
	} else if evidence.SchemaIdentity != "absent" {
		return fmt.Errorf("action inspection schema identity is unexpected")
	}
	if phase == action.PhasePostResult {
		if evidence.SchemaStatus == action.InspectionSchemaNotApplicable {
			return fmt.Errorf("post-result inspection schema status is not applicable")
		}
	} else if evidence.SchemaStatus != action.InspectionSchemaNotApplicable || len(evidence.UnsupportedContent) > 0 {
		return fmt.Errorf("non-result inspection contains result-only evidence")
	}
	switch evidence.Status {
	case action.InspectionClean:
		if evidence.Decision != "" || evidence.Reason != "" || len(evidence.RuleIDs) != 0 || len(evidence.Categories) != 0 {
			return fmt.Errorf("clean action inspection contains a match")
		}
	case action.InspectionMatched:
		if len(evidence.RuleIDs) == 0 || len(evidence.Categories) == 0 || !validFixtureInspectionMatch(evidence, phase) {
			return fmt.Errorf("matched action inspection has an invalid outcome")
		}
	case action.InspectionIncomplete:
		if evidence.Decision != action.DecisionBlock || len(evidence.RuleIDs) != 0 || len(evidence.Categories) != 0 ||
			!validFixtureInspectionFailure(evidence.Reason) {
			return fmt.Errorf("incomplete action inspection has an invalid outcome")
		}
		if evidence.Reason == action.ReasonUnsupportedContent &&
			(phase != action.PhasePostResult || len(evidence.UnsupportedContent) == 0) {
			return fmt.Errorf("unsupported action content has no content evidence")
		}
		if evidence.Reason == action.ReasonSchemaInvalid && phase != action.PhasePostResult {
			return fmt.Errorf("action inspection schema failure is outside post-result")
		}
	}
	return nil
}

func validFixtureInspectionMatch(evidence *action.InspectionEvidence, phase action.Phase) bool {
	switch phase {
	case action.PhasePreCall:
		return evidence.Reason == action.ReasonRuleMatched &&
			(evidence.Decision == action.DecisionWarn || evidence.Decision == action.DecisionRequireApproval ||
				evidence.Decision == action.DecisionBlock)
	case action.PhasePostResult, action.PhaseProgress:
		return evidence.Decision == action.DecisionWarn && evidence.Reason == action.ReasonRuleMatched ||
			evidence.Decision == action.DecisionBlock && evidence.Reason == action.ReasonResultWithheld
	default:
		return false
	}
}

func validFixtureInspectionFailure(reason action.ReasonCode) bool {
	switch reason {
	case action.ReasonInspectionIncomplete, action.ReasonUnsupportedContent, action.ReasonSchemaInvalid,
		action.ReasonLimitExceeded, action.ReasonInvalidUTF8, action.ReasonCancelled, action.ReasonDeadlineExceeded:
		return true
	default:
		return false
	}
}

func validateFixtureInspectionLists(evidence *action.InspectionEvidence) error {
	for index, value := range evidence.RuleIDs {
		if !action.SafeLabel(value) || index > 0 && evidence.RuleIDs[index-1] >= value {
			return fmt.Errorf("action inspection rule identities are invalid or unsorted")
		}
	}
	for index, value := range evidence.Categories {
		if !value.Valid() || index > 0 && evidence.Categories[index-1] >= value {
			return fmt.Errorf("action inspection categories are invalid or unsorted")
		}
	}
	for index, value := range evidence.PackIdentities {
		if !action.ValidSHA256Identity(value) || index > 0 && evidence.PackIdentities[index-1] >= value {
			return fmt.Errorf("action inspection pack identities are invalid or unsorted")
		}
	}
	return nil
}

func validateFixtureInspectionFields(evidence *action.InspectionEvidence, phase action.Phase) error {
	wantSource := action.SourceArguments
	if phase == action.PhasePostResult {
		wantSource = action.SourceResult
	} else if phase == action.PhaseProgress {
		wantSource = action.SourceProgress
	}
	var bytes uint64
	var items uint32
	for index, field := range evidence.Fields {
		if field.Source != wantSource || !action.ValidKeyedIdentity(field.PointerIdentity) ||
			!action.ValidKeyedIdentity(field.ValueIdentity) || field.ByteLength > action.MaxArgumentBytes ||
			field.ItemCount > action.MaxJSONItems ||
			index > 0 && evidence.Fields[index-1].PointerIdentity >= field.PointerIdentity {
			return fmt.Errorf("action inspection field evidence is invalid or unsorted")
		}
		if field.ByteLength > action.MaxArgumentBytes-bytes || field.ItemCount > action.MaxJSONItems-items {
			return fmt.Errorf("action inspection field totals exceed their boundary")
		}
		bytes += field.ByteLength
		items += field.ItemCount
	}
	if bytes != evidence.ScannedBytes || items != evidence.ScannedItems {
		return fmt.Errorf("action inspection field totals do not match")
	}
	for index, binary := range evidence.UnsupportedContent {
		if !binary.ContentType.Valid() || !action.ValidKeyedIdentity(binary.Identity) ||
			binary.ByteLength > action.MaxArgumentBytes || index > 0 &&
			(evidence.UnsupportedContent[index-1].ContentType > binary.ContentType ||
				evidence.UnsupportedContent[index-1].ContentType == binary.ContentType &&
					evidence.UnsupportedContent[index-1].Identity >= binary.Identity) {
			return fmt.Errorf("action inspection content evidence is invalid or unsorted")
		}
	}
	return nil
}

func validateActionAssertion(phase action.Phase, toolID string, assertion ActionAssertion) error {
	if !assertion.Decision.Valid() || !assertion.Reason.Valid() || !assertion.Cache.Reason.Valid() ||
		!assertion.PhaseOutcome.Valid() || assertion.ToolID != "" && assertion.ToolID != toolID ||
		assertion.FailureCode != "" && (!assertion.FailureCode.Valid() || assertion.FailureCode != assertion.Reason) {
		return fmt.Errorf("decision, reason, tool, cache, phase, or failure assertion is invalid")
	}
	if assertion.Cache.Eligible != (assertion.Cache.Reason == action.CacheEligible) {
		return fmt.Errorf("cache eligibility and reason disagree")
	}
	if assertion.PhaseOutcome != action.OutcomeFor(phase, assertion.Decision) {
		return fmt.Errorf("phase outcome does not match phase and decision")
	}
	if err := validateActionApprovalAssertion(assertion); err != nil {
		return err
	}
	if err := validateActionLedgerAssertion(phase, assertion.Ledger); err != nil {
		return err
	}
	canonical, err := action.NormalizeCompleteness(assertion.Completeness)
	if err != nil || !equalActionCompleteness(canonical, assertion.Completeness) {
		return fmt.Errorf("completeness assertion is invalid or non-canonical")
	}
	seen := make(map[string]struct{}, len(assertion.MatchedRuleIDs))
	for _, id := range assertion.MatchedRuleIDs {
		if !action.SafeLabel(id) || unsafeActionMetadata(id) {
			return fmt.Errorf("matched rule id %q is invalid", id)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("matched rule id %q is duplicated", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validateActionApprovalAssertion(assertion ActionAssertion) error {
	if assertion.Approval == nil {
		return nil
	}
	approval := assertion.Approval
	if !approval.Status.Valid() || !validFixtureIdentity(approval.Identity) ||
		unsafeActionMetadata(approval.Identity) ||
		!validApprovalTransition(approval.Status, approval.Transition) {
		return fmt.Errorf("approval assertion state is invalid")
	}
	if assertion.Decision == action.DecisionRequireApproval {
		if !action.ValidSHA256Identity(approval.RequiredApprovalIdentity) {
			return fmt.Errorf("approval assertion requirement identity is invalid")
		}
		return nil
	}
	if approval.RequiredApprovalIdentity != "" {
		return fmt.Errorf("non-approval assertion contains a requirement identity")
	}
	return nil
}

func validApprovalTransition(snapshot action.ApprovalStatus, transition actionapproval.Status) bool {
	if transition == "" {
		return snapshot == action.ApprovalNone
	}
	if !transition.Valid() {
		return false
	}
	switch snapshot {
	case action.ApprovalPending:
		return transition == actionapproval.StatusPending
	case action.ApprovalCurrentUnconsumed, action.ApprovalConsumed:
		return transition == actionapproval.StatusApproved
	case action.ApprovalNone:
		return transition != actionapproval.StatusPending && transition != actionapproval.StatusApproved
	default:
		return false
	}
}

func validateActionValueSummary(summary ActionValueSummary) error {
	if !summary.Source.Valid() || summary.Category == "" ||
		len(summary.Category) > action.MaxSafeLabelBytes || !safeSummaryCategory(summary.Category) ||
		summary.ByteLength < 0 || summary.ItemCount < 0 || !summary.Provenance.Valid() ||
		summary.Identity != "" && !action.ValidIdentity(summary.Identity) {
		return fmt.Errorf("selected-value summary is invalid")
	}
	tokens, err := action.CompilePointer(summary.Pointer)
	if err != nil {
		return fmt.Errorf("selected-value pointer is invalid: %w", err)
	}
	for _, token := range tokens {
		if unsafeActionMetadata(token) {
			return fmt.Errorf("selected-value pointer contains private metadata")
		}
	}
	return nil
}

func actionRawRequest(request ActionRequestFixture, policyDigest, lockDigest string) action.RawRequest {
	return action.RawRequest{
		FormatVersion: request.FormatVersion, CallID: request.CallID,
		Transport: request.Transport, Platform: request.Platform,
		ServerLabel: request.ServerLabel, ServerFingerprint: request.ServerFingerprint,
		Tool: request.Tool, ToolContractDigest: request.ToolContractDigest,
		Phase: request.Phase, RepositoryIdentity: request.RepositoryIdentity,
		PolicyDigest: policyDigest, LockDigest: lockDigest, AuthorityMode: request.AuthorityMode,
		Arguments: json.RawMessage(request.Arguments), Result: json.RawMessage(request.Result), Progress: json.RawMessage(request.Progress),
		Context: cloneRawContext(request.Context), Completeness: request.Completeness,
		Deadline: request.Deadline, StateVersion: request.StateVersion,
	}
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func cloneRawContext(values []action.RawContextValue) []action.RawContextValue {
	out := make([]action.RawContextValue, len(values))
	for index, value := range values {
		out[index] = value
		out[index].Value = cloneRaw(value.Value)
	}
	return out
}

func validFixtureIdentity(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' || bytes.ContainsRune([]byte("._:-"), character) {
			continue
		}
		return false
	}
	return true
}

func containsUnsafeControl(value string) bool {
	return bytes.IndexFunc([]byte(value), func(character rune) bool { return unicode.IsControl(character) }) >= 0
}

func safeSummaryCategory(value string) bool {
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func actionValueSummaryKey(summary ActionValueSummary) string {
	return string(summary.Source) + "\x00" + summary.Pointer + "\x00" + summary.Category
}

func equalActionCompleteness(left, right action.Completeness) bool {
	if left.RequestComplete != right.RequestComplete || left.PolicyComplete != right.PolicyComplete ||
		left.IdentityComplete != right.IdentityComplete || left.ContextComplete != right.ContextComplete ||
		left.StateComplete != right.StateComplete || left.PhaseComplete != right.PhaseComplete ||
		len(left.Missing) != len(right.Missing) {
		return false
	}
	for index := range left.Missing {
		if left.Missing[index] != right.Missing[index] {
			return false
		}
	}
	return true
}

const (
	samplePolicyDigest = "1111111111111111111111111111111111111111111111111111111111111111"
	sampleLockDigest   = "2222222222222222222222222222222222222222222222222222222222222222"
)
