// Package actionledgerexport converts verified ledger lifecycles into
// minimized, privacy-bounded Impact Lab cases without creating a dependency
// cycle between the live ledger and the policy runtime.
package actionledgerexport

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/impactlab"
	"reconc.dev/reconc/internal/runtime"
)

const (
	FormatVersion = "reconc.action-ledger-impact-export/v1"
	Derivation    = "synthetic_minimized_verified"
)

type OmissionReason string

const (
	OmitHistoryIncomplete       OmissionReason = "retained_history_incomplete"
	OmitLifecycleIncomplete     OmissionReason = "lifecycle_incomplete"
	OmitEvidenceIncomplete      OmissionReason = "evidence_incomplete"
	OmitSelectedFields          OmissionReason = "selected_fields_not_reconstructable"
	OmitPolicyChanged           OmissionReason = "policy_or_lock_changed"
	OmitToolIdentityUnavailable OmissionReason = "tool_identity_not_reconstructable"
	OmitSyntheticMismatch       OmissionReason = "minimized_case_does_not_reproduce_decision"
	OmitInvalidCase             OmissionReason = "minimized_case_invalid"
)

type Omission struct {
	CallID string         `json:"call_id"`
	Reason OmissionReason `json:"reason"`
}

type Report struct {
	FormatVersion             string                          `json:"format_version"`
	Derivation                string                          `json:"derivation"`
	Verification              actionledger.VerificationReport `json:"verification"`
	SelectedCalls             uint64                          `json:"selected_calls"`
	ExportedCases             uint64                          `json:"exported_cases"`
	OmittedCalls              []Omission                      `json:"omitted_calls"`
	LifecycleCoverageComplete bool                            `json:"lifecycle_coverage_complete"`
	ReplayComplete            bool                            `json:"replay_complete"`
	MissingDimensions         []string                        `json:"missing_dimensions"`
	Corpus                    *impactlab.Corpus               `json:"corpus,omitempty"`
}

func Build(
	ctx context.Context,
	store *actionledger.Store,
	repository string,
	filter actionledger.Filter,
	compiled runtime.CompiledActionRuntime,
) (Report, error) {
	report := emptyReport()
	if ctx == nil || store == nil || compiled.Evaluator == nil {
		return report, fmt.Errorf("ledger export requires a store, context, and compiled action runtime")
	}
	if err := filter.Validate(); err != nil {
		return report, err
	}
	records, verification, err := store.Snapshot(ctx)
	report.Verification = verification
	if err != nil {
		return report, err
	}
	statuses, err := actionledger.BuildCallStatuses(records)
	if err != nil {
		return report, err
	}
	selected := selectedCallIDs(records, filter)
	byCall := recordsByCall(records)
	plan := compiled.Evaluator.Plan()
	cases := make([]impactlab.Case, 0, len(selected))
	for _, status := range statuses {
		if _, include := selected[status.CallID]; !include {
			continue
		}
		report.SelectedCalls++
		replayCase, reason, err := buildCase(byCall[status.CallID], status, plan, compiled)
		if err != nil {
			return report, err
		}
		if reason != "" {
			report.OmittedCalls = append(report.OmittedCalls, Omission{CallID: status.CallID, Reason: reason})
			continue
		}
		cases = append(cases, replayCase)
	}
	if len(cases) > 0 {
		corpus, err := impactlab.NewCorpus(repository, cases, []impactlab.EventClass{})
		if err != nil {
			return report, fmt.Errorf("build Impact Lab corpus from ledger: %w", err)
		}
		report.Corpus = &corpus
		report.ExportedCases = uint64(len(corpus.Cases))
	}
	report.LifecycleCoverageComplete = report.SelectedCalls > 0 && len(report.OmittedCalls) == 0 &&
		!verification.DroppedHistory && verification.EventsEvaluated && verification.EventsComplete &&
		verification.CallsEvaluated && verification.CallsComplete
	return report, nil
}

func emptyReport() Report {
	return Report{
		FormatVersion:  FormatVersion,
		Derivation:     Derivation,
		Verification:   actionledger.EmptyVerificationReport(),
		OmittedCalls:   []Omission{},
		ReplayComplete: false,
		MissingDimensions: []string{
			"original_arguments", "original_context_values", "original_evaluator_state",
		},
	}
}

