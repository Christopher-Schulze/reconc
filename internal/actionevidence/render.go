package actionevidence

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actioninspect"
	"reconc.dev/reconc/internal/actionledger"
)

func MarshalJSON(report Report) ([]byte, error) {
	if err := ValidateReport(report); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode action evidence report: %w", err)
	}
	body = append(body, '\n')
	if len(body) > MaxReportBytes {
		return nil, fmt.Errorf("action evidence report exceeds %d bytes", MaxReportBytes)
	}
	if containsForbiddenClaim(string(body)) {
		return nil, fmt.Errorf("action evidence report contains a forbidden assurance claim")
	}
	return body, nil
}

func DecodeReport(body []byte) (Report, error) {
	if len(body) == 0 || len(body) > MaxReportBytes {
		return Report{}, fmt.Errorf("action evidence report must contain 1 to %d bytes", MaxReportBytes)
	}
	var report Report
	if err := decodeStrictObject(body, &report); err != nil {
		return Report{}, fmt.Errorf("decode action evidence report: %w", err)
	}
	if err := ValidateReport(report); err != nil {
		return Report{}, err
	}
	return report, nil
}

func ValidateReport(report Report) error {
	if report.Schema != ReportSchema || report.FormatVersion != FormatVersion ||
		report.Disclaimer != Disclaimer || !report.OverallStatus.Valid() ||
		report.RepositoryIdentity != "unavailable" && !action.ValidKeyedIdentity(report.RepositoryIdentity) ||
		!action.ValidSHA256Identity(report.Identity) {
		return fmt.Errorf("action evidence report metadata is invalid")
	}
	asOf, err := parseCanonicalTimestamp(report.AsOf)
	if err != nil {
		return fmt.Errorf("action evidence report as-of time is invalid")
	}
	if report.MappingPacks == nil || report.Facts == nil || report.Controls == nil ||
		report.Scenarios.CorpusIDs == nil || report.Scenarios.MissingDimensions == nil ||
		report.Scenarios.ObservedPlatforms == nil || report.Scenarios.MissingPlatforms == nil {
		return fmt.Errorf("action evidence report collections must be explicit arrays")
	}
	if err := validateStateEvidence(report.State); err != nil {
		return err
	}
	if err := validateReportPolicy(report.Policy); err != nil {
		return err
	}
	if err := validateReportTimeline(asOf, report.Window, report.Ledger); err != nil {
		return err
	}
	scanner, err := actioninspect.NewTextScanner()
	if err != nil {
		return fmt.Errorf("prepare action evidence privacy scanner: %w", err)
	}
	if err := validateReportScenarios(scanner, report.Scenarios); err != nil {
		return err
	}
	packIndex, err := validateReportPacks(scanner, report.MappingPacks)
	if err != nil {
		return err
	}
	if err := validateReportFacts(scanner, report.Facts); err != nil {
		return err
	}
	if err := validateReportControls(scanner, report.Controls, report.Facts, packIndex, asOf); err != nil {
		return err
	}
	if controlsStatus(report.Controls) != report.OverallStatus {
		return fmt.Errorf("action evidence overall status does not match control results")
	}
	want, err := reportIdentity(report)
	if err != nil || subtle.ConstantTimeCompare([]byte(want), []byte(report.Identity)) != 1 {
		return fmt.Errorf("action evidence report identity does not match its contents")
	}
	return nil
}

func validateReportPolicy(policy PolicyEvidence) error {
	if !lowerHex(policy.SourceDigest, 64) || !lowerHex(policy.LockDigest, 64) ||
		!action.ValidSHA256Identity(policy.PlanIdentity) || policy.ToolCount < 0 || policy.RuleCount < 0 ||
		policy.BudgetCount < 0 || policy.ApprovalCount < 0 {
		return fmt.Errorf("action evidence policy metadata is invalid")
	}
	return nil
}

