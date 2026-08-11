package action

import "encoding/json"

const (
	RequestFormatVersion    = "1"
	CacheIdentityVersion    = "action-decision-v1"
	MaxContextValues        = 256
	MaxCredentialLabels     = 256
	MaxTraceEntries         = 256
	MaxTraceBytes           = 64 << 10
	MaxDecisionCacheEntries = 256
)

type AuthorityMode string

const (
	AuthorityOperatorPinned    AuthorityMode = "operator_pinned"
	AuthorityRepositoryManaged AuthorityMode = "repository_managed"
)

func (m AuthorityMode) Valid() bool {
	return m == AuthorityOperatorPinned || m == AuthorityRepositoryManaged
}

type DeadlineState string

const (
	DeadlineReady    DeadlineState = "ready"
	DeadlineExceeded DeadlineState = "exceeded"
)

func (s DeadlineState) Valid() bool {
	return s == DeadlineReady || s == DeadlineExceeded
}

type LifecycleState string

const (
	LifecycleActive    LifecycleState = "active"
	LifecycleCancelled LifecycleState = "cancelled"
	LifecycleShutdown  LifecycleState = "shutdown"
)

func (s LifecycleState) Valid() bool {
	return s == LifecycleActive || s == LifecycleCancelled || s == LifecycleShutdown
}

type ApprovalStatus string

const (
	ApprovalNone              ApprovalStatus = "none"
	ApprovalPending           ApprovalStatus = "pending"
	ApprovalCurrentUnconsumed ApprovalStatus = "current_unconsumed"
	ApprovalConsumed          ApprovalStatus = "consumed"
)

func (s ApprovalStatus) Valid() bool {
	switch s {
	case ApprovalNone, ApprovalPending, ApprovalCurrentUnconsumed, ApprovalConsumed:
		return true
	default:
		return false
	}
}

type TaintStatus string

const (
	TaintClean   TaintStatus = "clean"
	TaintPresent TaintStatus = "tainted"
	TaintUnknown TaintStatus = "unknown"
)

func (s TaintStatus) Valid() bool {
	return s == TaintClean || s == TaintPresent || s == TaintUnknown
}

type EvidenceField string

const (
	EvidenceRequest  EvidenceField = "request"
	EvidencePolicy   EvidenceField = "policy"
	EvidenceIdentity EvidenceField = "identity"
	EvidenceContext  EvidenceField = "context"
	EvidenceState    EvidenceField = "state"
	EvidencePhase    EvidenceField = "phase"
)

func (f EvidenceField) Valid() bool {
	switch f {
	case EvidenceRequest, EvidencePolicy, EvidenceIdentity, EvidenceContext, EvidenceState, EvidencePhase:
		return true
	default:
		return false
	}
}

type ReasonCode string

const (
	ReasonDeclaredTool             ReasonCode = "declared_tool"
	ReasonHostUnmatched            ReasonCode = "host_unmatched"
	ReasonRuleMatched              ReasonCode = "rule_matched"
	ReasonInvalidRequest           ReasonCode = "invalid_request"
	ReasonDuplicateKey             ReasonCode = "duplicate_key"
	ReasonInvalidUTF8              ReasonCode = "invalid_utf8"
	ReasonSchemaInvalid            ReasonCode = "schema_invalid"
	ReasonLimitExceeded            ReasonCode = "limit_exceeded"
	ReasonPolicyMissing            ReasonCode = "policy_missing"
	ReasonPolicyStale              ReasonCode = "policy_stale"
	ReasonLockMismatch             ReasonCode = "lock_mismatch"
	ReasonUnsupportedPhase         ReasonCode = "unsupported_phase"
	ReasonToolUnclassified         ReasonCode = "tool_unclassified"
	ReasonToolContractStale        ReasonCode = "tool_contract_stale"
	ReasonContextUntrusted         ReasonCode = "context_untrusted"
	ReasonIdentityUnavailable      ReasonCode = "identity_unavailable"
	ReasonConditionIndeterminate   ReasonCode = "condition_indeterminate"
	ReasonBudgetExhausted          ReasonCode = "budget_exhausted"
	ReasonStateUnavailable         ReasonCode = "state_unavailable"
	ReasonStateCorrupt             ReasonCode = "state_corrupt"
	ReasonReservationIndeterminate ReasonCode = "reservation_indeterminate"
	ReasonApprovalRequired         ReasonCode = "approval_required"
	ReasonApprovalRejected         ReasonCode = "approval_rejected"
	ReasonApprovalInvalid          ReasonCode = "approval_invalid"
	ReasonApprovalExpired          ReasonCode = "approval_expired"
	ReasonApprovalReplayed         ReasonCode = "approval_replayed"
	ReasonAuthorityUnavailable     ReasonCode = "authority_unavailable"
	ReasonInspectionIncomplete     ReasonCode = "inspection_incomplete"
	ReasonUnsupportedContent       ReasonCode = "unsupported_content"
	ReasonResultWithheld           ReasonCode = "result_withheld"
	ReasonLedgerUnavailable        ReasonCode = "ledger_unavailable"
	ReasonLedgerCorrupt            ReasonCode = "ledger_corrupt"
	ReasonDownstreamUnavailable    ReasonCode = "downstream_unavailable"
	ReasonDownstreamError          ReasonCode = "downstream_error"
	ReasonDownstreamUnknown        ReasonCode = "downstream_outcome_unknown"
	ReasonProtocolError            ReasonCode = "protocol_error"
	ReasonCancelled                ReasonCode = "cancelled"
	ReasonDeadlineExceeded         ReasonCode = "deadline_exceeded"
	ReasonShutdown                 ReasonCode = "shutdown"
	ReasonInternalInvariant        ReasonCode = "internal_invariant"
)

