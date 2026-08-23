package actionevidence

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dlclark/regexp2"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionstate"
)

func TestBuildDerivesCoveredControlFromExactFactsDeterministically(t *testing.T) {
	input := completeBuildInput(t)
	first, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := MarshalJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := MarshalJSON(second)
	if err != nil {
		t.Fatal(err)
	}
	markdown, err := MarshalMarkdown(first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) || first.Identity != second.Identity ||
		first.OverallStatus != StatusCovered || len(first.Controls) != 1 ||
		first.Controls[0].Status != StatusCovered || !first.Window.Complete {
		t.Fatalf("deterministic covered report failed: %#v", first)
	}
	for _, expected := range []string{
		"Repository identity", "Retained history", "Ledger integrity", "State integrity",
		first.MappingPacks[0].SourceURL, first.MappingPacks[0].Identity, string(first.Controls[0].EvidenceSelectors[0]),
	} {
		if !bytes.Contains(markdown, []byte(expected)) {
			t.Fatalf("Markdown omitted required evidence metadata %q", expected)
		}
	}
	for _, private := range []string{"raw-secret-value", "alice@example.test", "Authorization"} {
		if bytes.Contains(firstJSON, []byte(private)) || bytes.Contains(markdown, []byte(private)) {
			t.Fatalf("private fixture escaped into output: %q", private)
		}
	}
	decoded, err := DecodeReport(firstJSON)
	if err != nil || decoded.Identity != first.Identity {
		t.Fatalf("decode report = %#v, %v", decoded, err)
	}
	validateActionEvidenceSchema(t, firstJSON)
}

func TestEveryFactStatusMutationDowngradesItsMappedControl(t *testing.T) {
	for _, id := range AllFactIDs() {
		t.Run(string(id), func(t *testing.T) {
			facts := map[FactID]Fact{}
			for _, factID := range AllFactIDs() {
				facts[factID] = coveredFact(factID, "Verified evidence.")
			}
			facts[id] = missingFact(id, "Evidence was removed by mutation.")
			result := controlResult(testPack([]FactID{id}), Control{
				ID: "example.control", Reference: "Control one", Rationale: "Maps exact technical evidence.",
				EvidenceSelectors: []FactID{id}, KnownGaps: []string{},
			}, facts)
			if result.Status != StatusMissing || !containsString(result.EvidenceGaps, string(id)+":missing") {
				t.Fatalf("mutated fact was promoted: %#v", result)
			}
		})
	}
}

func TestAllFactIDsReturnsAnIsolatedCanonicalCopy(t *testing.T) {
	first := AllFactIDs()
	second := AllFactIDs()
	if len(first) == 0 || !slices.IsSorted(first) || !slices.Equal(first, second) {
		t.Fatalf("fact IDs are not canonical: %#v", first)
	}
	first[0] = FactID("mutated")
	if second[0] == first[0] || !knownFactID(second[0]) || knownFactID(FactID("unknown")) {
		t.Fatal("fact ID registry aliases caller-owned memory or accepts an unknown ID")
	}
}

