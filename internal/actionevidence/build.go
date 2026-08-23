package actionevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionstate"
)

func Build(input BuildInput) (Report, error) {
	if err := validateBuildInput(input); err != nil {
		return Report{}, err
	}
	packs, err := validatedPacks(input.Packs)
	if err != nil {
		return Report{}, err
	}
	window, selected, err := selectEvidenceWindow(input)
	if err != nil {
		return Report{}, err
	}
	facts, err := buildFacts(input, window, selected)
	if err != nil {
		return Report{}, err
	}
	controls := buildControls(input.AsOf, packs, facts)
	report := newReport(input, packs, window, selected, facts, controls)
	report.Identity, err = reportIdentity(report)
	if err != nil {
		return Report{}, err
	}
	// MarshalJSON independently revalidates the report identity before emitting
	// its distinct indented wire representation. Reusing the earlier compact
	// identity payload here would collapse that final validation boundary.
	if _, err := MarshalJSON(report); err != nil {
		return Report{}, err
	}
	return report, nil
}

func validateBuildInput(input BuildInput) error {
	if input.AsOf.IsZero() || input.AsOf.Location() != time.UTC || input.Since.IsZero() || input.Since.Location() != time.UTC ||
		input.Until.Location() != time.UTC ||
		input.Until.IsZero() || !input.Since.Before(input.Until) || input.Until.After(input.AsOf) {
		return fmt.Errorf("evidence as-of, since, and until must be canonical UTC times with since < until <= as-of")
	}
	if input.RepositoryIdentity != "unavailable" && !action.ValidKeyedIdentity(input.RepositoryIdentity) ||
		!lowerHex(input.Policy.SourceDigest, 64) ||
		!lowerHex(input.Policy.LockDigest, 64) || !action.ValidSHA256Identity(input.Policy.PlanIdentity) {
		return fmt.Errorf("evidence repository or policy identity is invalid")
	}
	if input.Policy.ToolCount != len(input.Plan.Tools) || input.Policy.RuleCount != len(input.Plan.Rules) ||
		input.Policy.BudgetCount != len(input.Plan.Budgets) || input.Policy.ApprovalCount != len(input.Plan.Approvals) {
		return fmt.Errorf("evidence policy counts do not match the compiled action plan")
	}
	if !input.StateIntegrity.Valid() || (input.StateIntegrity == IntegrityVerified) != input.StatePresent {
		return fmt.Errorf("evidence state integrity and presence are inconsistent")
	}
	if err := validateReceiptEvidence(input); err != nil {
		return err
	}
	return validateScenarioEvidence(input.Scenarios)
}

func validateReceiptEvidence(input BuildInput) error {
	report := input.Receipts
	if !report.Evaluated || report.Records == nil || report.Applicable < 0 || report.Verified < 0 ||
		report.Unavailable < 0 || report.Invalid < 0 {
		return fmt.Errorf("approval receipt evidence is incomplete or invalid")
	}
	if !input.StatePresent {
		expectedComplete := input.StateIntegrity == IntegrityUnavailable
		if len(report.Records) != 0 || report.Applicable != 0 || report.Verified != 0 ||
			report.Unavailable != 0 || report.Invalid != 0 || report.Complete != expectedComplete {
			return fmt.Errorf("unverified action state contains approval receipt evidence")
		}
		return nil
	}
	if input.State.ApprovalRecords == nil || len(report.Records) != len(input.State.ApprovalRecords) {
		return fmt.Errorf("approval receipt evidence does not match the verified action state")
	}
	applicable, verified, unavailable, invalid := 0, 0, 0, 0
	for index, receipt := range report.Records {
		stateRecord := input.State.ApprovalRecords[index]
		if !actionapproval.ValidRequestID(receipt.RequestID) || !validActionCallID(receipt.CallID) ||
			receipt.RequestID != stateRecord.RequestID || receipt.CallID != stateRecord.CallID ||
			receipt.ApprovalStatus != stateRecord.Status || !receipt.ApprovalStatus.Valid() {
			return fmt.Errorf("approval receipt evidence record %d does not match the verified action state", index)
		}
		terminal := receipt.ApprovalStatus == actionapproval.StatusApproved ||
			receipt.ApprovalStatus == actionapproval.StatusRejected
		if !terminal {
			if receipt.Verification != actionstate.ApprovalReceiptNotApplicable ||
				receipt.RegistryIdentity != "" || receipt.AuthorityKeyID != "" ||
				receipt.ReceiptID != "" || receipt.ReceiptIdentity != "" {
				return fmt.Errorf("non-terminal approval receipt evidence record %d contains receipt material", index)
			}
			continue
		}
		if !action.ValidSHA256Identity(receipt.RegistryIdentity) || !action.SafeLabel(receipt.AuthorityKeyID) ||
			!actionapproval.ValidReceiptID(receipt.ReceiptID) || !action.ValidSHA256Identity(receipt.ReceiptIdentity) {
			return fmt.Errorf("approval receipt evidence record %d has invalid authenticated metadata", index)
		}
		applicable++
		switch receipt.Verification {
		case actionstate.ApprovalReceiptVerified:
			verified++
		case actionstate.ApprovalReceiptUnavailable:
			unavailable++
		case actionstate.ApprovalReceiptInvalid:
			invalid++
		default:
			return fmt.Errorf("approval receipt evidence record %d has an invalid verification status", index)
		}
	}
	complete := unavailable == 0 && invalid == 0
	if report.Applicable != applicable || report.Verified != verified || report.Unavailable != unavailable ||
		report.Invalid != invalid || report.Complete != complete {
		return fmt.Errorf("approval receipt evidence counters do not match exact records")
	}
	return nil
}

