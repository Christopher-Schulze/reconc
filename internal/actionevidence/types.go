// Package actionevidence builds deterministic, privacy-bounded technical
// control-evidence mappings from already verified Reconc runtime evidence.
package actionevidence

import (
	"slices"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionstate"
)

const (
	ReportSchema             = "reconc.action-evidence/v1"
	PackSchema               = "reconc.action-control-map/v1"
	PackSignatureSchema      = "reconc.action-control-map-signature/v1"
	AuthorityRegistrySchema  = "reconc.action-control-map-authorities/v1"
	FormatVersion            = "1"
	MaxPackBytes             = 8 << 20
	MaxReportBytes           = 32 << 20
	MaxControls              = 4096
	MaxSelectorsPerControl   = 64
	MaxTextBytes             = 1024
	MaxGapsPerControl        = 64
	MaxPacks                 = 64
	MaxMappingAuthorities    = 256
	MaxAuthorityRegistrySize = 1 << 20
)

const Disclaimer = "This report covers local technical evidence only. Organizational control design, operation, legal assessment, and external assurance remain outside Reconc."

type Status string

const (
	StatusCovered      Status = "covered"
	StatusPartial      Status = "partial"
	StatusMissing      Status = "missing"
	StatusNotEvaluated Status = "not_evaluated"
)

func (s Status) Valid() bool {
	return s == StatusCovered || s == StatusPartial || s == StatusMissing || s == StatusNotEvaluated
}

type IntegrityStatus string

const (
	IntegrityVerified    IntegrityStatus = "verified"
	IntegrityUnavailable IntegrityStatus = "unavailable"
	IntegrityInvalid     IntegrityStatus = "invalid"
)

func (s IntegrityStatus) Valid() bool {
	return s == IntegrityVerified || s == IntegrityUnavailable || s == IntegrityInvalid
}

type FactID string

const (
	FactPolicyLockCurrent       FactID = "policy.lock.current"
	FactPolicyActionTools       FactID = "policy.action.tools"
	FactPolicyActionRules       FactID = "policy.action.rules"
	FactLedgerIntegrity         FactID = "ledger.integrity"
	FactLedgerArchiveContinuity FactID = "ledger.archive-continuity"
	FactLedgerWindowComplete    FactID = "ledger.window.complete"
	FactLedgerPolicyIdentity    FactID = "ledger.policy-identity"
	FactLedgerEventsComplete    FactID = "ledger.events.complete"
	FactLedgerCallsComplete     FactID = "ledger.calls.complete"
	FactApprovalReceipts        FactID = "approval.receipts"
	FactApprovalAuthority       FactID = "approval.authority"
	FactBudgetState             FactID = "budget.state"
	FactBudgetIdentity          FactID = "budget.identity"
	FactScenarioResults         FactID = "scenario.results"
	FactScenarioCompleteness    FactID = "scenario.completeness"
	FactHostCoverage            FactID = "scenario.host-coverage"
	FactRepositoryIdentity      FactID = "identity.repository"
)

var allFactIDs = [...]FactID{
	FactApprovalAuthority, FactApprovalReceipts, FactBudgetIdentity, FactBudgetState,
	FactRepositoryIdentity, FactLedgerArchiveContinuity, FactLedgerCallsComplete,
	FactLedgerEventsComplete, FactLedgerIntegrity, FactLedgerPolicyIdentity,
	FactLedgerWindowComplete, FactPolicyActionRules, FactPolicyActionTools,
	FactPolicyLockCurrent, FactScenarioCompleteness, FactHostCoverage, FactScenarioResults,
}

func AllFactIDs() []FactID {
	return slices.Clone(allFactIDs[:])
}

func knownFactID(id FactID) bool {
	_, found := slices.BinarySearch(allFactIDs[:], id)
	return found
}

type Source struct {
	URL         string `json:"url"`
	Edition     string `json:"edition"`
	SourceDate  string `json:"source_date"`
	ReviewedAt  string `json:"reviewed_at"`
	ReuseNotice string `json:"reuse_notice"`
}

type Control struct {
	ID                string   `json:"id"`
	Reference         string   `json:"reference"`
	Rationale         string   `json:"rationale"`
	EvidenceSelectors []FactID `json:"evidence_selectors"`
	KnownGaps         []string `json:"known_gaps"`
}

type Pack struct {
	Schema        string    `json:"schema"`
	FormatVersion string    `json:"format_version"`
	PackID        string    `json:"pack_id"`
	PackVersion   string    `json:"pack_version"`
	Framework     string    `json:"framework"`
	ReviewStatus  string    `json:"review_status"`
	Source        Source    `json:"source"`
	Controls      []Control `json:"controls"`
}

type LoadedPack struct {
	Pack       Pack
	Identity   string
	Provenance string
}

type PackSignature struct {
	Schema         string `json:"schema"`
	FormatVersion  string `json:"format_version"`
	PackIdentity   string `json:"pack_identity"`
	AuthorityKeyID string `json:"authority_key_id"`
	Signature      string `json:"signature"`
}

type MappingAuthority struct {
	ID        string `json:"id"`
	PublicKey string `json:"public_key"`
}

type MappingAuthorityRegistry struct {
	Schema        string             `json:"schema"`
	FormatVersion string             `json:"format_version"`
	Authorities   []MappingAuthority `json:"authorities"`
}

type PackAuthentication struct {
	ExpectedDigest string
	SignaturePath  string
	RegistryPath   string
}

