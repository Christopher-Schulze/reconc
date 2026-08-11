// Package actionledger owns the privacy-bounded, tamper-evident Action Plane
// decision ledger. Its domain types cannot carry raw tool arguments, results,
// headers, credentials, environment values, prompts, stderr, or MCP metadata.
package actionledger

import (
	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

const (
	Schema            = "reconc.action-ledger-event/v1"
	FormatVersion     = "1"
	ChainVersion      = "action-ledger-chain-v1"
	MaxRecordBytes    = 64 << 10
	MaxLiveBytes      = 4 << 20
	MaxArchives       = 2
	MaxRuleIDs        = 256
	MaxSelectedFields = 256
	MaxBudgetIDs      = 1024
)

type EventType string

const (
	EventRequestAccepted    EventType = "request_accepted"
	EventPreDecision        EventType = "pre_decision"
	EventApprovalTransition EventType = "approval_transition"
	EventBudgetTransition   EventType = "budget_transition"
	EventDownstreamDispatch EventType = "downstream_dispatch"
	EventDownstreamOutcome  EventType = "downstream_outcome"
	EventResultInspection   EventType = "result_inspection"
	EventFinalDelivery      EventType = "final_delivery"
	EventTerminalFailure    EventType = "terminal_failure"
)

func (e EventType) Valid() bool {
	switch e {
	case EventRequestAccepted, EventPreDecision, EventApprovalTransition,
		EventBudgetTransition, EventDownstreamDispatch, EventDownstreamOutcome,
		EventResultInspection, EventFinalDelivery, EventTerminalFailure:
		return true
	default:
		return false
	}
}

type ToolIdentity struct {
	Mode  action.LedgerToolIdentity `json:"mode"`
	Value string                    `json:"value"`
}

type CallBinding struct {
	CallID             string            `json:"call_id"`
	RequestIdentity    string            `json:"request_identity,omitempty"`
	RepositoryIdentity string            `json:"repository_identity"`
	PolicyDigest       string            `json:"policy_digest"`
	LockDigest         string            `json:"lock_digest"`
	ServerLabel        string            `json:"server_label"`
	ServerFingerprint  string            `json:"server_fingerprint"`
	Tool               ToolIdentity      `json:"tool_identity"`
	ToolContractDigest string            `json:"tool_contract_digest"`
	Principal          string            `json:"principal"`
	CredentialLabels   []string          `json:"credential_labels"`
	RunIdentity        string            `json:"run_identity,omitempty"`
	SessionIdentity    string            `json:"session_identity,omitempty"`
	ContextIdentity    string            `json:"context_identity"`
	ContextProvenance  action.Provenance `json:"context_provenance"`
}

type DecisionBinding struct {
	Phase        action.Phase        `json:"phase"`
	Decision     action.Decision     `json:"decision,omitempty"`
	Reason       action.ReasonCode   `json:"reason_code,omitempty"`
	RuleIDs      []string            `json:"rule_ids"`
	Completeness action.Completeness `json:"completeness"`
}

type SelectedFieldEvidence struct {
	DeclarationIndex uint16              `json:"declaration_index"`
	Source           action.ValueSource  `json:"source"`
	PointerIdentity  string              `json:"pointer_identity,omitempty"`
	State            action.PointerState `json:"state"`
	Kind             action.ValueKind    `json:"kind,omitempty"`
	ValueIdentity    string              `json:"value_identity,omitempty"`
	ByteLength       uint64              `json:"byte_length"`
	ItemCount        uint32              `json:"item_count"`
	Complete         bool                `json:"complete"`
}

type RequestAccepted struct {
	ArgumentBytes uint64 `json:"argument_bytes"`
	ArgumentItems uint32 `json:"argument_items"`
}

type PreDecision struct {
	Outcome action.PhaseOutcome `json:"outcome"`
	Cached  bool                `json:"cached"`
}

type ApprovalTransition struct {
	RequestID         string                `json:"request_id"`
	Status            actionapproval.Status `json:"status"`
	AuthorityPolicyID string                `json:"authority_policy_id"`
	AuthorityKeyID    string                `json:"authority_key_id,omitempty"`
	ReceiptID         string                `json:"receipt_id,omitempty"`
	ReceiptIdentity   string                `json:"receipt_identity,omitempty"`
}

type BudgetTransitionKind string

const (
	BudgetReserved      BudgetTransitionKind = "reserved"
	BudgetReleased      BudgetTransitionKind = "released"
	BudgetDispatched    BudgetTransitionKind = "dispatch_committed"
	BudgetSettled       BudgetTransitionKind = "settled"
	BudgetIndeterminate BudgetTransitionKind = "indeterminate"
	BudgetDenied        BudgetTransitionKind = "denied"
)

func (k BudgetTransitionKind) Valid() bool {
	switch k {
	case BudgetReserved, BudgetReleased, BudgetDispatched, BudgetSettled,
		BudgetIndeterminate, BudgetDenied:
		return true
	default:
		return false
	}
}

type BudgetDelta struct {
	CallCount     int64 `json:"call_count"`
	DeniedCount   int64 `json:"denied_count"`
	ApprovalCount int64 `json:"approval_count"`
	ArgumentBytes int64 `json:"argument_bytes"`
	ResultBytes   int64 `json:"result_bytes"`
	CostUnits     int64 `json:"cost_units"`
	Concurrent    int64 `json:"concurrent"`
	RateWindow    int64 `json:"rate_window"`
}

type BudgetTransition struct {
	Kind                BudgetTransitionKind `json:"kind"`
	ReservationIdentity string               `json:"reservation_identity"`
	StateVersion        string               `json:"state_version"`
	BudgetIDs           []string             `json:"budget_ids"`
	ReservedDelta       BudgetDelta          `json:"reserved_delta"`
	ConsumedDelta       BudgetDelta          `json:"consumed_delta"`
}

type DownstreamDispatch struct {
	ReservationIdentity string `json:"reservation_identity"`
}

type DownstreamStatus string

const (
	DownstreamSucceeded DownstreamStatus = "succeeded"
	DownstreamFailed    DownstreamStatus = "failed"
	DownstreamUnknown   DownstreamStatus = "unknown"
)

func (s DownstreamStatus) Valid() bool {
	return s == DownstreamSucceeded || s == DownstreamFailed || s == DownstreamUnknown
}

type DownstreamOutcome struct {
	Status DownstreamStatus `json:"status"`
}

type ResultInspection struct {
	Status             action.InspectionStatus       `json:"status"`
	Categories         []action.DetectorCategory     `json:"categories"`
	SchemaStatus       action.InspectionSchemaStatus `json:"schema_status"`
	ScannedBytes       uint64                        `json:"scanned_bytes"`
	ScannedItems       uint32                        `json:"scanned_items"`
	UnsupportedContent uint32                        `json:"unsupported_content"`
}

type DeliveryStatus string

const (
	DeliveryForwarded  DeliveryStatus = "forwarded"
	DeliveryWithheld   DeliveryStatus = "withheld"
	DeliverySuppressed DeliveryStatus = "suppressed"
)

func (s DeliveryStatus) Valid() bool {
	return s == DeliveryForwarded || s == DeliveryWithheld || s == DeliverySuppressed
}

type FinalDelivery struct {
	Status     DeliveryStatus `json:"status"`
	ByteLength uint64         `json:"byte_length"`
	ItemCount  uint32         `json:"item_count"`
}

type TerminalFailure struct {
	Lifecycle     action.LifecycleState `json:"lifecycle"`
	DispatchKnown bool                  `json:"dispatch_known"`
	DeliveryKnown bool                  `json:"delivery_known"`
}

type Record struct {
	Schema          string                  `json:"schema"`
	FormatVersion   string                  `json:"format_version"`
	ChainVersion    string                  `json:"chain_version"`
	Sequence        uint64                  `json:"sequence"`
	PreviousDigest  string                  `json:"previous_digest,omitempty"`
	Digest          string                  `json:"digest"`
	Timestamp       string                  `json:"timestamp"`
	LatencyMicros   uint64                  `json:"latency_micros"`
	Event           EventType               `json:"event"`
	Call            CallBinding             `json:"call"`
	Decision        DecisionBinding         `json:"decision"`
	SelectedFields  []SelectedFieldEvidence `json:"selected_fields"`
	RequestAccepted *RequestAccepted        `json:"request_accepted,omitempty"`
	PreDecision     *PreDecision            `json:"pre_decision,omitempty"`
	Approval        *ApprovalTransition     `json:"approval_transition,omitempty"`
	Budget          *BudgetTransition       `json:"budget_transition,omitempty"`
	Dispatch        *DownstreamDispatch     `json:"downstream_dispatch,omitempty"`
	Downstream      *DownstreamOutcome      `json:"downstream_outcome,omitempty"`
	Inspection      *ResultInspection       `json:"result_inspection,omitempty"`
	Delivery        *FinalDelivery          `json:"final_delivery,omitempty"`
	Failure         *TerminalFailure        `json:"terminal_failure,omitempty"`
}