func validActionCallID(value string) bool {
	if len(value) != 30 || !strings.HasPrefix(value, "act_") {
		return false
	}
	for _, character := range value[4:] {
		if character < 'a' || character > 'z' {
			if character < '2' || character > '7' {
				return false
			}
		}
	}
	return true
}

func validateScenarioEvidence(evidence ScenarioEvidence) error {
	if evidence.CorpusIDs == nil || evidence.MissingDimensions == nil ||
		evidence.ObservedPlatforms == nil || evidence.MissingPlatforms == nil ||
		len(evidence.CorpusIDs) > MaxPacks || len(evidence.MissingDimensions) > MaxControls ||
		len(evidence.ObservedPlatforms) > MaxPacks || len(evidence.MissingPlatforms) > MaxPacks ||
		evidence.CaseCount < 0 || evidence.ActionCaseCount < 0 || evidence.ActionCaseCount > evidence.CaseCount {
		return fmt.Errorf("scenario evidence is incomplete or invalid")
	}
	if !sort.StringsAreSorted(evidence.CorpusIDs) || !sort.StringsAreSorted(evidence.MissingDimensions) ||
		!platformsSorted(evidence.ObservedPlatforms) || !platformsSorted(evidence.MissingPlatforms) {
		return fmt.Errorf("scenario evidence collections must be canonically sorted")
	}
	for index := 1; index < len(evidence.MissingDimensions); index++ {
		if evidence.MissingDimensions[index-1] == evidence.MissingDimensions[index] {
			return fmt.Errorf("scenario evidence missing dimensions must be unique")
		}
	}
	return nil
}

func validatedPacks(input []LoadedPack) ([]LoadedPack, error) {
	if len(input) == 0 || len(input) > MaxPacks {
		return nil, fmt.Errorf("evidence requires 1 to %d mapping packs", MaxPacks)
	}
	packs := append([]LoadedPack(nil), input...)
	sort.Slice(packs, func(i, j int) bool { return packs[i].Pack.PackID < packs[j].Pack.PackID })
	for index, loaded := range packs {
		identity, err := PackIdentity(loaded.Pack)
		if err != nil || identity != loaded.Identity || loaded.Provenance == "" ||
			index > 0 && packs[index-1].Pack.PackID == loaded.Pack.PackID {
			return nil, fmt.Errorf("mapping pack identity, provenance, or uniqueness is invalid")
		}
	}
	return packs, nil
}