func TestMissingOrIncompleteFactCannotBePromotedByMappingPack(t *testing.T) {
	input := completeBuildInput(t)
	input.Records[1].Decision.Completeness.ContextComplete = false
	report, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.Controls[0].Status != StatusPartial || report.OverallStatus != StatusPartial {
		t.Fatalf("incomplete evidence was promoted: %#v", report.Controls[0])
	}

	packBody, err := json.Marshal(map[string]any{
		"schema": PackSchema, "format_version": FormatVersion,
		"pack_id": "malicious-pack", "pack_version": "one", "framework": "Example",
		"review_status": "reviewed", "source": map[string]any{
			"url": "https://example.test/source", "edition": "One", "source_date": "2026-08-12",
			"reviewed_at": "2026-08-12", "reuse_notice": "Original mapping text.",
		},
		"controls": []map[string]any{{
			"id": "example.control", "reference": "Control one", "rationale": "Claims coverage without facts.",
			"evidence_selectors": []string{string(FactLedgerEventsComplete)}, "known_gaps": []string{},
			"status": "covered",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePack(packBody); err == nil {
		t.Fatal("custom status override was accepted")
	}
}

func TestBuiltinsAreCurrentSourceBoundAndContainNoForbiddenClaims(t *testing.T) {
	packs, err := BuiltinPacks()
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 4 {
		t.Fatalf("built-in pack count = %d, want 4", len(packs))
	}
	for _, loaded := range packs {
		body, err := json.Marshal(loaded.Pack)
		if err != nil {
			t.Fatal(err)
		}
		if containsForbiddenClaim(string(body)) || loaded.Pack.Source.ReviewedAt != "2026-08-12" ||
			!strings.HasPrefix(loaded.Pack.Source.URL, "https://") || loaded.Identity == "" {
			t.Fatalf("invalid built-in pack: %#v", loaded)
		}
	}
}

func TestPackDigestSignatureBoundsAndMaliciousText(t *testing.T) {
	directory := t.TempDir()
	pack := testPack([]FactID{FactLedgerEventsComplete})
	body, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	packPath := filepath.Join(directory, "pack.json")
	if err := os.WriteFile(packPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := PackIdentity(pack)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPack(packPath, PackAuthentication{ExpectedDigest: identity}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPack(packPath, PackAuthentication{ExpectedDigest: "sha256:" + strings.Repeat("0", 64)}); err == nil {
		t.Fatal("wrong pack digest was accepted")
	}

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signature := PackSignature{
		Schema: PackSignatureSchema, FormatVersion: FormatVersion, PackIdentity: identity,
		AuthorityKeyID: "reviewer", Signature: base64.RawURLEncoding.EncodeToString(
			ed25519.Sign(privateKey, []byte(packSigningContext+identity)),
		),
	}
	registry := MappingAuthorityRegistry{
		Schema: AuthorityRegistrySchema, FormatVersion: FormatVersion,
		Authorities: []MappingAuthority{{ID: "reviewer", PublicKey: base64.RawURLEncoding.EncodeToString(publicKey)}},
	}
	signaturePath := writeJSONFile(t, directory, "signature.json", signature)
	registryPath := writeJSONFile(t, directory, "authorities.json", registry)
	if _, err := LoadPack(packPath, PackAuthentication{SignaturePath: signaturePath, RegistryPath: registryPath}); err != nil {
		t.Fatal(err)
	}

	malicious := pack
	malicious.Controls[0].Rationale = "This is certified and legally sufficient."
	if err := ValidatePack(malicious); err == nil {
		t.Fatal("forbidden assurance claim was accepted")
	}
	private := pack
	private.Controls[0].Rationale = "Contact alice@example.test for proof."
	if err := ValidatePack(private); err == nil {
		t.Fatal("private-data-shaped mapping text was accepted")
	}
	privateURL := pack
	privateURL.Source.URL = "https://example.test/source?api_key=Q7m9V2p4R8x6L3n5"
	if err := ValidatePack(privateURL); err == nil {
		t.Fatal("private-data-shaped mapping source URL was accepted")
	}

	oversizedPath := filepath.Join(directory, "oversized.json")
	file, err := os.Create(oversizedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxPackBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPack(oversizedPath, PackAuthentication{ExpectedDigest: identity}); err == nil {
		t.Fatal("oversized mapping pack was accepted")
	}
}

func TestReportIdentityAndMappingReviewAreTamperEvident(t *testing.T) {
	report, err := Build(completeBuildInput(t))
	if err != nil {
		t.Fatal(err)
	}
	tampered := report
	tampered.Controls = append([]ControlResult(nil), report.Controls...)
	tampered.Controls[0].Status = StatusCovered
	tampered.Controls[0].Rationale = "Different but bounded rationale."
	if err := ValidateReport(tampered); err == nil {
		t.Fatal("tampered report identity was accepted")
	}

	stale := completeBuildInput(t)
	stale.Packs[0].Pack.Source.ReviewedAt = "2025-01-01"
	stale.Packs[0].Identity, err = PackIdentity(stale.Packs[0].Pack)
	if err != nil {
		t.Fatal(err)
	}
	staleReport, err := Build(stale)
	if err != nil {
		t.Fatal(err)
	}
	if staleReport.Controls[0].Status != StatusPartial ||
		!containsString(staleReport.Controls[0].EvidenceGaps, "mapping-review:stale") {
		t.Fatalf("stale mapping did not downgrade: %#v", staleReport.Controls[0])
	}
}

func TestReportValidationRejectsResealedInvalidMetadataAndPrivateText(t *testing.T) {
	baseline, err := Build(completeBuildInput(t))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Report)
	}{
		{name: "policy", mutate: func(report *Report) { report.Policy.SourceDigest = "invalid" }},
		{name: "window", mutate: func(report *Report) { report.Window.Since = "2026-08-12T01:00:00+01:00" }},
		{name: "ledger", mutate: func(report *Report) { report.Ledger.Integrity = "unknown" }},
		{name: "state", mutate: func(report *Report) { report.State.Complete = true }},
		{name: "scenario", mutate: func(report *Report) { report.Scenarios.CorpusIDs[0] = "invalid" }},
		{name: "duplicate scenario dimension", mutate: func(report *Report) {
			report.Scenarios.MissingDimensions = []string{"class:one", "class:one"}
		}},
		{name: "private scenario dimension", mutate: func(report *Report) {
			report.Scenarios.MissingDimensions = []string{"principal:alice@example.test"}
		}},
		{name: "pack provenance", mutate: func(report *Report) { report.MappingPacks[0].Provenance = "untrusted" }},
		{name: "private fact", mutate: func(report *Report) {
			report.Facts[0].Basis = []string{"Contact alice@example.test."}
		}},
		{name: "control gaps", mutate: func(report *Report) {
			report.Controls[0].EvidenceGaps = []string{"unexpected:missing"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := baseline
			report.MappingPacks = append([]PackSummary(nil), baseline.MappingPacks...)
			report.Facts = cloneFacts(baseline.Facts)
			report.Controls = cloneControls(baseline.Controls)
			report.Scenarios.CorpusIDs = append([]string(nil), baseline.Scenarios.CorpusIDs...)
			report.Scenarios.MissingDimensions = append([]string(nil), baseline.Scenarios.MissingDimensions...)
			report.Scenarios.ObservedPlatforms = append([]action.Platform(nil), baseline.Scenarios.ObservedPlatforms...)
			report.Scenarios.MissingPlatforms = append([]action.Platform(nil), baseline.Scenarios.MissingPlatforms...)
			test.mutate(&report)
			report.Identity, err = reportIdentity(report)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateReport(report); err == nil {
				t.Fatal("resealed invalid report was accepted")
			}
		})
	}
}

func cloneFacts(values []Fact) []Fact {
	out := append([]Fact(nil), values...)
	for index := range out {
		out[index].Basis = append([]string(nil), out[index].Basis...)
		out[index].Gaps = append([]string(nil), out[index].Gaps...)
	}
	return out
}

func cloneControls(values []ControlResult) []ControlResult {
	out := append([]ControlResult(nil), values...)
	for index := range out {
		out[index].EvidenceSelectors = append([]FactID(nil), out[index].EvidenceSelectors...)
		out[index].KnownGaps = append([]string(nil), out[index].KnownGaps...)
		out[index].EvidenceGaps = append([]string(nil), out[index].EvidenceGaps...)
	}
	return out
}

func TestStateIntegrityDistinguishesInvalidFromUnavailableEvidence(t *testing.T) {
	input := completeBuildInput(t)
	input.Plan.Budgets = []action.Budget{{ID: "budget-one"}}
	input.Policy.BudgetCount = 1
	input.StateIntegrity = IntegrityInvalid
	input.Receipts.Complete = false
	input.Packs[0].Pack.Controls[0].EvidenceSelectors = []FactID{FactBudgetState, FactRepositoryIdentity}
	var err error
	input.Packs[0].Identity, err = PackIdentity(input.Packs[0].Pack)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.State.Integrity != IntegrityInvalid || report.State.Present || report.Controls[0].Status != StatusPartial ||
		!containsString(reportFact(t, report, FactBudgetState).Gaps, "Current budget state failed integrity verification.") {
		t.Fatalf("invalid state evidence was not explicit: %#v", report.State)
	}

	input.StateIntegrity = IntegrityUnavailable
	input.Receipts.Complete = true
	report, err = Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.State.Integrity != IntegrityUnavailable ||
		!containsString(reportFact(t, report, FactBudgetState).Gaps, "Current verified budget state is unavailable.") {
		t.Fatalf("unavailable state evidence was not explicit: %#v", report.State)
	}
}

func TestStateReceiptSummaryUsesOnlySelectedEvidenceCalls(t *testing.T) {
	input := completeBuildInput(t)
	input.StateIntegrity = IntegrityVerified
	input.StatePresent = true
	input.State = actionstate.StateStatus{
		StateVersion: keyedIdentity("d"), KeyID: strings.Repeat("9", 32),
		ApprovalRecords: []actionstate.ApprovalRecordView{{
			RequestID: approvalID("apr_", "b"), CallID: approvalID("act_", "b"),
			Status: actionapproval.StatusApproved,
		}}, Complete: true,
	}
	input.Receipts = actionstate.ApprovalReceiptVerificationReport{
		Evaluated: true, Complete: false, Applicable: 1, Invalid: 1,
		Records: []actionstate.ApprovalReceiptVerification{{
			RequestID: approvalID("apr_", "b"), CallID: approvalID("act_", "b"),
			ApprovalStatus: actionapproval.StatusApproved, Verification: actionstate.ApprovalReceiptInvalid,
			RegistryIdentity: "sha256:" + strings.Repeat("8", 64), AuthorityKeyID: "authority-one",
			ReceiptID: approvalID("arc_", "b"), ReceiptIdentity: "sha256:" + strings.Repeat("7", 64),
		}},
	}
	report, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if !report.State.Complete || report.State.ReceiptApplicable != 0 || report.State.ReceiptInvalid != 0 {
		t.Fatalf("out-of-window receipt changed state evidence: %#v", report.State)
	}
	tests := []struct {
		name   string
		mutate func(*BuildInput)
	}{
		{name: "unknown verification", mutate: func(candidate *BuildInput) {
			candidate.Receipts.Records[0].Verification = "unknown"
		}},
		{name: "counter mismatch", mutate: func(candidate *BuildInput) { candidate.Receipts.Applicable++ }},
		{name: "state record mismatch", mutate: func(candidate *BuildInput) {
			candidate.Receipts.Records[0].RequestID = approvalID("apr_", "c")
		}},
		{name: "state key mismatch", mutate: func(candidate *BuildInput) {
			candidate.State.KeyID = strings.Repeat("8", 32)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := input
			candidate.State.ApprovalRecords = append([]actionstate.ApprovalRecordView(nil), input.State.ApprovalRecords...)
			candidate.Receipts.Records = append([]actionstate.ApprovalReceiptVerification(nil), input.Receipts.Records...)
			test.mutate(&candidate)
			if _, err := Build(candidate); err == nil {
				t.Fatal("forged approval receipt evidence was accepted")
			}
		})
	}
}

func TestBuildRejectsNonCanonicalUTCLocations(t *testing.T) {
	input := completeBuildInput(t)
	input.AsOf = input.AsOf.In(time.FixedZone("offset-zero", 0))
	if _, err := Build(input); err == nil {
		t.Fatal("non-canonical zero-offset location was accepted")
	}
	input = completeBuildInput(t)
	input.AsOf = input.AsOf.Add(-90 * time.Minute)
	input.Until = input.AsOf
	if _, err := Build(input); err == nil {
		t.Fatal("as-of time preceding retained evidence was accepted")
	}
}

func TestBuildDowngradesConcreteIntegrityCompletenessAndAuthorityGaps(t *testing.T) {
	tests := []struct {
		name       string
		fact       FactID
		wantStatus Status
		mutate     func(*BuildInput)
	}{
		{name: "policy source changed", fact: FactLedgerPolicyIdentity, wantStatus: StatusMissing, mutate: func(input *BuildInput) {
			for index := range input.Records {
				input.Records[index].Call.PolicyDigest = strings.Repeat("8", 64)
			}
		}},
		{name: "ledger integrity failed", fact: FactLedgerIntegrity, wantStatus: StatusMissing, mutate: func(input *BuildInput) {
			input.LedgerVerification.Integrity = actionledger.StatusInvalid
		}},
		{name: "archive gap predates window", fact: FactLedgerWindowComplete, wantStatus: StatusMissing, mutate: func(input *BuildInput) {
			input.LedgerVerification.DroppedHistory = true
			input.LedgerVerification.ArchiveCount = 1
		}},
		{name: "configured host missing", fact: FactHostCoverage, wantStatus: StatusPartial, mutate: func(input *BuildInput) {
			input.Scenarios.Complete = false
			input.Scenarios.MissingPlatforms = []action.Platform{action.PlatformCodex}
		}},
		{name: "approval authority unavailable", fact: FactApprovalAuthority, wantStatus: StatusMissing, mutate: func(input *BuildInput) {
			input.Plan.Approvals = []action.ApprovalDisclosure{{ID: "approval-one"}}
			input.Policy.ApprovalCount = 1
			input.StateIntegrity = IntegrityVerified
			input.StatePresent = true
			input.State = actionstate.StateStatus{
				StateVersion: keyedIdentity("d"), KeyID: strings.Repeat("9", 32),
				ApprovalRecords: []actionstate.ApprovalRecordView{{
					RequestID: approvalID("apr_", "a"), CallID: input.Records[0].Call.CallID,
					Status: actionapproval.StatusApproved,
				}}, Complete: true,
			}
			input.Receipts = actionstate.ApprovalReceiptVerificationReport{
				Evaluated: true, Complete: false, Applicable: 1, Unavailable: 1,
				Records: []actionstate.ApprovalReceiptVerification{{
					RequestID: approvalID("apr_", "a"), CallID: input.Records[0].Call.CallID,
					ApprovalStatus: actionapproval.StatusApproved, Verification: actionstate.ApprovalReceiptUnavailable,
					RegistryIdentity: "sha256:" + strings.Repeat("8", 64), AuthorityKeyID: "authority-one",
					ReceiptID: approvalID("arc_", "a"), ReceiptIdentity: "sha256:" + strings.Repeat("7", 64),
				}},
			}
		}},
		{name: "budget state invalid", fact: FactBudgetState, wantStatus: StatusMissing, mutate: func(input *BuildInput) {
			input.Plan.Budgets = []action.Budget{{ID: "budget-one"}}
			input.Policy.BudgetCount = 1
			input.StateIntegrity = IntegrityInvalid
			input.Receipts.Complete = false
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := completeBuildInput(t)
			test.mutate(&input)
			setTestPackSelectors(t, &input, []FactID{test.fact, FactRepositoryIdentity})
			report, err := Build(input)
			if err != nil {
				t.Fatal(err)
			}
			if fact := reportFact(t, report, test.fact); fact.Status != test.wantStatus ||
				report.Controls[0].Status == StatusCovered {
				t.Fatalf("gap was not downgraded: fact=%#v control=%#v", fact, report.Controls[0])
			}
		})
	}
}

func TestDroppedHistoryCanStillProveAWindowInsideRetainedRange(t *testing.T) {
	input := completeBuildInput(t)
	input.LedgerVerification.DroppedHistory = true
	input.LedgerVerification.ArchiveCount = 1
	input.Since = input.AsOf.Add(-2 * time.Hour)
	report, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Window.DroppedHistory || !report.Window.Complete || report.Window.SelectedCalls != 1 {
		t.Fatalf("retained in-range window was not proven exactly: %#v", report.Window)
	}
}

func setTestPackSelectors(t testing.TB, input *BuildInput, selectors []FactID) {
	t.Helper()
	sort.Slice(selectors, func(i, j int) bool { return selectors[i] < selectors[j] })
	input.Packs[0].Pack.Controls[0].EvidenceSelectors = selectors
	identity, err := PackIdentity(input.Packs[0].Pack)
	if err != nil {
		t.Fatal(err)
	}
	input.Packs[0].Identity = identity
}

func reportFact(t testing.TB, report Report, id FactID) Fact {
	t.Helper()
	for _, fact := range report.Facts {
		if fact.ID == id {
			return fact
		}
	}
	t.Fatalf("fact %q is absent", id)
	return Fact{}
}

func completeBuildInput(t testing.TB) BuildInput {
	t.Helper()
	asOf := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	source := strings.Repeat("1", 64)
	lock := strings.Repeat("2", 64)
	callID := "act_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	call := actionledger.CallBinding{
		CallID: callID, RepositoryIdentity: keyedIdentity("a"),
		PolicyDigest: source, LockDigest: lock, ServerLabel: "gateway",
		ServerFingerprint:  keyedIdentity("b"),
		Tool:               actionledger.ToolIdentity{Mode: action.LedgerDeclarationID, Value: "tool-one"},
		ToolContractDigest: "sha256:" + strings.Repeat("3", 64), Principal: "operator",
		CredentialLabels: []string{}, ContextIdentity: keyedIdentity("c"),
		ContextProvenance: action.ProvenanceOperatorBound,
	}
	complete := action.CompleteEvidence()
	records := []actionledger.Record{
		{Sequence: 1, Digest: "sha256:" + strings.Repeat("4", 64), Timestamp: asOf.Add(-2 * time.Hour).Format(time.RFC3339Nano), Event: actionledger.EventRequestAccepted, Call: call, Decision: actionledger.DecisionBinding{Phase: action.PhasePreCall, RuleIDs: []string{}, Completeness: complete}, SelectedFields: []actionledger.SelectedFieldEvidence{}, RequestAccepted: &actionledger.RequestAccepted{}},
		{Sequence: 2, PreviousDigest: "sha256:" + strings.Repeat("4", 64), Digest: "sha256:" + strings.Repeat("5", 64), Timestamp: asOf.Add(-time.Hour).Format(time.RFC3339Nano), Event: actionledger.EventPreDecision, Call: call, Decision: actionledger.DecisionBinding{Phase: action.PhasePreCall, Decision: action.DecisionBlock, Reason: action.ReasonRuleMatched, RuleIDs: []string{"block-one"}, Completeness: complete}, SelectedFields: []actionledger.SelectedFieldEvidence{}, PreDecision: &actionledger.PreDecision{Outcome: action.OutcomeDispatchBlocked}},
	}
	pack := testPack([]FactID{FactLedgerEventsComplete, FactLedgerIntegrity, FactLedgerPolicyIdentity, FactLedgerWindowComplete})
	identity, err := PackIdentity(pack)
	if err != nil {
		t.Fatal(err)
	}
	return BuildInput{
		AsOf: asOf, Since: asOf.Add(-3 * time.Hour), Until: asOf,
		RepositoryIdentity: keyedIdentity("a"),
		Policy: PolicyEvidence{
			SourceDigest: source, LockDigest: lock, PlanIdentity: "sha256:" + strings.Repeat("6", 64),
			ToolCount: 1, RuleCount: 1,
		},
		Plan:    action.Plan{Tools: []action.Tool{{ID: "tool-one"}}, Rules: []action.Rule{{ID: "block-one"}}, Budgets: []action.Budget{}, Approvals: []action.ApprovalDisclosure{}},
		Records: records,
		LedgerVerification: actionledger.VerificationReport{
			Integrity: actionledger.StatusVerified, ArchiveContinuity: actionledger.StatusVerified,
			DetachedHead: actionledger.HeadMatched, RecordCount: 2, EventsComplete: true, CallsComplete: true,
		},
		StateIntegrity: IntegrityUnavailable,
		Receipts:       actionstate.ApprovalReceiptVerificationReport{Evaluated: true, Complete: true, Records: []actionstate.ApprovalReceiptVerification{}},
		Scenarios: ScenarioEvidence{
			Evaluated: true, CorpusIDs: []string{"sha256:" + strings.Repeat("7", 64)},
			CaseCount: 1, ActionCaseCount: 1, ResultsCurrent: true, Complete: true,
			MissingDimensions: []string{}, ObservedPlatforms: []action.Platform{}, MissingPlatforms: []action.Platform{},
		},
		Packs: []LoadedPack{{Pack: pack, Identity: identity, Provenance: "digest-pinned"}},
	}
}

func testPack(selectors []FactID) Pack {
	return Pack{
		Schema: PackSchema, FormatVersion: FormatVersion, PackID: "example-pack", PackVersion: "one",
		Framework: "Example framework", ReviewStatus: "reviewed",
		Source: Source{
			URL: "https://example.test/source", Edition: "One", SourceDate: "2026-08-12",
			ReviewedAt: "2026-08-12", ReuseNotice: "Original mapping text.",
		},
		Controls: []Control{{
			ID: "example.control", Reference: "Control one", Rationale: "Maps exact technical evidence.",
			EvidenceSelectors: selectors, KnownGaps: []string{},
		}},
	}
}

func keyedIdentity(character string) string {
	return "hmac-sha256:v1:" + strings.Repeat("9", 32) + ":" + strings.Repeat(character, 64)
}

func approvalID(prefix, character string) string {
	return prefix + strings.Repeat(character, 26)
}

func writeJSONFile(t testing.TB, directory, name string, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func validateActionEvidenceSchema(t testing.TB, body []byte) {
	t.Helper()
	schemaPath := filepath.Join("..", "..", "schemas", "v1", "action-evidence.schema.json")
	schemaBody, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(schemaBody, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseRegexpEngine(compileEvidenceRegexp)
	const schemaURL = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/action-evidence.schema.json"
	if err := compiler.AddResource(schemaURL, document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	var report any
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(report); err != nil {
		t.Fatalf("action evidence does not match its public schema: %v", err)
	}
}

type evidenceRegexp regexp2.Regexp

func (regexp *evidenceRegexp) MatchString(value string) bool {
	matched, err := (*regexp2.Regexp)(regexp).MatchString(value)
	return err == nil && matched
}

func (regexp *evidenceRegexp) String() string {
	return (*regexp2.Regexp)(regexp).String()
}

func compileEvidenceRegexp(expression string) (jsonschema.Regexp, error) {
	compiled, err := regexp2.Compile(expression, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	return (*evidenceRegexp)(compiled), nil
}
