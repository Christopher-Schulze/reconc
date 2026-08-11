// Package impactlab compares current and additive candidate policy decisions
// over explicit privacy-bounded replay evidence.
package impactlab

import (
	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/runtime"
)

const (
	LegacyCorpusFormatVersion  = "reconc-impact-corpus/v1"
	CorpusFormatVersion        = "reconc-impact-corpus/v2"
	ReportFormatVersion        = "reconc-impact-report/v2"
	DeltaManifestFormatVersion = "reconc-impact-delta-manifest/v1"
	MaxCorpusBytes             = 64 << 20
	MaxDeltaManifestBytes      = 8 << 20
	maxCases                   = 10000
	maxItemsPerField           = 2048
	maxTotalItems              = 65536
	maxValueBytes              = 4096
	maxCaseIDBytes             = 128
	maxRationaleBytes          = 2048
)

// EventClass identifies which evidence surface a capture covered.
type EventClass string

const (
	EventClassRead           EventClass = "read"
	EventClassWrite          EventClass = "write"
	EventClassCommand        EventClass = "command"
	EventClassCommandOutcome EventClass = "command_outcome"
	EventClassClaim          EventClass = "claim"
)

// AllEventClasses returns the canonical replay evidence classes.
func AllEventClasses() []EventClass {
	return []EventClass{
		EventClassRead, EventClassWrite, EventClassCommand,
		EventClassCommandOutcome, EventClassClaim,
	}
}

// CaseKind is the strict discriminant for one format-2 scenario.
type CaseKind string

const (
	CaseRepository CaseKind = "repository"
	CaseActionPre  CaseKind = "action_pre"
	CaseActionPost CaseKind = "action_post"
)

func (k CaseKind) Valid() bool {
	return k == CaseRepository || k == CaseActionPre || k == CaseActionPost
}

// ActionDimensions names exact coverage requirements and observations. Every
// collection is canonical, unique, and non-null.
type ActionDimensions struct {
	Classes             []CaseKind              `json:"classes"`
	Tools               []string                `json:"tools"`
	Phases              []action.Phase          `json:"phases"`
	Decisions           []action.Decision       `json:"decisions"`
	Provenance          []action.Provenance     `json:"provenance"`
	Outcomes            []action.PhaseOutcome   `json:"outcomes"`
	Approvals           []action.ApprovalStatus `json:"approvals"`
	ApprovalTransitions []actionapproval.Status `json:"approval_transitions"`
}

// ActionCoverage distinguishes represented dimensions from explicit review
// requirements. Complete is recomputed and cannot be asserted from case count.
type ActionCoverage struct {
	Observed ActionDimensions `json:"observed"`
	Required ActionDimensions `json:"required"`
	Missing  ActionDimensions `json:"missing"`
	Complete bool             `json:"complete"`
}

// Completeness states what was observed, what the capture mechanism covered,
// and what remains unknowable. Redaction always removes completeness.
type Completeness struct {
	ObservedEventClasses []EventClass   `json:"observed_event_classes"`
	CompleteEventClasses []EventClass   `json:"complete_event_classes"`
	MissingEventClasses  []EventClass   `json:"missing_event_classes"`
	RedactedEventClasses []EventClass   `json:"redacted_event_classes"`
	RedactionCount       int            `json:"redaction_count"`
	CompleteReplay       bool           `json:"complete_replay"`
	Action               ActionCoverage `json:"action"`
}

// RepositoryCase owns one deterministic repository-evidence fixture.
type RepositoryCase struct {
	Inputs               runtime.ExecutionInputs `json:"inputs"`
	RedactedEventClasses []EventClass            `json:"redacted_event_classes"`
	RedactionCount       int                     `json:"redaction_count"`
}

// ActionRequestFixture is the policy-independent transport request. Policy and
// lock digests are rebound from each exact compiled runtime under comparison.
type ActionRequestFixture struct {
	FormatVersion      string                   `json:"format_version"`
	CallID             string                   `json:"call_id"`
	Transport          action.Transport         `json:"transport"`
	Platform           action.Platform          `json:"platform,omitempty"`
	ServerLabel        string                   `json:"server_label,omitempty"`
	ServerFingerprint  string                   `json:"server_fingerprint"`
	Tool               string                   `json:"tool"`
	ToolContractDigest string                   `json:"tool_contract_digest"`
	Phase              action.Phase             `json:"phase"`
	RepositoryIdentity string                   `json:"repository_identity"`
	AuthorityMode      action.AuthorityMode     `json:"authority_mode"`
	Arguments          ActionPayload            `json:"arguments,omitempty"`
	Result             ActionPayload            `json:"result,omitempty"`
	Progress           ActionPayload            `json:"progress,omitempty"`
	Context            []action.RawContextValue `json:"context"`
	Completeness       action.Completeness      `json:"completeness"`
	Deadline           action.DeadlineState     `json:"deadline"`
	StateVersion       string                   `json:"state_version"`
}