func selectEvidenceWindow(input BuildInput) (WindowEvidence, []actionledger.Record, error) {
	window := WindowEvidence{Since: canonicalTime(input.Since), Until: canonicalTime(input.Until)}
	if len(input.Records) == 0 {
		window.Complete = input.LedgerVerification.Integrity != actionledger.StatusInvalid &&
			!input.LedgerVerification.DroppedHistory
		return window, []actionledger.Record{}, nil
	}
	first, err := time.Parse(time.RFC3339Nano, input.Records[0].Timestamp)
	if err != nil {
		return WindowEvidence{}, nil, fmt.Errorf("parse first retained ledger timestamp: %w", err)
	}
	last, err := time.Parse(time.RFC3339Nano, input.Records[len(input.Records)-1].Timestamp)
	if err != nil {
		return WindowEvidence{}, nil, fmt.Errorf("parse last retained ledger timestamp: %w", err)
	}
	if last.After(input.AsOf) {
		return WindowEvidence{}, nil, fmt.Errorf("evidence as-of precedes the latest retained ledger record")
	}
	window.FirstRetainedAt, window.LastRetainedAt = canonicalTime(first), canonicalTime(last)
	window.FirstRetainedSequence = input.Records[0].Sequence
	window.LastRetainedSequence = input.Records[len(input.Records)-1].Sequence
	window.DroppedHistory = input.LedgerVerification.DroppedHistory
	selected, calls, err := recordsForAcceptedCalls(input.Records, input.Since, input.Until)
	if err != nil {
		return WindowEvidence{}, nil, err
	}
	window.SelectedCalls, window.SelectedRecords = calls, len(selected)
	window.Complete = windowAvailable(input.LedgerVerification, first, input.Since)
	return window, selected, nil
}

func recordsForAcceptedCalls(records []actionledger.Record, since, until time.Time) ([]actionledger.Record, int, error) {
	selectedCalls := make(map[string]bool)
	for _, record := range records {
		if record.Event != actionledger.EventRequestAccepted {
			continue
		}
		timestamp, err := time.Parse(time.RFC3339Nano, record.Timestamp)
		if err != nil {
			return nil, 0, fmt.Errorf("parse ledger timestamp at sequence %d: %w", record.Sequence, err)
		}
		if !timestamp.Before(since) && timestamp.Before(until) {
			selectedCalls[record.Call.CallID] = true
		}
	}
	selected := make([]actionledger.Record, 0)
	for _, record := range records {
		if selectedCalls[record.Call.CallID] {
			selected = append(selected, record)
		}
	}
	return selected, len(selectedCalls), nil
}

func windowAvailable(report actionledger.VerificationReport, first, since time.Time) bool {
	if report.Integrity != actionledger.StatusVerified || report.ArchiveContinuity != actionledger.StatusVerified {
		return false
	}
	return !report.DroppedHistory || !first.After(since)
}

func buildFacts(input BuildInput, window WindowEvidence, selected []actionledger.Record) ([]Fact, error) {
	statuses, err := actionledger.BuildCallStatuses(selected)
	if err != nil {
		return nil, fmt.Errorf("verify selected evidence call lifecycles: %w", err)
	}
	facts := map[FactID]Fact{}
	setPolicyFacts(facts, input)
	setLedgerFacts(facts, input, window, selected, statuses)
	setApprovalFacts(facts, input, selected)
	setBudgetFacts(facts, input, selected)
	setScenarioFacts(facts, input)
	return canonicalFacts(facts), nil
}

func setPolicyFacts(facts map[FactID]Fact, input BuildInput) {
	if input.RepositoryIdentity == "unavailable" {
		facts[FactRepositoryIdentity] = missingFact(FactRepositoryIdentity, "Operator-owned repository identity is unavailable.")
	} else {
		facts[FactRepositoryIdentity] = coveredFact(FactRepositoryIdentity, "Operator-owned repository identity was verified.")
	}
	facts[FactPolicyLockCurrent] = coveredFact(FactPolicyLockCurrent, "Current source, lock, and action-plan identities were compiled from one validated lock snapshot.")
	facts[FactPolicyActionTools] = countFact(FactPolicyActionTools, input.Policy.ToolCount, "compiled action tools")
	facts[FactPolicyActionRules] = countFact(FactPolicyActionRules, input.Policy.RuleCount, "compiled action rules")
}