func (c ReasonCode) Valid() bool {
	switch c {
	case ReasonDeclaredTool, ReasonHostUnmatched, ReasonRuleMatched,
		ReasonInvalidRequest, ReasonDuplicateKey, ReasonInvalidUTF8,
		ReasonSchemaInvalid, ReasonLimitExceeded, ReasonPolicyMissing,
		ReasonPolicyStale, ReasonLockMismatch, ReasonUnsupportedPhase,
		ReasonToolUnclassified, ReasonToolContractStale,
		ReasonContextUntrusted, ReasonIdentityUnavailable,
		ReasonConditionIndeterminate, ReasonBudgetExhausted,
		ReasonStateUnavailable, ReasonStateCorrupt,
		ReasonReservationIndeterminate, ReasonApprovalRequired,
		ReasonApprovalRejected, ReasonApprovalInvalid,
		ReasonApprovalExpired, ReasonApprovalReplayed,
		ReasonAuthorityUnavailable, ReasonInspectionIncomplete,
		ReasonUnsupportedContent, ReasonResultWithheld,
		ReasonLedgerUnavailable, ReasonLedgerCorrupt,
		ReasonDownstreamUnavailable, ReasonDownstreamError,
		ReasonDownstreamUnknown, ReasonProtocolError, ReasonCancelled,
		ReasonDeadlineExceeded, ReasonShutdown, ReasonInternalInvariant:
		return true
	default:
		return false
	}
}

type MissingEvidence struct {
	Field  EvidenceField `json:"field"`
	Reason ReasonCode    `json:"reason"`
}

type Completeness struct {
	RequestComplete  bool              `json:"request_complete"`
	PolicyComplete   bool              `json:"policy_complete"`
	IdentityComplete bool              `json:"identity_complete"`
	ContextComplete  bool              `json:"context_complete"`
	StateComplete    bool              `json:"state_complete"`
	PhaseComplete    bool              `json:"phase_complete"`
	Missing          []MissingEvidence `json:"missing"`
}

func CompleteEvidence() Completeness {
	return Completeness{
		RequestComplete: true, PolicyComplete: true, IdentityComplete: true,
		ContextComplete: true, StateComplete: true, PhaseComplete: true,
		Missing: []MissingEvidence{},
	}
}

func (c Completeness) Complete() bool {
	return c.RequestComplete && c.PolicyComplete && c.IdentityComplete &&
		c.ContextComplete && c.StateComplete && c.PhaseComplete && len(c.Missing) == 0
}

type RawContextValue struct {
	Name       string          `json:"name"`
	Value      json.RawMessage `json:"value,omitempty"`
	Provenance Provenance      `json:"provenance"`
	Available  bool            `json:"available"`
}

type ContextValue struct {
	Name       string     `json:"name"`
	Value      Value      `json:"value"`
	Provenance Provenance `json:"provenance"`
	Available  bool       `json:"available"`
}

func (v ContextValue) MarshalJSON() ([]byte, error) {
	type encodedContextValue struct {
		Name       string     `json:"name"`
		Value      *Value     `json:"value,omitempty"`
		Provenance Provenance `json:"provenance"`
		Available  bool       `json:"available"`
	}
	encoded := encodedContextValue{
		Name: v.Name, Provenance: v.Provenance, Available: v.Available,
	}
	if v.Available {
		value := v.Value
		encoded.Value = &value
	}
	return json.Marshal(encoded)
}