func validateReportTimeline(asOf time.Time, window WindowEvidence, ledger LedgerEvidence) error {
	since, err := parseCanonicalTimestamp(window.Since)
	if err != nil {
		return fmt.Errorf("action evidence window start is invalid")
	}
	until, err := parseCanonicalTimestamp(window.Until)
	if err != nil || !since.Before(until) || until.After(asOf) || window.SelectedCalls < 0 ||
		window.SelectedRecords < window.SelectedCalls || window.SelectedCalls == 0 && window.SelectedRecords != 0 {
		return fmt.Errorf("action evidence window is invalid")
	}
	boundariesAbsent := window.FirstRetainedAt == "" && window.LastRetainedAt == "" &&
		window.FirstRetainedSequence == 0 && window.LastRetainedSequence == 0
	if !boundariesAbsent {
		first, firstErr := parseCanonicalTimestamp(window.FirstRetainedAt)
		last, lastErr := parseCanonicalTimestamp(window.LastRetainedAt)
		if firstErr != nil || lastErr != nil || first.After(last) || window.FirstRetainedSequence == 0 ||
			window.LastRetainedSequence < window.FirstRetainedSequence {
			return fmt.Errorf("action evidence retained-history boundaries are invalid")
		}
	}
	if !validLedgerVerification(ledger.Integrity) || !validLedgerVerification(ledger.ArchiveContinuity) ||
		!validLedgerHead(ledger.DetachedHead) || ledger.ArchiveCount > actionledger.MaxArchives ||
		uint64(window.SelectedRecords) > ledger.RecordCount ||
		ledger.Integrity == actionledger.StatusVerified && (ledger.RecordCount == 0 || boundariesAbsent) ||
		ledger.Integrity == actionledger.StatusEmpty && (ledger.RecordCount != 0 || !boundariesAbsent) {
		return fmt.Errorf("action evidence ledger metadata is invalid")
	}
	return nil
}

func validLedgerVerification(status actionledger.VerificationStatus) bool {
	return status == actionledger.StatusEmpty || status == actionledger.StatusVerified || status == actionledger.StatusInvalid
}

func validLedgerHead(status actionledger.HeadStatus) bool {
	return status == actionledger.HeadAbsent || status == actionledger.HeadMatched || status == actionledger.HeadInvalid
}

func validateReportScenarios(scanner *actioninspect.TextScanner, scenarios ScenarioEvidence) error {
	if err := validateScenarioEvidence(scenarios); err != nil {
		return err
	}
	for index, identity := range scenarios.CorpusIDs {
		if !action.ValidSHA256Identity(identity) || index > 0 && scenarios.CorpusIDs[index-1] == identity {
			return fmt.Errorf("action evidence scenario corpus identities are invalid")
		}
	}
	if err := validateCanonicalTextList(scanner, scenarios.MissingDimensions); err != nil {
		return fmt.Errorf("action evidence scenario missing dimensions: %w", err)
	}
	if !scenarios.Evaluated {
		if len(scenarios.CorpusIDs) != 0 || scenarios.CaseCount != 0 || scenarios.ActionCaseCount != 0 ||
			scenarios.ResultsCurrent || scenarios.Complete || len(scenarios.MissingDimensions) != 0 ||
			len(scenarios.ObservedPlatforms) != 0 || len(scenarios.MissingPlatforms) != 0 {
			return fmt.Errorf("unevaluated action scenario evidence contains results")
		}
		return nil
	}
	if len(scenarios.CorpusIDs) == 0 || scenarios.CaseCount == 0 {
		return fmt.Errorf("evaluated action scenario evidence is empty")
	}
	return nil
}

func validateReportPacks(
	scanner *actioninspect.TextScanner,
	packs []PackSummary,
) (map[string]PackSummary, error) {
	if len(packs) == 0 || len(packs) > MaxPacks {
		return nil, fmt.Errorf("action evidence mapping-pack count is invalid")
	}
	out := make(map[string]PackSummary, len(packs))
	for index, pack := range packs {
		if !action.SafeLabel(pack.PackID) || !action.SafeLabel(pack.PackVersion) ||
			!action.ValidSHA256Identity(pack.Identity) || !validPackProvenance(pack.Provenance) ||
			pack.ReviewStatus != "reviewed" && pack.ReviewStatus != "stale" && pack.ReviewStatus != "not-reviewed" ||
			index > 0 && packs[index-1].PackID >= pack.PackID {
			return nil, fmt.Errorf("action evidence mapping-pack metadata is invalid")
		}
		if err := validateSource(Source{
			URL: pack.SourceURL, SourceDate: pack.SourceDate, ReviewedAt: pack.ReviewedAt,
		}); err != nil {
			return nil, err
		}
		if err := validateTextFields(scanner, pack.Framework, pack.Edition, pack.SourceURL); err != nil {
			return nil, err
		}
		out[pack.PackID] = pack
	}
	return out, nil
}

func validPackProvenance(value string) bool {
	if value == "builtin" || value == "digest-pinned" {
		return true
	}
	return strings.HasPrefix(value, "signed:") && action.SafeLabel(strings.TrimPrefix(value, "signed:"))
}