// EmptyReport returns the canonical export result for a repository with no
// initialized action ledger. It performs no filesystem mutation.
func EmptyReport() Report {
	return emptyReport()
}

func selectedCallIDs(records []actionledger.Record, filter actionledger.Filter) map[string]struct{} {
	selected := make(map[string]struct{})
	for _, record := range records {
		if filter.Matches(record) {
			selected[record.Call.CallID] = struct{}{}
		}
	}
	return selected
}

func recordsByCall(records []actionledger.Record) map[string][]actionledger.Record {
	out := make(map[string][]actionledger.Record)
	for _, record := range records {
		out[record.Call.CallID] = append(out[record.Call.CallID], record)
	}
	return out
}

func buildCase(
	records []actionledger.Record,
	status actionledger.CallStatus,
	plan action.Plan,
	compiled runtime.CompiledActionRuntime,
) (impactlab.Case, OmissionReason, error) {
	if !status.HistoryComplete {
		return impactlab.Case{}, OmitHistoryIncomplete, nil
	}
	if !status.TerminalComplete {
		return impactlab.Case{}, OmitLifecycleIncomplete, nil
	}
	if !status.EvidenceComplete {
		return impactlab.Case{}, OmitEvidenceIncomplete, nil
	}
	preDecision, selected := preDecisionAndSelectedEvidence(records)
	if preDecision == nil {
		return impactlab.Case{}, OmitLifecycleIncomplete, nil
	}
	if selected {
		return impactlab.Case{}, OmitSelectedFields, nil
	}
	if preDecision.Call.PolicyDigest != compiled.SourceDigest || preDecision.Call.LockDigest != compiled.LockDigest {
		return impactlab.Case{}, OmitPolicyChanged, nil
	}
	tool, ok := resolveTool(preDecision.Call, plan.Tools)
	if !ok {
		return impactlab.Case{}, OmitToolIdentityUnavailable, nil
	}
	replayCase, err := minimizedCase(*preDecision, tool, plan.Ledger)
	if err != nil {
		return impactlab.Case{}, OmitInvalidCase, nil
	}
	bound, err := impactlab.BindCapturedActionExpectation(replayCase, compiled)
	if err != nil {
		return impactlab.Case{}, OmitInvalidCase, nil
	}
	if !matchesRetainedDecision(bound, *preDecision, tool.ID) {
		return impactlab.Case{}, OmitSyntheticMismatch, nil
	}
	return bound, "", nil
}

func preDecisionAndSelectedEvidence(records []actionledger.Record) (*actionledger.Record, bool) {
	var preDecision *actionledger.Record
	selected := false
	for index := range records {
		record := &records[index]
		if len(record.SelectedFields) > 0 {
			selected = true
		}
		if record.Event == actionledger.EventPreDecision && record.Decision.Phase == action.PhasePreCall {
			if preDecision != nil {
				return nil, selected
			}
			preDecision = record
		}
	}
	return preDecision, selected
}

func resolveTool(binding actionledger.CallBinding, tools []action.Tool) (action.Tool, bool) {
	if binding.Tool.Mode != action.LedgerExactName {
		return action.Tool{}, false
	}
	var matched *action.Tool
	for index := range tools {
		tool := &tools[index]
		if !tool.LedgerNameSafe || tool.Tool != binding.Tool.Value || tool.Transport != action.TransportMCPStdio ||
			tool.ServerLabel != "" && tool.ServerLabel != binding.ServerLabel ||
			tool.ServerFingerprint != "" && tool.ServerFingerprint != binding.ServerFingerprint {
			continue
		}
		if matched != nil {
			return action.Tool{}, false
		}
		copy := *tool
		matched = &copy
	}
	if matched == nil {
		return action.Tool{}, false
	}
	return *matched, true
}

