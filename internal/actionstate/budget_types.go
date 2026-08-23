package actionstate

import (
	"errors"
	"fmt"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

const (
	StateSchema            = "reconc.action-state/v1"
	StateFormatVersion     = "1"
	TransactionSchema      = "reconc.action-state-transaction/v1"
	MaxStateBytes          = 16 << 20
	MaxStateTransaction    = MaxStateBytes + 64<<10
	MaxBudgetRecords       = 65536
	MaxReservations        = 65536
	MaxTerminalCallRecords = 65536
	MaxApprovalRecords     = 65536
	MaxPendingApprovals    = 4
)

type ClockSnapshot struct {
	Time   time.Time
	Source string
}

type TrustedClock interface {
	Snapshot() (ClockSnapshot, error)
}

type SystemClock struct{}

func (SystemClock) Snapshot() (ClockSnapshot, error) {
	return ClockSnapshot{Time: time.Now().UTC(), Source: "system-wall-v1"}, nil
}

type ReservationStatus string

const (
	ReservationReserved      ReservationStatus = "reserved"
	ReservationDispatched    ReservationStatus = "dispatched"
	ReservationIndeterminate ReservationStatus = "indeterminate"
)

func (s ReservationStatus) Valid() bool {
	return s == ReservationReserved || s == ReservationDispatched || s == ReservationIndeterminate
}

type TerminalOutcome string

const (
	OutcomeReleased               TerminalOutcome = "released_before_dispatch"
	OutcomeBlocked                TerminalOutcome = "blocked_before_dispatch"
	OutcomeSucceeded              TerminalOutcome = "succeeded"
	OutcomeFailed                 TerminalOutcome = "failed"
	OutcomeIndeterminateCommitted TerminalOutcome = "indeterminate_committed"
)

func (o TerminalOutcome) Valid() bool {
	return o == OutcomeReleased || o == OutcomeBlocked || o == OutcomeSucceeded || o == OutcomeFailed ||
		o == OutcomeIndeterminateCommitted
}

type BudgetRecord struct {
	BudgetID          string                    `json:"budget_id"`
	ScopeIdentity     string                    `json:"scope_identity"`
	LineageIdentity   string                    `json:"lineage_identity"`
	Scope             action.BudgetScope        `json:"scope"`
	Reset             action.BudgetReset        `json:"reset"`
	WindowSeconds     uint32                    `json:"window_seconds,omitempty"`
	Limits            action.BudgetLimits       `json:"limits"`
	Consumed          action.BudgetUsage        `json:"consumed"`
	Generation        action.BudgetGeneration   `json:"generation"`
	GenerationHistory []action.BudgetGeneration `json:"generation_history"`
}

type ReservationCharge struct {
	BudgetID           string                  `json:"budget_id"`
	ScopeIdentity      string                  `json:"scope_identity"`
	LineageIdentity    string                  `json:"lineage_identity"`
	Generation         action.BudgetGeneration `json:"generation"`
	Reserved           action.BudgetUsage      `json:"reserved"`
	DispatchCommitted  bool                    `json:"dispatch_committed"`
	CommittedApprovals uint64                  `json:"committed_approvals"`
}

type Reservation struct {
	Identity         string              `json:"identity"`
	CallID           string              `json:"call_id"`
	OwnerID          string              `json:"owner_id"`
	RequestIdentity  string              `json:"request_identity"`
	ContextIdentity  string              `json:"context_identity"`
	ExecutableDigest string              `json:"executable_digest"`
	Status           ReservationStatus   `json:"status"`
	CreatedAtUnix    int64               `json:"created_at_unix"`
	UpdatedAtUnix    int64               `json:"updated_at_unix"`
	Charges          []ReservationCharge `json:"charges"`
}

type TerminalCall struct {
	CallID              string          `json:"call_id"`
	ReservationIdentity string          `json:"reservation_identity"`
	Outcome             TerminalOutcome `json:"outcome"`
	CompletedAtUnix     int64           `json:"completed_at_unix"`
}

type ApprovalRecord struct {
	Request             actionapproval.Request  `json:"request"`
	Status              actionapproval.Status   `json:"status"`
	ReservationIdentity string                  `json:"reservation_identity"`
	NonceIdentity       string                  `json:"nonce_identity"`
	RegistryIdentity    string                  `json:"registry_identity,omitempty"`
	AuthorityKeyID      string                  `json:"authority_key_id,omitempty"`
	ReceiptID           string                  `json:"receipt_id,omitempty"`
	ReceiptIdentity     string                  `json:"receipt_identity,omitempty"`
	ReceiptSignedAt     string                  `json:"receipt_signed_at,omitempty"`
	ReceiptDecision     actionapproval.Decision `json:"receipt_decision,omitempty"`
	ReceiptSignature    string                  `json:"receipt_signature,omitempty"`
	UpdatedAtUnix       int64                   `json:"updated_at_unix"`
}

type State struct {
	Schema               string           `json:"schema"`
	FormatVersion        string           `json:"format_version"`
	KeyID                string           `json:"key_id"`
	RepositoryIdentity   string           `json:"repository_identity"`
	Revision             uint64           `json:"revision"`
	ClockSource          string           `json:"clock_source"`
	LastObservedUnixNano int64            `json:"last_observed_unix_nano"`
	Budgets              []BudgetRecord   `json:"budgets"`
	Reservations         []Reservation    `json:"reservations"`
	TerminalCalls        []TerminalCall   `json:"terminal_calls"`
	Approvals            []ApprovalRecord `json:"approvals"`
	Digest               string           `json:"digest"`
}

type statePayload struct {
	Schema               string           `json:"schema"`
	FormatVersion        string           `json:"format_version"`
	KeyID                string           `json:"key_id"`
	RepositoryIdentity   string           `json:"repository_identity"`
	Revision             uint64           `json:"revision"`
	ClockSource          string           `json:"clock_source"`
	LastObservedUnixNano int64            `json:"last_observed_unix_nano"`
	Budgets              []BudgetRecord   `json:"budgets"`
	Reservations         []Reservation    `json:"reservations"`
	TerminalCalls        []TerminalCall   `json:"terminal_calls"`
	Approvals            []ApprovalRecord `json:"approvals"`
}

type stateTransaction struct {
	Schema          string `json:"schema"`
	FormatVersion   string `json:"format_version"`
	BeforePersisted bool   `json:"before_persisted"`
	BeforeRevision  uint64 `json:"before_revision"`
	BeforeDigest    string `json:"before_digest"`
	After           State  `json:"after"`
	Digest          string `json:"digest"`
}

type transactionPayload struct {
	Schema          string `json:"schema"`
	FormatVersion   string `json:"format_version"`
	BeforePersisted bool   `json:"before_persisted"`
	BeforeRevision  uint64 `json:"before_revision"`
	BeforeDigest    string `json:"before_digest"`
	After           State  `json:"after"`
}

type ReserveRequest struct {
	Plan      *action.CompiledPlan
	Request   action.Request
	Context   BoundContext
	Authority PolicyAuthority
	Server    ObservedServer
}

type ReserveResult struct {
	Snapshot    action.BudgetSnapshot `json:"snapshot"`
	Reservation *Reservation          `json:"reservation,omitempty"`
}

type StateStatus struct {
	StateVersion       string               `json:"state_version"`
	Revision           uint64               `json:"revision"`
	KeyID              string               `json:"key_id"`
	RepositoryIdentity string               `json:"repository_identity"`
	ClockSource        string               `json:"clock_source"`
	Budgets            []BudgetStatus       `json:"budgets"`
	Reservations       []ReservationView    `json:"reservations"`
	LiveReservations   int                  `json:"live_reservations"`
	Indeterminate      int                  `json:"indeterminate_reservations"`
	TerminalCallCount  int                  `json:"terminal_call_count"`
	ApprovalRecords    []ApprovalRecordView `json:"approval_records"`
	PendingApprovals   int                  `json:"pending_approvals"`
	Capacity           StateCapacity        `json:"capacity"`
	Remediations       []StateRemediation   `json:"remediations"`
	Provenance         []AuthorityBinding   `json:"provenance"`
	Complete           bool                 `json:"complete"`
}

type StateCapacity struct {
	StateBytes              int `json:"state_bytes"`
	StateBytesMaximum       int `json:"state_bytes_maximum"`
	BudgetRecords           int `json:"budget_records"`
	BudgetRecordsMaximum    int `json:"budget_records_maximum"`
	Reservations            int `json:"reservations"`
	ReservationsMaximum     int `json:"reservations_maximum"`
	TerminalCalls           int `json:"terminal_calls"`
	TerminalCallsMaximum    int `json:"terminal_calls_maximum"`
	ApprovalRecords         int `json:"approval_records"`
	ApprovalRecordsMaximum  int `json:"approval_records_maximum"`
	PendingApprovalsMaximum int `json:"pending_approvals_maximum"`
	GenerationHistoryMax    int `json:"generation_history_maximum_per_budget"`
}

type StateRemediation string

const (
	RemediationReleaseOrDispatch      StateRemediation = "release_before_dispatch_or_mark_dispatched"
	RemediationSettleOrMarkUnknown    StateRemediation = "settle_or_mark_indeterminate"
	RemediationReconcileIndeterminate StateRemediation = "operator_reconcile_indeterminate"
)

type ReservationView struct {
	Identity      string            `json:"identity"`
	CallID        string            `json:"call_id"`
	Status        ReservationStatus `json:"status"`
	CreatedAtUnix int64             `json:"created_at_unix"`
	UpdatedAtUnix int64             `json:"updated_at_unix"`
	BudgetIDs     []string          `json:"budget_ids"`
	Remediation   StateRemediation  `json:"remediation"`
}

type BudgetStatus struct {
	BudgetID                  string                    `json:"budget_id"`
	ScopeIdentity             string                    `json:"scope_identity"`
	LineageIdentity           string                    `json:"lineage_identity"`
	Scope                     action.BudgetScope        `json:"scope"`
	Reset                     action.BudgetReset        `json:"reset"`
	WindowSeconds             uint32                    `json:"window_seconds,omitempty"`
	Limits                    action.BudgetLimits       `json:"limits"`
	Consumed                  action.BudgetUsage        `json:"consumed"`
	Reserved                  action.BudgetUsage        `json:"reserved"`
	Generation                action.BudgetGeneration   `json:"generation"`
	GenerationHistory         []action.BudgetGeneration `json:"generation_history"`
	LiveReservations          int                       `json:"live_reservations"`
	IndeterminateReservations int                       `json:"indeterminate_reservations"`
}

type ApprovalRecordView struct {
	RequestID       string                `json:"request_id"`
	CallID          string                `json:"call_id"`
	Status          actionapproval.Status `json:"status"`
	AuthorityPolicy string                `json:"authority_policy"`
	AuthorityKeyID  string                `json:"authority_key_id,omitempty"`
	ReceiptID       string                `json:"receipt_id,omitempty"`
	ReceiptSignedAt string                `json:"receipt_signed_at,omitempty"`
	IssuedAt        string                `json:"issued_at"`
	ExpiresAt       string                `json:"expires_at"`
	UpdatedAtUnix   int64                 `json:"updated_at_unix"`
}

type StateError struct {
	Code    action.ReasonCode
	Message string
	Cause   error
}

var ErrStateVersionChanged = errors.New("action state version changed")

func (e *StateError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause == nil {
		return string(e.Code) + ": " + e.Message
	}
	return string(e.Code) + ": " + e.Message + ": " + e.Cause.Error()
}

func (e *StateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func stateError(code action.ReasonCode, message string, cause error) error {
	if !code.Valid() {
		return fmt.Errorf("invalid action state error code %q", code)
	}
	return &StateError{Code: code, Message: message, Cause: cause}
}