func setLedgerFacts(
	facts map[FactID]Fact,
	input BuildInput,
	window WindowEvidence,
	selected []actionledger.Record,
	statuses []actionledger.CallStatus,
) {
	report := input.LedgerVerification
	facts[FactLedgerIntegrity] = verificationFact(FactLedgerIntegrity, report.Integrity, "ledger retained-chain integrity")
	facts[FactLedgerArchiveContinuity] = verificationFact(FactLedgerArchiveContinuity, report.ArchiveContinuity, "ledger archive continuity")
	facts[FactLedgerWindowComplete] = booleanWindowFact(window, len(selected))
	facts[FactLedgerPolicyIdentity] = ledgerPolicyFact(input, selected)
	facts[FactLedgerEventsComplete] = selectedCompletenessFact(FactLedgerEventsComplete, selectedEventsComplete(selected), len(selected), "selected ledger events")
	facts[FactLedgerCallsComplete] = selectedCompletenessFact(FactLedgerCallsComplete, selectedCallsComplete(statuses), len(statuses), "selected action calls")
}

func setApprovalFacts(facts map[FactID]Fact, input BuildInput, selected []actionledger.Record) {
	if input.Policy.ApprovalCount == 0 {
		facts[FactApprovalReceipts] = notEvaluatedFact(FactApprovalReceipts, "The current action plan declares no approval disclosures.")
		facts[FactApprovalAuthority] = notEvaluatedFact(FactApprovalAuthority, "The current action plan declares no approval disclosures.")
		return
	}
	report := selectedReceiptReport(input.Receipts, selected)
	if report.Applicable == 0 {
		facts[FactApprovalReceipts] = notEvaluatedFact(FactApprovalReceipts, "No stored signed approval decision applies to the selected evidence.")
		facts[FactApprovalAuthority] = notEvaluatedFact(FactApprovalAuthority, "No stored signed approval decision applies to the selected evidence.")
		return
	}
	facts[FactApprovalReceipts] = receiptFact(FactApprovalReceipts, report)
	facts[FactApprovalAuthority] = receiptFact(FactApprovalAuthority, report)
}

func selectedReceiptReport(
	report actionstate.ApprovalReceiptVerificationReport,
	records []actionledger.Record,
) actionstate.ApprovalReceiptVerificationReport {
	calls := make(map[string]bool)
	for _, record := range records {
		calls[record.Call.CallID] = true
	}
	selected := actionstate.ApprovalReceiptVerificationReport{
		Evaluated: report.Evaluated, Complete: true,
		Records: []actionstate.ApprovalReceiptVerification{},
	}
	for _, record := range report.Records {
		if !calls[record.CallID] {
			continue
		}
		selected.Records = append(selected.Records, record)
		if record.Verification == actionstate.ApprovalReceiptNotApplicable {
			continue
		}
		selected.Applicable++
		switch record.Verification {
		case actionstate.ApprovalReceiptVerified:
			selected.Verified++
		case actionstate.ApprovalReceiptUnavailable:
			selected.Unavailable++
			selected.Complete = false
		case actionstate.ApprovalReceiptInvalid:
			selected.Invalid++
			selected.Complete = false
		}
	}
	return selected
}

func setBudgetFacts(facts map[FactID]Fact, input BuildInput, selected []actionledger.Record) {
	if input.Policy.BudgetCount == 0 {
		facts[FactBudgetState] = notEvaluatedFact(FactBudgetState, "The current action plan declares no budgets.")
		facts[FactBudgetIdentity] = notEvaluatedFact(FactBudgetIdentity, "The current action plan declares no budgets.")
		return
	}
	if input.StateIntegrity == IntegrityInvalid {
		facts[FactBudgetState] = missingFact(FactBudgetState, "Current budget state failed integrity verification.")
	} else if input.StateIntegrity == IntegrityUnavailable || !input.StatePresent || !input.State.Complete {
		facts[FactBudgetState] = missingFact(FactBudgetState, "Current verified budget state is unavailable.")
	} else if input.State.Indeterminate > 0 {
		facts[FactBudgetState] = partialFact(FactBudgetState, "Current state is verified but contains indeterminate reservations.")
	} else {
		facts[FactBudgetState] = coveredFact(FactBudgetState, "Current budget state identity and contents were verified.")
	}
	facts[FactBudgetIdentity] = budgetIdentityFact(input, selected)
}