func minimizedCase(
	record actionledger.Record,
	tool action.Tool,
	ledgerPolicy *action.LedgerPolicy,
) (impactlab.Case, error) {
	ledger, err := impactlab.LedgerAssertionForPhase(action.PhasePreCall, ledgerPolicy)
	if err != nil {
		return impactlab.Case{}, err
	}
	stateVersion := "state-ledger-export"
	expected := impactlab.ActionAssertion{
		Decision: record.Decision.Decision, Reason: record.Decision.Reason,
		ToolID: tool.ID, MatchedRuleIDs: append([]string{}, record.Decision.RuleIDs...),
		Completeness: record.Decision.Completeness, PhaseOutcome: record.PreDecision.Outcome,
		Ledger: ledger,
	}
	if expected.Decision == action.DecisionRequireApproval {
		expected.Approval = &impactlab.ActionApprovalAssertion{}
	}
	return impactlab.Case{
		ID: "ledger-" + strings.TrimPrefix(record.Call.CallID, "act_"), Kind: impactlab.CaseActionPre,
		Action: &impactlab.ActionCase{
			ToolID: tool.ID,
			Request: impactlab.ActionRequestFixture{
				FormatVersion: action.RequestFormatVersion, CallID: record.Call.CallID,
				Transport: tool.Transport, Platform: tool.Platform, ServerLabel: record.Call.ServerLabel,
				ServerFingerprint: record.Call.ServerFingerprint, Tool: tool.Tool,
				ToolContractDigest: record.Call.ToolContractDigest, Phase: action.PhasePreCall,
				RepositoryIdentity: record.Call.RepositoryIdentity,
				AuthorityMode:      action.AuthorityOperatorPinned, Arguments: impactlab.ActionPayload(`{}`),
				Context: []action.RawContextValue{}, Completeness: record.Decision.Completeness,
				Deadline: action.DeadlineReady, StateVersion: stateVersion,
			},
			State: impactlab.ActionStateFixture{
				ContextIdentity:  record.Call.ContextIdentity,
				ExecutableDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
				Principal:        record.Call.Principal,
				CredentialLabels: append([]string{}, record.Call.CredentialLabels...),
				Approval:         action.ApprovalSnapshot{Status: action.ApprovalNone, Identity: "approval-none"},
				Taint:            action.TaintSnapshot{Status: action.TaintClean, Identity: "taint-clean"},
				Lifecycle:        action.LifecycleActive, CachePolicyVersion: action.CacheIdentityVersion,
				Budget: action.BudgetSnapshot{
					StateVersion: stateVersion, Identity: "absent", ReservationIdentity: "absent",
					Complete: true, Candidates: []action.BudgetCandidate{},
				},
				ResampleDrift: []impactlab.ActionIdentityComponent{},
			},
			Expected: expected, SelectedValues: []impactlab.ActionValueSummary{},
		},
	}, nil
}

func matchesRetainedDecision(
	replayCase impactlab.Case,
	record actionledger.Record,
	toolID string,
) bool {
	if replayCase.Action == nil {
		return false
	}
	actual := replayCase.Action.Expected
	return actual.Decision == record.Decision.Decision && actual.Reason == record.Decision.Reason &&
		actual.ToolID == toolID && slices.Equal(actual.MatchedRuleIDs, record.Decision.RuleIDs) &&
		reflect.DeepEqual(actual.Completeness, record.Decision.Completeness) &&
		actual.PhaseOutcome == record.PreDecision.Outcome
}