// ActionPayload stores exact raw JSON as a JSON string so malformed and
// duplicate-key test values cannot corrupt the enclosing corpus object.
type ActionPayload string

// ActionStateFixture contains caller-owned mutable inputs. None of these values
// can be upgraded from the action arguments or downstream result.
type ActionStateFixture struct {
	ContextIdentity    string                            `json:"context_identity"`
	ExecutableDigest   string                            `json:"executable_digest"`
	Principal          string                            `json:"principal"`
	CredentialLabels   []string                          `json:"credential_labels"`
	Approval           action.ApprovalSnapshot           `json:"approval"`
	ApprovalTransition actionapproval.Status             `json:"approval_transition,omitempty"`
	Taint              action.TaintSnapshot              `json:"taint"`
	RepositoryEffect   *action.RepositoryEffectCandidate `json:"repository_effect,omitempty"`
	Lifecycle          action.LifecycleState             `json:"lifecycle"`
	CachePolicyVersion string                            `json:"cache_policy_version"`
	Budget             action.BudgetSnapshot             `json:"budget"`
	ResampleDrift      []ActionIdentityComponent         `json:"resample_drift"`
}

// ActionIdentityComponent selects one explicit between-check-and-use mutation
// for negative scenario coverage.
type ActionIdentityComponent string

const (
	IdentityPlan             ActionIdentityComponent = "plan"
	IdentitySource           ActionIdentityComponent = "source"
	IdentityPolicy           ActionIdentityComponent = "policy"
	IdentityLock             ActionIdentityComponent = "lock"
	IdentityAuthority        ActionIdentityComponent = "authority"
	IdentityServer           ActionIdentityComponent = "server"
	IdentityToolContract     ActionIdentityComponent = "tool_contract"
	IdentityExecutable       ActionIdentityComponent = "executable"
	IdentityRepository       ActionIdentityComponent = "repository"
	IdentityContext          ActionIdentityComponent = "context"
	IdentityPrincipal        ActionIdentityComponent = "principal"
	IdentityCredentials      ActionIdentityComponent = "credentials"
	IdentityState            ActionIdentityComponent = "state"
	IdentityBudget           ActionIdentityComponent = "budget"
	IdentityReservation      ActionIdentityComponent = "reservation"
	IdentityApproval         ActionIdentityComponent = "approval"
	IdentityTaint            ActionIdentityComponent = "taint"
	IdentityRepositoryEffect ActionIdentityComponent = "repository_effect"
)

func (c ActionIdentityComponent) Valid() bool {
	switch c {
	case IdentityPlan, IdentitySource, IdentityPolicy, IdentityLock,
		IdentityAuthority, IdentityServer, IdentityToolContract, IdentityExecutable,
		IdentityRepository, IdentityContext, IdentityPrincipal,
		IdentityCredentials, IdentityState, IdentityBudget, IdentityReservation,
		IdentityApproval, IdentityTaint,
		IdentityRepositoryEffect:
		return true
	default:
		return false
	}
}

// ActionCacheAssertion is the exact reusable-decision expectation.
type ActionCacheAssertion struct {
	Eligible bool               `json:"eligible"`
	Reason   action.CacheReason `json:"reason"`
}

// ActionApprovalAssertion binds the exact redacted approval state visible to
// the evaluator and, for approval decisions, its call-specific requirement.
type ActionApprovalAssertion struct {
	Status                   action.ApprovalStatus `json:"status"`
	Identity                 string                `json:"identity"`
	RequiredApprovalIdentity string                `json:"required_approval_identity,omitempty"`
	Transition               actionapproval.Status `json:"transition,omitempty"`
}

// ActionAssertion is mandatory for every action case and contains only stable,
// privacy-bounded outcome fields.
type ActionAssertion struct {
	Decision       action.Decision          `json:"decision"`
	Reason         action.ReasonCode        `json:"reason"`
	ToolID         string                   `json:"tool_id"`
	MatchedRuleIDs []string                 `json:"matched_rule_ids"`
	Cache          ActionCacheAssertion     `json:"cache"`
	Completeness   action.Completeness      `json:"completeness"`
	PhaseOutcome   action.PhaseOutcome      `json:"phase_outcome"`
	FailureCode    action.ReasonCode        `json:"failure_code,omitempty"`
	Approval       *ActionApprovalAssertion `json:"approval,omitempty"`
}