func validateStateEvidence(state StateEvidence) error {
	if !state.Integrity.Valid() || (state.Integrity == IntegrityVerified) != state.Present ||
		state.BudgetCount < 0 || state.LiveReservations < 0 || state.Indeterminate < 0 ||
		state.ApprovalRecordCount < 0 || state.PendingApprovals < 0 || state.ReceiptApplicable < 0 ||
		state.ReceiptVerified < 0 || state.ReceiptUnavailable < 0 || state.ReceiptInvalid < 0 ||
		state.PendingApprovals > state.ApprovalRecordCount || state.ReceiptApplicable > state.ApprovalRecordCount ||
		state.ReceiptApplicable != state.ReceiptVerified+state.ReceiptUnavailable+state.ReceiptInvalid {
		return fmt.Errorf("action evidence state metadata is invalid")
	}
	if state.Present {
		keyID, valid := action.KeyedIdentityKeyID(state.StateVersion)
		if !valid || keyID != state.KeyID || len(state.KeyID) != 32 ||
			!lowerHex(state.KeyID, 32) || state.Complete && (state.ReceiptUnavailable != 0 || state.ReceiptInvalid != 0) {
			return fmt.Errorf("action evidence state identity is invalid")
		}
		return nil
	}
	if state.StateVersion != "" || state.Revision != 0 || state.KeyID != "" || state.BudgetCount != 0 ||
		state.LiveReservations != 0 || state.Indeterminate != 0 || state.ApprovalRecordCount != 0 ||
		state.PendingApprovals != 0 || state.ReceiptApplicable != 0 || state.ReceiptVerified != 0 ||
		state.ReceiptUnavailable != 0 || state.ReceiptInvalid != 0 || state.Complete {
		return fmt.Errorf("unverified action evidence state exposes untrusted contents")
	}
	return nil
}

func parseCanonicalTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || value != parsed.UTC().Format(time.RFC3339Nano) {
		return time.Time{}, fmt.Errorf("timestamp is not canonical UTC RFC3339")
	}
	return parsed.UTC(), nil
}

func validateReportFacts(scanner *actioninspect.TextScanner, facts []Fact) error {
	if len(facts) != len(AllFactIDs()) {
		return fmt.Errorf("action evidence report must contain every exact evidence fact")
	}
	want := AllFactIDs()
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	for index, fact := range facts {
		if fact.ID != want[index] || !fact.Status.Valid() || fact.Basis == nil || fact.Gaps == nil ||
			!sort.StringsAreSorted(fact.Basis) || !sort.StringsAreSorted(fact.Gaps) ||
			len(fact.Basis) > MaxGapsPerControl || len(fact.Gaps) > MaxGapsPerControl ||
			fact.Status == StatusCovered && (len(fact.Basis) == 0 || len(fact.Gaps) != 0) ||
			fact.Status != StatusCovered && len(fact.Gaps) == 0 {
			return fmt.Errorf("action evidence fact set is invalid or non-canonical")
		}
		if err := validateCanonicalTextList(scanner, fact.Basis); err != nil {
			return fmt.Errorf("action evidence fact %q basis: %w", fact.ID, err)
		}
		if err := validateCanonicalTextList(scanner, fact.Gaps); err != nil {
			return fmt.Errorf("action evidence fact %q gaps: %w", fact.ID, err)
		}
	}
	return nil
}