func Marshal(report Report) ([]byte, error) {
	if err := validateReport(report); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func validateReport(report Report) error {
	missing := []string{"original_arguments", "original_context_values", "original_evaluator_state"}
	if report.FormatVersion != FormatVersion || report.Derivation != Derivation ||
		report.OmittedCalls == nil || !slices.Equal(report.MissingDimensions, missing) || report.ReplayComplete ||
		report.SelectedCalls != report.ExportedCases+uint64(len(report.OmittedCalls)) ||
		!validVerification(report.Verification) {
		return fmt.Errorf("action ledger export report is inconsistent")
	}
	selectedCallIDs := make(map[string]struct{})
	for index, omission := range report.OmittedCalls {
		if err := (actionledger.Filter{CallID: omission.CallID}).Validate(); err != nil || !omission.Reason.Valid() ||
			index > 0 && report.OmittedCalls[index-1].CallID >= omission.CallID {
			return fmt.Errorf("action ledger export omission is invalid or non-canonical")
		}
		selectedCallIDs[omission.CallID] = struct{}{}
	}
	wantCoverage := report.SelectedCalls > 0 && len(report.OmittedCalls) == 0 &&
		!report.Verification.DroppedHistory && report.Verification.EventsEvaluated &&
		report.Verification.EventsComplete && report.Verification.CallsEvaluated &&
		report.Verification.CallsComplete
	if report.LifecycleCoverageComplete != wantCoverage {
		return fmt.Errorf("action ledger export lifecycle coverage is inconsistent")
	}
	if report.Corpus == nil {
		if report.ExportedCases != 0 {
			return fmt.Errorf("action ledger export corpus count is inconsistent")
		}
		return nil
	}
	if uint64(len(report.Corpus.Cases)) != report.ExportedCases || report.Corpus.Completeness.CompleteReplay {
		return fmt.Errorf("action ledger export corpus count or completeness is inconsistent")
	}
	for _, replayCase := range report.Corpus.Cases {
		if replayCase.Action == nil || replayCase.Action.Expected.Ledger == nil ||
			len(replayCase.Action.SelectedValues) != 0 || replayCase.Action.RedactionCount != 0 {
			return fmt.Errorf("action ledger export case is not a minimized ledger-bound action case")
		}
		callID, err := validateMinimizedCase(replayCase)
		if err != nil {
			return err
		}
		if _, duplicate := selectedCallIDs[callID]; duplicate {
			return fmt.Errorf("action ledger export selects call %s more than once", callID)
		}
		selectedCallIDs[callID] = struct{}{}
	}
	if uint64(len(selectedCallIDs)) != report.SelectedCalls {
		return fmt.Errorf("action ledger export selected-call identities are inconsistent")
	}
	if _, err := impactlab.MarshalCorpus(*report.Corpus); err != nil {
		return fmt.Errorf("action ledger export corpus is invalid: %w", err)
	}
	return nil
}

func validateMinimizedCase(replayCase impactlab.Case) (string, error) {
	if !strings.HasPrefix(replayCase.ID, "ledger-") || replayCase.Kind != impactlab.CaseActionPre ||
		replayCase.Repository != nil || replayCase.Action == nil {
		return "", fmt.Errorf("action ledger export case identity or kind is invalid")
	}
	callID := "act_" + strings.TrimPrefix(replayCase.ID, "ledger-")
	if err := (actionledger.Filter{CallID: callID}).Validate(); err != nil {
		return "", fmt.Errorf("action ledger export case identity is invalid")
	}
	scenario := replayCase.Action
	request := scenario.Request
	state := scenario.State
	ledger := scenario.Expected.Ledger
	if request.Arguments != impactlab.ActionPayload(`{}`) || request.Result != "" || request.Progress != "" ||
		len(request.Context) != 0 || request.CallID != callID || request.Phase != action.PhasePreCall ||
		request.AuthorityMode != action.AuthorityOperatorPinned || request.Deadline != action.DeadlineReady ||
		state.Approval.Status != action.ApprovalNone || state.Approval.Identity != "approval-none" ||
		state.ApprovalTransition != "" || state.Taint.Status != action.TaintClean ||
		state.Taint.Identity != "taint-clean" || state.RepositoryEffect != nil ||
		state.Lifecycle != action.LifecycleActive || state.CachePolicyVersion != action.CacheIdentityVersion ||
		state.Budget.Identity != "absent" || state.Budget.ReservationIdentity != "absent" ||
		!state.Budget.Complete || len(state.Budget.Candidates) != 0 || state.Inspection != nil ||
		len(state.ResampleDrift) != 0 || ledger.ToolIdentity != action.LedgerExactName ||
		len(ledger.SelectedFields) != 0 {
		return "", fmt.Errorf("action ledger export case contains non-minimized or undisclosed evidence")
	}
	return callID, nil
}

func validVerification(report actionledger.VerificationReport) bool {
	if report.FormatVersion != actionledger.FormatVersion {
		return false
	}
	if report.Integrity == actionledger.StatusEmpty {
		return reflect.DeepEqual(report, actionledger.EmptyVerificationReport())
	}
	if report.Integrity != actionledger.StatusVerified ||
		report.ArchiveContinuity != actionledger.StatusVerified ||
		report.DetachedHead != actionledger.HeadMatched || report.RecordCount == 0 ||
		report.ArchiveCount > actionledger.MaxArchives || report.FirstRecordedSequence != 1 ||
		report.FirstRetainedSequence == 0 || report.LastRetainedSequence < report.FirstRetainedSequence ||
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

func (r OmissionReason) Valid() bool {
	switch r {
	case OmitHistoryIncomplete, OmitLifecycleIncomplete, OmitEvidenceIncomplete,
		OmitSelectedFields, OmitPolicyChanged, OmitToolIdentityUnavailable,
		OmitSyntheticMismatch, OmitInvalidCase:
		return true
	default:
		return false
	}
}
