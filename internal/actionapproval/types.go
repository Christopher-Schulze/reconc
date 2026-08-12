// Package actionapproval owns canonical, authority-signed, one-call approval
// requests and receipts. It performs no persistence or downstream IO.
package actionapproval

import (
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"reconc.dev/reconc/internal/action"
)

const (
	RequestSchema              = "reconc.action-approval-request/v1"
	ReceiptSchema              = "reconc.action-approval-receipt/v1"
	RegistrySchema             = "reconc.action-approval-authorities/v1"
	FormatVersion              = "1"
	MaxApprovalObjectBytes     = 64 << 10
	MaxAuthorityRegistryBytes  = 1 << 20
	MaxAuthorities             = 256
	MaxAuthorityPolicies       = 256
	MaxSelectedArguments       = 256
	MaxApprovalRuleIDs         = 256
	MaximumApprovalTTL         = 120 * time.Second
	MaximumApprovalFutureSkew  = 30 * time.Second
	DefaultApprovalWaitTimeout = 120 * time.Second
	SigningContext             = "reconc/action-approval-receipt/v1"
)

type Decision string

const (
	DecisionApprove Decision = "approve"
	DecisionReject  Decision = "reject"
)

func (d Decision) Valid() bool {
	return d == DecisionApprove || d == DecisionReject
}

type Status string

const (
	StatusPending     Status = "pending"
	StatusApproved    Status = "approved"
	StatusRejected    Status = "rejected"
	StatusExpired     Status = "expired"
	StatusCancelled   Status = "cancelled"
	StatusUnavailable Status = "unavailable"
	StatusMalformed   Status = "malformed"
	StatusReplayed    Status = "replayed"
)

func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusApproved, StatusRejected, StatusExpired,
		StatusCancelled, StatusUnavailable, StatusMalformed, StatusReplayed:
		return true
	default:
		return false
	}
}

type SelectedArgument struct {
	Pointer    string              `json:"pointer"`
	State      action.PointerState `json:"state"`
	Kind       action.ValueKind    `json:"kind,omitempty"`
	ByteLength uint64              `json:"byte_length"`
	Identity   string              `json:"identity"`
}

type Request struct {
	Schema                   string             `json:"schema"`
	FormatVersion            string             `json:"format_version"`
	RequestID                string             `json:"request_id"`
	CallID                   string             `json:"call_id"`
	RequestIdentity          string             `json:"request_identity"`
	RequiredApprovalIdentity string             `json:"required_approval_identity"`
	PlanIdentity             string             `json:"plan_identity"`
	SourceIdentity           string             `json:"source_identity"`
	RepositoryIdentity       string             `json:"repository_identity"`
	StateVersion             string             `json:"state_version"`
	PolicyDigest             string             `json:"policy_digest"`
	LockDigest               string             `json:"lock_digest"`
	ExecutableDigest         string             `json:"executable_digest"`
	ServerLabel              string             `json:"server_label"`
	ServerFingerprint        string             `json:"server_fingerprint"`
	ToolID                   string             `json:"tool_id"`
	Tool                     string             `json:"tool"`
	ToolContractDigest       string             `json:"tool_contract_digest"`
	Phase                    action.Phase       `json:"phase"`
	Principal                string             `json:"principal"`
	ContextIdentity          string             `json:"context_identity"`
	CredentialLabels         []string           `json:"credential_labels"`
	TaintIdentity            string             `json:"taint_identity"`
	RepositoryEffectIdentity string             `json:"repository_effect_identity"`
	SelectedArguments        []SelectedArgument `json:"selected_arguments"`
	BudgetReservationID      string             `json:"budget_reservation_id"`
	ReasonCode               action.ReasonCode  `json:"reason_code"`
	RuleIDs                  []string           `json:"rule_ids"`
	AuthorityPolicyID        string             `json:"authority_policy_id"`
	IssuedAt                 string             `json:"issued_at"`
	ExpiresAt                string             `json:"expires_at"`
	Nonce                    string             `json:"nonce"`
}