type RawRequest struct {
	FormatVersion      string            `json:"format_version"`
	CallID             string            `json:"call_id"`
	Transport          Transport         `json:"transport"`
	Platform           Platform          `json:"platform,omitempty"`
	ServerLabel        string            `json:"server_label,omitempty"`
	ServerFingerprint  string            `json:"server_fingerprint"`
	Tool               string            `json:"tool"`
	ToolContractDigest string            `json:"tool_contract_digest"`
	Phase              Phase             `json:"phase"`
	RepositoryIdentity string            `json:"repository_identity"`
	PolicyDigest       string            `json:"policy_digest"`
	LockDigest         string            `json:"lock_digest"`
	AuthorityMode      AuthorityMode     `json:"authority_mode"`
	Arguments          json.RawMessage   `json:"arguments,omitempty"`
	Result             json.RawMessage   `json:"result,omitempty"`
	Progress           json.RawMessage   `json:"progress,omitempty"`
	Context            []RawContextValue `json:"context"`
	Completeness       Completeness      `json:"completeness"`
	Deadline           DeadlineState     `json:"deadline"`
	StateVersion       string            `json:"state_version"`
}

type Request struct {
	FormatVersion      string         `json:"format_version"`
	CallID             string         `json:"call_id"`
	Transport          Transport      `json:"transport"`
	Platform           Platform       `json:"platform,omitempty"`
	ServerLabel        string         `json:"server_label,omitempty"`
	ServerFingerprint  string         `json:"server_fingerprint"`
	Tool               string         `json:"tool"`
	ToolContractDigest string         `json:"tool_contract_digest"`
	Phase              Phase          `json:"phase"`
	RepositoryIdentity string         `json:"repository_identity"`
	PolicyDigest       string         `json:"policy_digest"`
	LockDigest         string         `json:"lock_digest"`
	AuthorityMode      AuthorityMode  `json:"authority_mode"`
	Arguments          *Value         `json:"arguments,omitempty"`
	Result             *Value         `json:"result,omitempty"`
	Progress           *Value         `json:"progress,omitempty"`
	Context            []ContextValue `json:"context"`
	Completeness       Completeness   `json:"completeness"`
	Deadline           DeadlineState  `json:"deadline"`
	StateVersion       string         `json:"state_version"`
}

type ApprovalSnapshot struct {
	Status   ApprovalStatus `json:"status"`
	Identity string         `json:"identity"`
}

type TaintSnapshot struct {
	Status   TaintStatus `json:"status"`
	Identity string      `json:"identity"`
}

type IdentitySnapshot struct {
	PlanIdentity             string        `json:"plan_identity"`
	SourceIdentity           string        `json:"source_identity"`
	PolicyDigest             string        `json:"policy_digest"`
	LockDigest               string        `json:"lock_digest"`
	AuthorityMode            AuthorityMode `json:"authority_mode"`
	ServerLabel              string        `json:"server_label"`
	ServerFingerprint        string        `json:"server_fingerprint"`
	ToolContractDigest       string        `json:"tool_contract_digest"`
	RepositoryIdentity       string        `json:"repository_identity"`
	ContextIdentity          string        `json:"context_identity"`
	Principal                string        `json:"principal"`
	CredentialLabels         []string      `json:"credential_labels"`
	StateVersion             string        `json:"state_version"`
	ApprovalIdentity         string        `json:"approval_identity"`
	TaintIdentity            string        `json:"taint_identity"`
	RepositoryEffectIdentity string        `json:"repository_effect_identity"`
}

type RepositoryEffectCandidate struct {
	Decision Decision   `json:"decision"`
	Reason   ReasonCode `json:"reason"`
	RuleIDs  []string   `json:"rule_ids"`
	Identity string     `json:"identity"`
	Complete bool       `json:"complete"`
}

type EvaluationInput struct {
	Request             Request                    `json:"request"`
	SourceIdentity      string                     `json:"source_identity"`
	ContextIdentity     string                     `json:"context_identity"`
	Principal           string                     `json:"principal"`
	CredentialLabels    []string                   `json:"credential_labels"`
	Approval            ApprovalSnapshot           `json:"approval"`
	Taint               TaintSnapshot              `json:"taint"`
	RepositoryEffect    *RepositoryEffectCandidate `json:"repository_effect,omitempty"`
	Lifecycle           LifecycleState             `json:"lifecycle"`
	CachePolicyVersion  string                     `json:"cache_policy_version"`
	ResampledIdentities IdentitySnapshot           `json:"resampled_identities"`
}

type ConditionState string

const (
	ConditionFalse         ConditionState = "false"
	ConditionTrue          ConditionState = "true"
	ConditionIndeterminate ConditionState = "indeterminate"
)

type PointerState string

const (
	PointerPresent        PointerState = "present"
	PointerNull           PointerState = "null"
	PointerMissing        PointerState = "missing"
	PointerWrongContainer PointerState = "wrong_container"
	PointerInvalidIndex   PointerState = "invalid_index"
)

type SelectorState string

const (
	SelectorMatched   SelectorState = "matched"
	SelectorUnmatched SelectorState = "unmatched"
)

type OperandSummary struct {
	PointerState PointerState `json:"pointer_state"`
	Kind         ValueKind    `json:"kind,omitempty"`
	ByteLength   int          `json:"byte_length,omitempty"`
	ItemCount    int          `json:"item_count,omitempty"`
}