func setScenarioFacts(facts map[FactID]Fact, input BuildInput) {
	scenarios := input.Scenarios
	if !scenarios.Evaluated {
		facts[FactScenarioResults] = notEvaluatedFact(FactScenarioResults, "No scenario corpus was evaluated.")
		facts[FactScenarioCompleteness] = notEvaluatedFact(FactScenarioCompleteness, "No scenario corpus was evaluated.")
		facts[FactHostCoverage] = notEvaluatedFact(FactHostCoverage, "No scenario corpus was evaluated.")
		return
	}
	facts[FactScenarioResults] = boolFact(FactScenarioResults, scenarios.ResultsCurrent, "Scenario expectations match the current production evaluator.")
	facts[FactScenarioCompleteness] = boolFact(FactScenarioCompleteness, scenarios.Complete, "Declared action scenario dimensions are complete.")
	if len(scenarios.MissingPlatforms) == 0 {
		facts[FactHostCoverage] = coveredFact(FactHostCoverage, "Every configured host platform appears in the evaluated action scenarios.")
	} else {
		facts[FactHostCoverage] = partialFact(FactHostCoverage, "Configured host platforms are absent from the evaluated action scenarios.")
	}
}

func canonicalFacts(values map[FactID]Fact) []Fact {
	out := make([]Fact, 0, len(allFactIDs))
	for _, id := range allFactIDs {
		out = append(out, values[id])
	}
	return out
}

func buildControls(asOf time.Time, packs []LoadedPack, facts []Fact) []ControlResult {
	byID := make(map[FactID]Fact, len(facts))
	for _, fact := range facts {
		byID[fact.ID] = fact
	}
	results := []ControlResult{}
	for _, loaded := range packs {
		for _, control := range loaded.Pack.Controls {
			result := controlResult(loaded.Pack, control, byID)
			if packReviewStale(asOf, loaded.Pack) {
				result = downgradeStaleMapping(result)
			}
			results = append(results, result)
		}
	}
	return results
}

func controlResult(pack Pack, control Control, facts map[FactID]Fact) ControlResult {
	statuses := make([]Status, len(control.EvidenceSelectors))
	gaps := []string{}
	for index, selector := range control.EvidenceSelectors {
		fact := facts[selector]
		statuses[index] = fact.Status
		if fact.Status != StatusCovered {
			gaps = append(gaps, string(selector)+":"+string(fact.Status))
		}
	}
	sort.Strings(gaps)
	return ControlResult{
		PackID: pack.PackID, Framework: pack.Framework, ControlID: control.ID,
		Reference: control.Reference, Status: combineStatuses(statuses),
		Rationale: control.Rationale, EvidenceSelectors: append([]FactID(nil), control.EvidenceSelectors...),
		KnownGaps: append([]string{}, control.KnownGaps...), EvidenceGaps: gaps,
	}
}

func combineStatuses(statuses []Status) Status {
	covered, weaker, missing := 0, 0, 0
	for _, status := range statuses {
		switch status {
		case StatusCovered:
			covered++
		case StatusPartial:
			covered++
			weaker++
		case StatusMissing:
			missing++
			weaker++
		case StatusNotEvaluated:
			weaker++
		}
	}
	if covered == len(statuses) && weaker == 0 {
		return StatusCovered
	}
	if covered > 0 {
		return StatusPartial
	}
	if missing > 0 {
		return StatusMissing
	}
	return StatusNotEvaluated
}

func packReviewStale(asOf time.Time, pack Pack) bool {
	reviewed, err := time.Parse("2006-01-02", pack.Source.ReviewedAt)
	return err != nil || pack.ReviewStatus != "reviewed" || reviewed.After(asOf) || asOf.Sub(reviewed) > 366*24*time.Hour
}

func downgradeStaleMapping(result ControlResult) ControlResult {
	if result.Status == StatusCovered {
		result.Status = StatusPartial
	}
	result.EvidenceGaps = append(result.EvidenceGaps, "mapping-review:stale")
	sort.Strings(result.EvidenceGaps)
	return result
}