// ActionValueSummary records a removed selected value without retaining it.
// Identity is optional until a trusted keyed digest owner supplies one.
type ActionValueSummary struct {
	Source     action.ValueSource `json:"source"`
	Pointer    string             `json:"pointer"`
	Category   string             `json:"category"`
	ByteLength int                `json:"byte_length"`
	ItemCount  int                `json:"item_count"`
	Provenance action.Provenance  `json:"provenance"`
	Identity   string             `json:"identity,omitempty"`
}

// ActionCase is one transport-neutral pre-call or post-result scenario.
type ActionCase struct {
	ToolID         string               `json:"tool_id"`
	Request        ActionRequestFixture `json:"request"`
	State          ActionStateFixture   `json:"state"`
	Expected       ActionAssertion      `json:"expected"`
	SelectedValues []ActionValueSummary `json:"selected_values"`
	RedactionCount int                  `json:"redaction_count"`
}

// Case is one strict discriminated format-2 fixture.
type Case struct {
	ID         string          `json:"id"`
	Kind       CaseKind        `json:"kind"`
	Repository *RepositoryCase `json:"repository,omitempty"`
	Action     *ActionCase     `json:"action,omitempty"`
}

// Corpus is the import/export contract for explicit or captured fixtures.
type Corpus struct {
	FormatVersion string       `json:"format_version"`
	CorpusID      string       `json:"corpus_id"`
	Completeness  Completeness `json:"completeness"`
	Cases         []Case       `json:"cases"`
}

// Candidate identifies the additive in-memory candidate without its host path.
type Candidate struct {
	Kind               string `json:"kind"`
	Name               string `json:"name"`
	SourceDigest       string `json:"source_digest"`
	LockDigest         string `json:"lock_digest"`
	ActionPlanIdentity string `json:"action_plan_identity"`
	RuleCount          int    `json:"rule_count"`
	ActionToolCount    int    `json:"action_tool_count"`
	ActionRuleCount    int    `json:"action_rule_count"`
}

// CostDelta compares deterministic structural evaluator work.
type CostDelta struct {
	Current        runtime.EvaluationMetrics `json:"current"`
	Candidate      runtime.EvaluationMetrics `json:"candidate"`
	EstimatedUnits int64                     `json:"estimated_units_delta"`
}

// RepositoryComparison is the exact repository-policy delta for one fixture.
type RepositoryComparison struct {
	CurrentDecision      runtime.Decision `json:"current_decision"`
	CandidateDecision    runtime.Decision `json:"candidate_decision"`
	DecisionChanged      bool             `json:"decision_changed"`
	NewlyBlockingRules   []string         `json:"newly_blocking_rules"`
	NewlyWarningRules    []string         `json:"newly_warning_rules"`
	ResolvedRules        []string         `json:"resolved_rules"`
	ActionChanged        bool             `json:"action_changed"`
	CurrentActions       []string         `json:"current_actions"`
	CandidateActions     []string         `json:"candidate_actions"`
	ActionRedactionCount int              `json:"action_redaction_count"`
	Cost                 CostDelta        `json:"cost"`
}

// ActionDeltaKind is one independently reviewable semantic delta class.
type ActionDeltaKind string

const (
	DeltaDecision              ActionDeltaKind = "decision"
	DeltaNewlyAllowed          ActionDeltaKind = "newly_allowed"
	DeltaNewlyWarned           ActionDeltaKind = "newly_warned"
	DeltaNewlyApprovalRequired ActionDeltaKind = "newly_approval_required"
	DeltaNewlyBlocked          ActionDeltaKind = "newly_blocked"
	DeltaRuleTrace             ActionDeltaKind = "rule_trace"
	DeltaCache                 ActionDeltaKind = "cache"
	DeltaPhaseOutcome          ActionDeltaKind = "phase_outcome"
	DeltaCompleteness          ActionDeltaKind = "completeness"
	DeltaReason                ActionDeltaKind = "reason"
	DeltaToolIdentity          ActionDeltaKind = "tool_identity"
	DeltaFailure               ActionDeltaKind = "failure"
	DeltaApproval              ActionDeltaKind = "approval"
)

func (k ActionDeltaKind) Valid() bool {
	switch k {
	case DeltaDecision, DeltaNewlyAllowed, DeltaNewlyWarned, DeltaNewlyApprovalRequired,
		DeltaNewlyBlocked, DeltaRuleTrace, DeltaCache, DeltaPhaseOutcome,
		DeltaCompleteness, DeltaReason, DeltaToolIdentity, DeltaFailure, DeltaApproval:
		return true
	default:
		return false
	}
}

// ActionObservation is the bounded production-evaluator result and trace.
type ActionObservation struct {
	Outcome       ActionAssertion     `json:"outcome"`
	Trace         []action.TraceEntry `json:"trace"`
	TraceComplete bool                `json:"trace_complete"`
	TraceOmitted  int                 `json:"trace_omitted"`
	Identity      string              `json:"identity"`
}