type TraceEntry struct {
	RuleID             string         `json:"rule_id"`
	ToolID             string         `json:"tool_id,omitempty"`
	Selector           SelectorState  `json:"selector"`
	Condition          ConditionState `json:"condition"`
	CandidateDecision  Decision       `json:"candidate_decision,omitempty"`
	Reason             ReasonCode     `json:"reason,omitempty"`
	ActualProvenance   Provenance     `json:"actual_provenance,omitempty"`
	RequiredProvenance Provenance     `json:"required_provenance,omitempty"`
	Completeness       bool           `json:"complete"`
	Operand            OperandSummary `json:"operand"`
	Omitted            int            `json:"omitted,omitempty"`
}

type CandidateSource string

const (
	CandidateBaseline         CandidateSource = "baseline"
	CandidateRule             CandidateSource = "rule"
	CandidateRepositoryEffect CandidateSource = "repository_effect"
)

type Candidate struct {
	Source   CandidateSource `json:"source"`
	ID       string          `json:"id"`
	Decision Decision        `json:"decision"`
	Reason   ReasonCode      `json:"reason"`
}

type PhaseOutcome string

const (
	OutcomeDispatchEligible PhaseOutcome = "dispatch_eligible"
	OutcomeDispatchBlocked  PhaseOutcome = "dispatch_blocked"
	OutcomeDeliveryEligible PhaseOutcome = "delivery_eligible"
	OutcomeWithheld         PhaseOutcome = "withheld"
	OutcomeProgressEligible PhaseOutcome = "progress_eligible"
	OutcomeSuppressed       PhaseOutcome = "suppressed"
	OutcomeRecorded         PhaseOutcome = "recorded"
)

func (o PhaseOutcome) Valid() bool {
	switch o {
	case OutcomeDispatchEligible, OutcomeDispatchBlocked, OutcomeDeliveryEligible,
		OutcomeWithheld, OutcomeProgressEligible, OutcomeSuppressed, OutcomeRecorded:
		return true
	default:
		return false
	}
}

type CacheReason string

const (
	CacheEligible           CacheReason = "eligible"
	CachePolicyNever        CacheReason = "policy_never"
	CacheRuleNever          CacheReason = "rule_never"
	CacheIdentityMissing    CacheReason = "identity_missing"
	CacheIdentityDrift      CacheReason = "identity_drift"
	CacheContextUnresolved  CacheReason = "context_unresolved"
	CacheStateStale         CacheReason = "state_stale"
	CacheApprovalPending    CacheReason = "approval_pending"
	CacheEvidenceTainted    CacheReason = "evidence_tainted"
	CacheEvidenceIncomplete CacheReason = "evidence_incomplete"
	CacheFailureResult      CacheReason = "failure_result"
	CacheLifecycleInactive  CacheReason = "lifecycle_inactive"
)

func (r CacheReason) Valid() bool {
	switch r {
	case CacheEligible, CachePolicyNever, CacheRuleNever, CacheIdentityMissing,
		CacheIdentityDrift, CacheContextUnresolved, CacheStateStale,
		CacheApprovalPending, CacheEvidenceTainted, CacheEvidenceIncomplete,
		CacheFailureResult, CacheLifecycleInactive:
		return true
	default:
		return false
	}
}

type CacheResult struct {
	Eligible bool        `json:"eligible"`
	Reason   CacheReason `json:"reason"`
	Identity string      `json:"-"`
}

type Failure struct {
	Code    ReasonCode `json:"code"`
	Message string     `json:"message"`
}

type EvaluationResult struct {
	Decision                 Decision     `json:"decision"`
	Reason                   ReasonCode   `json:"reason_code"`
	ToolID                   string       `json:"tool_id,omitempty"`
	MatchedRuleIDs           []string     `json:"matched_rule_ids"`
	Candidates               []Candidate  `json:"candidates"`
	Trace                    []TraceEntry `json:"trace"`
	TraceComplete            bool         `json:"trace_complete"`
	TraceOmitted             int          `json:"trace_omitted"`
	Completeness             Completeness `json:"completeness"`
	PolicyDigest             string       `json:"policy_digest"`
	LockDigest               string       `json:"lock_digest"`
	PlanIdentity             string       `json:"plan_identity"`
	SourceIdentity           string       `json:"source_identity"`
	Cache                    CacheResult  `json:"cache"`
	RequiredApprovalIdentity string       `json:"required_approval_identity,omitempty"`
	PhaseOutcome             PhaseOutcome `json:"phase_outcome"`
	Failure                  *Failure     `json:"failure,omitempty"`
}

type RequestError struct {
	Code    ReasonCode
	Message string
}

func (e *RequestError) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Code) + ": " + e.Message
}