type RequestInput struct {
	CallID                   string
	RequestIdentity          string
	RequiredApprovalIdentity string
	PlanIdentity             string
	SourceIdentity           string
	RepositoryIdentity       string
	StateVersion             string
	PolicyDigest             string
	LockDigest               string
	ExecutableDigest         string
	ServerLabel              string
	ServerFingerprint        string
	ToolID                   string
	Tool                     string
	ToolContractDigest       string
	Phase                    action.Phase
	Principal                string
	ContextIdentity          string
	CredentialLabels         []string
	TaintIdentity            string
	RepositoryEffectIdentity string
	SelectedArguments        []SelectedArgument
	BudgetReservationID      string
	ReasonCode               action.ReasonCode
	RuleIDs                  []string
	AuthorityPolicyID        string
	IssuedAt                 time.Time
	TTL                      time.Duration
}

type Receipt struct {
	Schema         string   `json:"schema"`
	FormatVersion  string   `json:"format_version"`
	Request        Request  `json:"request"`
	Decision       Decision `json:"decision"`
	AuthorityKeyID string   `json:"authority_key_id"`
	ReceiptID      string   `json:"receipt_id"`
	SignedAt       string   `json:"signed_at"`
	Signature      string   `json:"signature"`
}

type Verification struct {
	Receipt  Receipt
	Identity string
}

type ApprovalError struct {
	Code    action.ReasonCode
	Message string
	Cause   error
}

func (e *ApprovalError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause == nil {
		return string(e.Code) + ": " + e.Message
	}
	return string(e.Code) + ": " + e.Message + ": " + e.Cause.Error()
}