// ActionComparison is the exact current-candidate delta for one action case.
type ActionComparison struct {
	Current   ActionObservation `json:"current"`
	Candidate ActionObservation `json:"candidate"`
	Deltas    []ActionDeltaKind `json:"deltas"`
	Reviewed  bool              `json:"reviewed"`
}

// CaseComparison preserves the format-2 discriminated case shape in reports.
type CaseComparison struct {
	ID           string                `json:"id"`
	Kind         CaseKind              `json:"kind"`
	CaseIdentity string                `json:"case_identity"`
	Repository   *RepositoryComparison `json:"repository,omitempty"`
	Action       *ActionComparison     `json:"action,omitempty"`
}

// RuleImpact counts violation matches across the replay corpus.
type RuleImpact struct {
	RuleID           string `json:"rule_id"`
	CurrentMatches   int    `json:"current_matches"`
	CandidateMatches int    `json:"candidate_matches"`
	MatchDelta       int    `json:"match_delta"`
}

// Summary is the bounded quick-scan comparison.
type Summary struct {
	CaseCount                        int   `json:"case_count"`
	DecisionChanges                  int   `json:"decision_changes"`
	NewlyBlockingCases               int   `json:"newly_blocking_cases"`
	NewlyWarningCases                int   `json:"newly_warning_cases"`
	ResolvedViolationCount           int   `json:"resolved_violation_count"`
	ActionChanges                    int   `json:"action_changes"`
	CurrentEstimatedUnits            int64 `json:"current_estimated_units"`
	CandidateEstimatedUnits          int64 `json:"candidate_estimated_units"`
	EstimatedUnitsDelta              int64 `json:"estimated_units_delta"`
	ActionCaseCount                  int   `json:"action_case_count"`
	ActionDecisionChanges            int   `json:"action_decision_changes"`
	NewlyAllowedActionCases          int   `json:"newly_allowed_action_cases"`
	NewlyWarnedActionCases           int   `json:"newly_warned_action_cases"`
	NewlyApprovalRequiredActionCases int   `json:"newly_approval_required_action_cases"`
	NewlyBlockedActionCases          int   `json:"newly_blocked_action_cases"`
	ActionRuleTraceChanges           int   `json:"action_rule_trace_changes"`
	ActionCacheChanges               int   `json:"action_cache_changes"`
	ActionPhaseOutcomeChanges        int   `json:"action_phase_outcome_changes"`
	ActionCompletenessChanges        int   `json:"action_completeness_changes"`
	ActionReasonChanges              int   `json:"action_reason_changes"`
	ActionToolIdentityChanges        int   `json:"action_tool_identity_changes"`
	ActionFailureChanges             int   `json:"action_failure_changes"`
	ActionApprovalChanges            int   `json:"action_approval_changes"`
}

// DeltaGate is the exact CI review state for permission and block changes.
type DeltaGate struct {
	Passed          bool     `json:"passed"`
	RequiredCount   int      `json:"required_count"`
	ReviewedCount   int      `json:"reviewed_count"`
	UnreviewedCases []string `json:"unreviewed_cases"`
}

// DeltaManifest contains exact, expiring reviews for gated action deltas.
type DeltaManifest struct {
	FormatVersion string                `json:"format_version"`
	ManifestID    string                `json:"manifest_id"`
	Entries       []ReviewedActionDelta `json:"entries"`
}

// ReviewedActionDelta binds one case and both exact outcomes to one candidate.
type ReviewedActionDelta struct {
	CaseID              string          `json:"case_id"`
	CaseIdentity        string          `json:"case_identity"`
	Delta               ActionDeltaKind `json:"delta"`
	CandidateLockDigest string          `json:"candidate_lock_digest"`
	Current             ActionAssertion `json:"current"`
	Candidate           ActionAssertion `json:"candidate"`
	Rationale           string          `json:"rationale"`
	Permanent           bool            `json:"permanent"`
	ExpiresAt           string          `json:"expires_at,omitempty"`
}

// Report is the deterministic policy-impact result.
type Report struct {
	FormatVersion              string           `json:"format_version"`
	CorpusID                   string           `json:"corpus_id"`
	CorpusCompleteness         Completeness     `json:"corpus_completeness"`
	Candidate                  Candidate        `json:"candidate"`
	Summary                    Summary          `json:"summary"`
	Cases                      []CaseComparison `json:"cases"`
	Rules                      []RuleImpact     `json:"rules"`
	CorpusUnmatchedRules       []string         `json:"corpus_unmatched_rules"`
	ActionRules                []RuleImpact     `json:"action_rules"`
	ActionCorpusUnmatchedRules []string         `json:"action_corpus_unmatched_rules"`
	DeltaGate                  DeltaGate        `json:"delta_gate"`
	SafetyConclusion           string           `json:"safety_conclusion"`
}