func validateReportControls(
	scanner *actioninspect.TextScanner,
	controls []ControlResult,
	facts []Fact,
	packs map[string]PackSummary,
	asOf time.Time,
) error {
	if len(controls) == 0 || len(controls) > MaxControls {
		return fmt.Errorf("action evidence control count is invalid")
	}
	byID := make(map[FactID]Fact, len(facts))
	for _, fact := range facts {
		byID[fact.ID] = fact
	}
	for index, control := range controls {
		pack, packExists := packs[control.PackID]
		if index > 0 && controlKey(controls[index-1]) >= controlKey(control) {
			return fmt.Errorf("action evidence control results are not uniquely sorted")
		}
		if !packExists || control.Framework != pack.Framework || !validControlID(control.ControlID) ||
			!control.Status.Valid() || control.EvidenceSelectors == nil ||
			len(control.EvidenceSelectors) == 0 || len(control.EvidenceSelectors) > MaxSelectorsPerControl ||
			control.KnownGaps == nil || len(control.KnownGaps) > MaxGapsPerControl ||
			control.EvidenceGaps == nil || len(control.EvidenceGaps) > MaxSelectorsPerControl+1 ||
			!sort.StringsAreSorted(control.EvidenceGaps) {
			return fmt.Errorf("action evidence control %q metadata or collections are invalid", control.ControlID)
		}
		if err := validateTextFields(scanner, control.Framework, control.Reference, control.Rationale); err != nil {
			return fmt.Errorf("action evidence control %q text: %w", control.ControlID, err)
		}
		if err := validateCanonicalTextList(scanner, control.KnownGaps); err != nil {
			return fmt.Errorf("action evidence control %q known gaps: %w", control.ControlID, err)
		}
		if err := validateCanonicalTextList(scanner, control.EvidenceGaps); err != nil {
			return fmt.Errorf("action evidence control %q evidence gaps: %w", control.ControlID, err)
		}
		statuses := make([]Status, len(control.EvidenceSelectors))
		expectedGaps := []string{}
		for selectorIndex, selector := range control.EvidenceSelectors {
			fact, exists := byID[selector]
			if !exists || selectorIndex > 0 && control.EvidenceSelectors[selectorIndex-1] >= selector {
				return fmt.Errorf("action evidence control selectors are invalid")
			}
			statuses[selectorIndex] = fact.Status
			if fact.Status != StatusCovered {
				expectedGaps = append(expectedGaps, string(selector)+":"+string(fact.Status))
			}
		}
		expectedStatus := combineStatuses(statuses)
		if summaryReviewStale(asOf, pack) {
			if expectedStatus == StatusCovered {
				expectedStatus = StatusPartial
			}
			expectedGaps = append(expectedGaps, "mapping-review:stale")
		}
		sort.Strings(expectedGaps)
		if control.Status != expectedStatus || !slices.Equal(control.EvidenceGaps, expectedGaps) {
			return fmt.Errorf("action evidence control status or gaps do not match exact evidence")
		}
	}
	return nil
}

func validateCanonicalTextList(scanner *actioninspect.TextScanner, values []string) error {
	if !sort.StringsAreSorted(values) {
		return fmt.Errorf("text values must be lexically sorted")
	}
	for index, value := range values {
		if index > 0 && values[index-1] == value {
			return fmt.Errorf("text values must be unique")
		}
		if err := validateTextFields(scanner, value); err != nil {
			return err
		}
	}
	return nil
}

func summaryReviewStale(asOf time.Time, pack PackSummary) bool {
	reviewed, err := time.Parse("2006-01-02", pack.ReviewedAt)
	return err != nil || pack.ReviewStatus != "reviewed" || reviewed.After(asOf) || asOf.Sub(reviewed) > 366*24*time.Hour
}

func controlKey(control ControlResult) string {
	return control.PackID + "\x00" + control.ControlID
}

func MarshalMarkdown(report Report) ([]byte, error) {
	if err := ValidateReport(report); err != nil {
		return nil, err
	}
	var body strings.Builder
	body.WriteString("# Reconc Action Evidence\n\n")
	writeMarkdownSummary(&body, report)
	writeMarkdownPacks(&body, report.MappingPacks)
	writeMarkdownFacts(&body, report.Facts)
	writeMarkdownControls(&body, report.Controls)
	body.WriteString("## Boundary\n\n")
	body.WriteString(markdownText(report.Disclaimer))
	body.WriteString("\n")
	result := []byte(body.String())
	if len(result) > MaxReportBytes {
		return nil, fmt.Errorf("action evidence Markdown exceeds %d bytes", MaxReportBytes)
	}
	if containsForbiddenClaim(string(result)) {
		return nil, fmt.Errorf("action evidence Markdown contains a forbidden assurance claim")
	}
	return result, nil
}

func RenderVerificationText(report Report) []byte {
	return []byte(fmt.Sprintf(
		"Action evidence: %s\nAs of: %s\nWindow: %s to %s\nControls: %d\nMapping packs: %d\nLedger: %s\nState integrity: %s\nState complete: %t\nScenario evidence: %t\n",
		report.OverallStatus, report.AsOf, report.Window.Since, report.Window.Until,
		len(report.Controls), len(report.MappingPacks), report.Ledger.Integrity,
		report.State.Integrity, report.State.Complete, report.Scenarios.Complete,
	))
}