func (e *ApprovalError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func approvalError(code action.ReasonCode, message string, cause error) error {
	return &ApprovalError{Code: code, Message: message, Cause: cause}
}

func NewRequest(input RequestInput, entropy io.Reader) (Request, error) {
	if entropy == nil {
		return Request{}, fmt.Errorf("approval request entropy source is unavailable")
	}
	requestID, err := randomPrefixedID("apr_", entropy)
	if err != nil {
		return Request{}, err
	}
	nonce := make([]byte, 32)
	if _, err := io.ReadFull(entropy, nonce); err != nil {
		return Request{}, fmt.Errorf("read approval nonce entropy: %w", err)
	}
	issued := input.IssuedAt.UTC()
	request := Request{
		Schema: RequestSchema, FormatVersion: FormatVersion,
		RequestID: requestID, CallID: input.CallID,
		RequestIdentity: input.RequestIdentity, RequiredApprovalIdentity: input.RequiredApprovalIdentity,
		PlanIdentity: input.PlanIdentity, SourceIdentity: input.SourceIdentity,
		RepositoryIdentity: input.RepositoryIdentity, StateVersion: input.StateVersion,
		PolicyDigest: input.PolicyDigest, LockDigest: input.LockDigest,
		ExecutableDigest: input.ExecutableDigest, ServerLabel: input.ServerLabel,
		ServerFingerprint: input.ServerFingerprint, ToolID: input.ToolID, Tool: input.Tool,
		ToolContractDigest: input.ToolContractDigest, Phase: input.Phase,
		Principal:                input.Principal,
		ContextIdentity:          input.ContextIdentity,
		CredentialLabels:         append([]string{}, input.CredentialLabels...),
		TaintIdentity:            input.TaintIdentity,
		RepositoryEffectIdentity: input.RepositoryEffectIdentity,
		SelectedArguments:        append([]SelectedArgument{}, input.SelectedArguments...),
		BudgetReservationID:      input.BudgetReservationID, ReasonCode: input.ReasonCode,
		RuleIDs: append([]string{}, input.RuleIDs...), AuthorityPolicyID: input.AuthorityPolicyID,
		IssuedAt:  issued.Format(time.RFC3339Nano),
		ExpiresAt: issued.Add(input.TTL).UTC().Format(time.RFC3339Nano),
		Nonce:     base64.RawURLEncoding.EncodeToString(nonce),
	}
	clear(nonce)
	if err := request.Validate(); err != nil {
		return Request{}, err
	}
	return request, nil
}

func (r Request) Validate() error {
	if r.Schema != RequestSchema || r.FormatVersion != FormatVersion ||
		!validPrefixedID(r.RequestID, "apr_") || !validCallID(r.CallID) ||
		!action.ValidKeyedIdentity(r.RequestIdentity) ||
		!action.ValidSHA256Identity(r.RequiredApprovalIdentity) ||
		!action.ValidSHA256Identity(r.PlanIdentity) || !lowerHex(r.SourceIdentity, 64) ||
		!action.ValidKeyedIdentity(r.RepositoryIdentity) || !action.ValidKeyedIdentity(r.StateVersion) ||
		!lowerHex(r.PolicyDigest, 64) || !lowerHex(r.LockDigest, 64) ||
		!action.ValidSHA256Identity(r.ExecutableDigest) || !action.SafeLabel(r.ServerLabel) ||
		!action.ValidKeyedIdentity(r.ServerFingerprint) || !action.SafeLabel(r.ToolID) ||
		!validToolName(r.Tool) || !action.ValidSHA256Identity(r.ToolContractDigest) ||
		(r.Phase != action.PhasePreCall && r.Phase != action.PhasePostResult) ||
		!action.SafeLabel(r.Principal) || !action.ValidKeyedIdentity(r.ContextIdentity) ||
		!validOpaqueBinding(r.TaintIdentity) || !validOpaqueBinding(r.RepositoryEffectIdentity) ||
		!action.SafeLabel(r.AuthorityPolicyID) ||
		r.ReasonCode != action.ReasonApprovalRequired ||
		(r.BudgetReservationID != "absent" && !action.ValidKeyedIdentity(r.BudgetReservationID)) {
		return fmt.Errorf("approval request binding is invalid")
	}
	if err := validateSafeLabels(r.CredentialLabels, action.MaxCredentialLabels, "credential labels"); err != nil {
		return err
	}
	if err := validateSafeLabels(r.RuleIDs, MaxApprovalRuleIDs, "rule IDs"); err != nil {
		return err
	}
	if r.SelectedArguments == nil || len(r.SelectedArguments) > MaxSelectedArguments {
		return fmt.Errorf("selected approval arguments must be an explicit array of at most %d entries", MaxSelectedArguments)
	}
	for index, selected := range r.SelectedArguments {
		if _, err := action.CompilePointer(selected.Pointer); err != nil ||
			!validPointerState(selected.State) || !validSelectedKind(selected.State, selected.Kind) ||
			!action.ValidKeyedIdentity(selected.Identity) ||
			index > 0 && r.SelectedArguments[index-1].Pointer >= selected.Pointer {
			return fmt.Errorf("selected approval argument is invalid or unsorted")
		}
		if selected.State != action.PointerPresent && selected.State != action.PointerNull &&
			(selected.Kind != "" || selected.ByteLength != 0) {
			return fmt.Errorf("unavailable selected approval argument carries value metadata")
		}
		if (selected.State == action.PointerPresent || selected.State == action.PointerNull) &&
			selected.ByteLength == 0 {
			return fmt.Errorf("available selected approval argument lacks value metadata")
		}
	}
	issued, err := parseCanonicalTime(r.IssuedAt)
	if err != nil {
		return fmt.Errorf("approval issued_at is invalid: %w", err)
	}
	expires, err := parseCanonicalTime(r.ExpiresAt)
	if err != nil || !expires.After(issued) || expires.Sub(issued) > MaximumApprovalTTL {
		return fmt.Errorf("approval expiry must be after issuance and within %s", MaximumApprovalTTL)
	}
	nonce, err := base64.RawURLEncoding.Strict().DecodeString(r.Nonce)
	if err != nil || len(nonce) != 32 || base64.RawURLEncoding.EncodeToString(nonce) != r.Nonce {
		return fmt.Errorf("approval nonce must be canonical unpadded base64url for 32 bytes")
	}
	return nil
}

func validToolName(value string) bool {
	if value == "" || len(value) > action.MaxToolNameBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validOpaqueBinding(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return true
}

func validPointerState(value action.PointerState) bool {
	switch value {
	case action.PointerPresent, action.PointerNull, action.PointerMissing,
		action.PointerWrongContainer, action.PointerInvalidIndex:
		return true
	default:
		return false
	}
}

func validSelectedKind(state action.PointerState, kind action.ValueKind) bool {
	if state != action.PointerPresent && state != action.PointerNull {
		return kind == ""
	}
	switch kind {
	case action.ValueNull, action.ValueBool, action.ValueNumber, action.ValueString,
		action.ValueArray, action.ValueObject:
		return true
	default:
		return false
	}
}
