package impactlab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode"
	"unicode/utf8"

	"reconc.dev/reconc/internal/action"
)

func validateActionCase(kind CaseKind, scenario ActionCase) (int, error) {
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
	if err := validateActionState(scenario.State); err != nil {
		return 0, err
	}
	if err := validateActionRequestFixture(scenario.Request, scenario.Expected); err != nil {
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
	if scenario.State.RepositoryEffect != nil {
		items += len(scenario.State.RepositoryEffect.RuleIDs) + 1
	}
	return items, nil
}

func validateActionRequestFixture(request ActionRequestFixture, expected ActionAssertion) error {
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
	if err := validateActionPrivateRequest(request); err != nil {
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

func validateActionState(state ActionStateFixture) error {
	if state.ResampleDrift == nil || len(state.CredentialLabels) > action.MaxCredentialLabels ||
		!validFixtureIdentity(state.ContextIdentity) || !action.SafeLabel(state.Principal) ||
		!state.Approval.Status.Valid() || !validFixtureIdentity(state.Approval.Identity) ||
		!state.Taint.Status.Valid() || !validFixtureIdentity(state.Taint.Identity) ||
		!state.Lifecycle.Valid() || state.CachePolicyVersion != action.CacheIdentityVersion {
		return fmt.Errorf("action state identity is invalid")
	}
	for _, value := range []string{
		state.ContextIdentity, state.Principal, state.Approval.Identity,
		state.Taint.Identity,
	} {
		if unsafeActionMetadata(value) {
			return fmt.Errorf("action state metadata contains private content")
		}
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