func newReport(
	input BuildInput,
	packs []LoadedPack,
	window WindowEvidence,
	selected []actionledger.Record,
	facts []Fact,
	controls []ControlResult,
) Report {
	return Report{
		Schema: ReportSchema, FormatVersion: FormatVersion, AsOf: canonicalTime(input.AsOf),
		RepositoryIdentity: input.RepositoryIdentity, Policy: input.Policy, Window: window,
		Ledger: LedgerEvidence{
			Integrity:         input.LedgerVerification.Integrity,
			ArchiveContinuity: input.LedgerVerification.ArchiveContinuity,
			DetachedHead:      input.LedgerVerification.DetachedHead,
			RecordCount:       input.LedgerVerification.RecordCount, ArchiveCount: input.LedgerVerification.ArchiveCount,
			EventsComplete: input.LedgerVerification.EventsComplete, CallsComplete: input.LedgerVerification.CallsComplete,
		},
		State: stateEvidence(input, selected), Scenarios: input.Scenarios,
		MappingPacks: packSummaries(packs), Facts: facts, Controls: controls,
		OverallStatus: controlsStatus(controls), Disclaimer: Disclaimer,
	}
}

func stateEvidence(input BuildInput, selected []actionledger.Record) StateEvidence {
	receipts := selectedReceiptReport(input.Receipts, selected)
	return StateEvidence{
		Integrity: input.StateIntegrity, Present: input.StatePresent, StateVersion: input.State.StateVersion,
		Revision: input.State.Revision, KeyID: input.State.KeyID, BudgetCount: len(input.State.Budgets),
		LiveReservations: input.State.LiveReservations, Indeterminate: input.State.Indeterminate,
		ApprovalRecordCount: len(input.State.ApprovalRecords), PendingApprovals: input.State.PendingApprovals,
		ReceiptApplicable: receipts.Applicable, ReceiptVerified: receipts.Verified,
		ReceiptUnavailable: receipts.Unavailable, ReceiptInvalid: receipts.Invalid,
		Complete: input.StateIntegrity == IntegrityVerified && input.State.Complete && receipts.Complete,
	}
}

func packSummaries(packs []LoadedPack) []PackSummary {
	out := make([]PackSummary, len(packs))
	for index, loaded := range packs {
		pack := loaded.Pack
		out[index] = PackSummary{
			PackID: pack.PackID, PackVersion: pack.PackVersion, Framework: pack.Framework,
			Identity: loaded.Identity, Provenance: loaded.Provenance, ReviewStatus: pack.ReviewStatus,
			Edition: pack.Source.Edition, SourceDate: pack.Source.SourceDate,
			ReviewedAt: pack.Source.ReviewedAt, SourceURL: pack.Source.URL,
		}
	}
	return out
}

func controlsStatus(controls []ControlResult) Status {
	statuses := make([]Status, len(controls))
	for index, control := range controls {
		statuses[index] = control.Status
	}
	return combineStatuses(statuses)
}