type PolicyEvidence struct {
	SourceDigest  string `json:"source_digest"`
	LockDigest    string `json:"lock_digest"`
	PlanIdentity  string `json:"plan_identity"`
	ToolCount     int    `json:"tool_count"`
	RuleCount     int    `json:"rule_count"`
	BudgetCount   int    `json:"budget_count"`
	ApprovalCount int    `json:"approval_count"`
}

type WindowEvidence struct {
	Since                 string `json:"since"`
	Until                 string `json:"until"`
	FirstRetainedAt       string `json:"first_retained_at,omitempty"`
	LastRetainedAt        string `json:"last_retained_at,omitempty"`
	FirstRetainedSequence uint64 `json:"first_retained_sequence,omitempty"`
	LastRetainedSequence  uint64 `json:"last_retained_sequence,omitempty"`
	SelectedCalls         int    `json:"selected_calls"`
	SelectedRecords       int    `json:"selected_records"`
	DroppedHistory        bool   `json:"dropped_history"`
	Complete              bool   `json:"complete"`
}

type LedgerEvidence struct {
	Integrity         actionledger.VerificationStatus `json:"integrity"`
	ArchiveContinuity actionledger.VerificationStatus `json:"archive_continuity"`
	DetachedHead      actionledger.HeadStatus         `json:"detached_head"`
	RecordCount       uint64                          `json:"record_count"`
	ArchiveCount      uint32                          `json:"archive_count"`
	EventsComplete    bool                            `json:"events_complete"`
	CallsComplete     bool                            `json:"calls_complete"`
}

type StateEvidence struct {
	Integrity           IntegrityStatus `json:"integrity"`
	Present             bool            `json:"present"`
	StateVersion        string          `json:"state_version,omitempty"`
	Revision            uint64          `json:"revision"`
	KeyID               string          `json:"key_id,omitempty"`
	BudgetCount         int             `json:"budget_count"`
	LiveReservations    int             `json:"live_reservations"`
	Indeterminate       int             `json:"indeterminate_reservations"`
	ApprovalRecordCount int             `json:"approval_record_count"`
	PendingApprovals    int             `json:"pending_approvals"`
	ReceiptApplicable   int             `json:"receipt_applicable"`
	ReceiptVerified     int             `json:"receipt_verified"`
	ReceiptUnavailable  int             `json:"receipt_unavailable"`
	ReceiptInvalid      int             `json:"receipt_invalid"`
	Complete            bool            `json:"complete"`
}

type ScenarioEvidence struct {
	Evaluated         bool              `json:"evaluated"`
	CorpusIDs         []string          `json:"corpus_ids"`
	CaseCount         int               `json:"case_count"`
	ActionCaseCount   int               `json:"action_case_count"`
	ResultsCurrent    bool              `json:"results_current"`
	Complete          bool              `json:"complete"`
	MissingDimensions []string          `json:"missing_dimensions"`
	ObservedPlatforms []action.Platform `json:"observed_platforms"`
	MissingPlatforms  []action.Platform `json:"missing_platforms"`
}

type Fact struct {
	ID     FactID   `json:"id"`
	Status Status   `json:"status"`
	Basis  []string `json:"basis"`
	Gaps   []string `json:"gaps"`
}

type PackSummary struct {
	PackID       string `json:"pack_id"`
	PackVersion  string `json:"pack_version"`
	Framework    string `json:"framework"`
	Identity     string `json:"identity"`
	Provenance   string `json:"provenance"`
	ReviewStatus string `json:"review_status"`
	Edition      string `json:"edition"`
	SourceDate   string `json:"source_date"`
	ReviewedAt   string `json:"reviewed_at"`
	SourceURL    string `json:"source_url"`
}

type ControlResult struct {
	PackID            string   `json:"pack_id"`
	Framework         string   `json:"framework"`
	ControlID         string   `json:"control_id"`
	Reference         string   `json:"reference"`
	Status            Status   `json:"status"`
	Rationale         string   `json:"rationale"`
	EvidenceSelectors []FactID `json:"evidence_selectors"`
	KnownGaps         []string `json:"known_gaps"`
	EvidenceGaps      []string `json:"evidence_gaps"`
}

type Report struct {
	Schema             string           `json:"schema"`
	FormatVersion      string           `json:"format_version"`
	AsOf               string           `json:"as_of"`
	RepositoryIdentity string           `json:"repository_identity"`
	Policy             PolicyEvidence   `json:"policy"`
	Window             WindowEvidence   `json:"window"`
	Ledger             LedgerEvidence   `json:"ledger"`
	State              StateEvidence    `json:"state"`
	Scenarios          ScenarioEvidence `json:"scenarios"`
	MappingPacks       []PackSummary    `json:"mapping_packs"`
	Facts              []Fact           `json:"facts"`
	Controls           []ControlResult  `json:"controls"`
	OverallStatus      Status           `json:"overall_status"`
	Disclaimer         string           `json:"disclaimer"`
	Identity           string           `json:"identity"`
}

type BuildInput struct {
	AsOf               time.Time
	Since              time.Time
	Until              time.Time
	RepositoryIdentity string
	Policy             PolicyEvidence
	Plan               action.Plan
	Records            []actionledger.Record
	LedgerVerification actionledger.VerificationReport
	StateIntegrity     IntegrityStatus
	StatePresent       bool
	State              actionstate.StateStatus
	Receipts           actionstate.ApprovalReceiptVerificationReport
	Scenarios          ScenarioEvidence
	Packs              []LoadedPack
}