func writeMarkdownSummary(body *strings.Builder, report Report) {
	body.WriteString("- Status: `" + string(report.OverallStatus) + "`\n")
	body.WriteString("- As of: `" + report.AsOf + "`\n")
	body.WriteString("- Repository identity: `" + report.RepositoryIdentity + "`\n")
	body.WriteString("- Window: `" + report.Window.Since + "` to `" + report.Window.Until + "`\n")
	body.WriteString(fmt.Sprintf("- Window completeness: `%t`; selected calls: `%d`; selected records: `%d`\n",
		report.Window.Complete, report.Window.SelectedCalls, report.Window.SelectedRecords))
	if report.Window.FirstRetainedAt != "" {
		body.WriteString(fmt.Sprintf("- Retained history: sequence `%d` at `%s` through sequence `%d` at `%s`; dropped history: `%t`\n",
			report.Window.FirstRetainedSequence, report.Window.FirstRetainedAt,
			report.Window.LastRetainedSequence, report.Window.LastRetainedAt, report.Window.DroppedHistory))
	} else {
		body.WriteString("- Retained history: no retained record\n")
	}
	body.WriteString("- Source digest: `" + report.Policy.SourceDigest + "`\n")
	body.WriteString("- Lock digest: `" + report.Policy.LockDigest + "`\n")
	body.WriteString("- Action plan identity: `" + report.Policy.PlanIdentity + "`\n")
	body.WriteString(fmt.Sprintf("- Ledger integrity: `%s`; archive continuity: `%s`; records: `%d`; archives: `%d`\n",
		report.Ledger.Integrity, report.Ledger.ArchiveContinuity,
		report.Ledger.RecordCount, report.Ledger.ArchiveCount))
	body.WriteString(fmt.Sprintf("- State integrity: `%s`; complete: `%t`\n", report.State.Integrity, report.State.Complete))
	body.WriteString(fmt.Sprintf("- Scenarios evaluated: `%t`; results current: `%t`; complete: `%t`\n",
		report.Scenarios.Evaluated, report.Scenarios.ResultsCurrent, report.Scenarios.Complete))
	body.WriteString("- Report identity: `" + report.Identity + "`\n\n")
}

func writeMarkdownPacks(body *strings.Builder, packs []PackSummary) {
	body.WriteString("## Mapping Packs\n\n")
	body.WriteString("| Pack | Framework | Edition | Source date | Reviewed at | Source | Provenance | Identity |\n")
	body.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, pack := range packs {
		body.WriteString("| " + markdownText(pack.PackID) + " | " + markdownText(pack.Framework) +
			" | " + markdownText(pack.Edition) + " | " + pack.SourceDate + " | " + pack.ReviewedAt +
			" | " + markdownText(pack.SourceURL) + " | " + markdownText(pack.Provenance) +
			" | `" + pack.Identity + "` |\n")
	}
	body.WriteString("\n")
}

func writeMarkdownFacts(body *strings.Builder, facts []Fact) {
	body.WriteString("## Evidence Facts\n\n")
	body.WriteString("| Fact | Status | Gaps |\n")
	body.WriteString("|---|---|---|\n")
	for _, fact := range facts {
		body.WriteString("| `" + string(fact.ID) + "` | `" + string(fact.Status) + "` | " +
			markdownText(strings.Join(fact.Gaps, "; ")) + " |\n")
	}
	body.WriteString("\n")
}

func writeMarkdownControls(body *strings.Builder, controls []ControlResult) {
	body.WriteString("## Control Mappings\n\n")
	body.WriteString("| Pack | Control | Framework | Reference | Status | Selectors | Evidence gaps | Known gaps |\n")
	body.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, control := range controls {
		selectors := make([]string, len(control.EvidenceSelectors))
		for index, selector := range control.EvidenceSelectors {
			selectors[index] = string(selector)
		}
		body.WriteString("| " + markdownText(control.PackID) + " | " + markdownText(control.ControlID) +
			" | " + markdownText(control.Framework) + " | " + markdownText(control.Reference) +
			" | `" + string(control.Status) + "` | " + markdownText(strings.Join(selectors, "; ")) +
			" | " + markdownText(strings.Join(control.EvidenceGaps, "; ")) +
			" | " + markdownText(strings.Join(control.KnownGaps, "; ")) + " |\n")
	}
	body.WriteString("\n")
}

func markdownText(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\", "|", "\\|", "`", "\\`", "*", "\\*", "_", "\\_",
		"[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;", "#", "\\#",
	)
	return replacer.Replace(value)
}