func reportIdentity(report Report) (string, error) {
	report.Identity = ""
	body, err := json.Marshal(report)
	if err != nil {
		return "", fmt.Errorf("encode action evidence identity: %w", err)
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func coveredFact(id FactID, basis string) Fact {
	return Fact{ID: id, Status: StatusCovered, Basis: []string{basis}, Gaps: []string{}}
}

func partialFact(id FactID, gap string) Fact {
	return Fact{ID: id, Status: StatusPartial, Basis: []string{}, Gaps: []string{gap}}
}

func missingFact(id FactID, gap string) Fact {
	return Fact{ID: id, Status: StatusMissing, Basis: []string{}, Gaps: []string{gap}}
}

func notEvaluatedFact(id FactID, gap string) Fact {
	return Fact{ID: id, Status: StatusNotEvaluated, Basis: []string{}, Gaps: []string{gap}}
}

func countFact(id FactID, count int, label string) Fact {
	if count == 0 {
		return missingFact(id, "No "+label+" are present in the current policy.")
	}
	return coveredFact(id, fmt.Sprintf("Current policy contains %d %s.", count, label))
}

func verificationFact(id FactID, status actionledger.VerificationStatus, label string) Fact {
	if status == actionledger.StatusVerified {
		return coveredFact(id, label+" is verified.")
	}
	if status == actionledger.StatusEmpty {
		return missingFact(id, label+" has no retained records.")
	}
	return missingFact(id, label+" failed verification.")
}

func booleanWindowFact(window WindowEvidence, selected int) Fact {
	if !window.Complete {
		return missingFact(FactLedgerWindowComplete, "The requested evidence window predates retained history or ledger verification failed.")
	}
	if selected == 0 {
		return notEvaluatedFact(FactLedgerWindowComplete, "The requested evidence window contains no accepted action call.")
	}
	return coveredFact(FactLedgerWindowComplete, "The requested window is within verified retained history.")
}

func ledgerPolicyFact(input BuildInput, selected []actionledger.Record) Fact {
	if len(selected) == 0 {
		return notEvaluatedFact(FactLedgerPolicyIdentity, "No selected ledger record is available for current-policy comparison.")
	}
	for _, record := range selected {
		if record.Call.PolicyDigest != input.Policy.SourceDigest || record.Call.LockDigest != input.Policy.LockDigest {
			return missingFact(FactLedgerPolicyIdentity, "A selected ledger record uses a different source or lock identity.")
		}
	}
	return coveredFact(FactLedgerPolicyIdentity, "Every selected ledger record matches the current source and lock identities.")
}

func selectedEventsComplete(records []actionledger.Record) bool {
	for _, record := range records {
		if !record.Decision.Completeness.Complete() {
			return false
		}
		for _, selected := range record.SelectedFields {
			if !selected.Complete {
				return false
			}
		}
	}
	return true
}

func selectedCallsComplete(statuses []actionledger.CallStatus) bool {
	for _, status := range statuses {
		if !status.TerminalComplete || !status.EvidenceComplete || !status.HistoryComplete {
			return false
		}
	}
	return true
}

func selectedCompletenessFact(id FactID, complete bool, count int, label string) Fact {
	if count == 0 {
		return notEvaluatedFact(id, "No "+label+" are present in the requested window.")
	}
	if !complete {
		return missingFact(id, "One or more "+label+" are incomplete.")
	}
	return coveredFact(id, "All "+label+" are complete.")
}

func receiptFact(id FactID, report actionstate.ApprovalReceiptVerificationReport) Fact {
	if report.Invalid > 0 {
		return missingFact(id, "One or more stored approval receipts failed cryptographic reverification.")
	}
	if report.Unavailable > 0 && report.Verified == 0 {
		return missingFact(id, "Stored approval receipt material or its authority registry is unavailable.")
	}
	if report.Unavailable > 0 {
		return partialFact(id, "Some stored approval receipts cannot be cryptographically reverified.")
	}
	return coveredFact(id, fmt.Sprintf("All %d applicable stored approval receipts were cryptographically reverified.", report.Verified))
}

func budgetIdentityFact(input BuildInput, selected []actionledger.Record) Fact {
	transitions := 0
	for _, record := range selected {
		if record.Budget == nil {
			continue
		}
		transitions++
		keyID, valid := action.KeyedIdentityKeyID(record.Budget.StateVersion)
		if !valid || input.StateIntegrity != IntegrityVerified || !input.StatePresent || keyID != input.State.KeyID {
			return missingFact(FactBudgetIdentity, "A selected budget transition is not bound to the current state identity generation.")
		}
	}
	if transitions == 0 {
		return notEvaluatedFact(FactBudgetIdentity, "No budget transition is present in the requested window.")
	}
	return coveredFact(FactBudgetIdentity, fmt.Sprintf("All %d selected budget transitions use the current state identity generation.", transitions))
}

func boolFact(id FactID, value bool, basis string) Fact {
	if value {
		return coveredFact(id, basis)
	}
	return missingFact(id, "Required "+strings.ToLower(basis))
}

func canonicalTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func lowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func platformsSorted(values []action.Platform) bool {
	for index := range values {
		if !action.ValidPlatform(values[index]) || index > 0 && values[index-1] >= values[index] {
			return false
		}
	}
	return true
}
