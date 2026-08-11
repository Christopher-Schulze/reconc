package actionstate

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"reconc.dev/reconc/internal/actionapproval"
)

const (
	ApprovalRequestStateSchema   = "reconc.action-approval-request-state/v1"
	MaxApprovalRequestStateBytes = 4096
)

type approvalRequestStatePayload struct {
	Schema               string `json:"schema"`
	FormatVersion        string `json:"format_version"`
	RequestID            string `json:"request_id"`
	CallID               string `json:"call_id"`
	RequestIdentity      string `json:"request_identity"`
	IssuanceStateVersion string `json:"issuance_state_version"`
	ExpiresAt            string `json:"expires_at"`
}

type sealedApprovalRequestState struct {
	Schema               string `json:"schema"`
	FormatVersion        string `json:"format_version"`
	RequestID            string `json:"request_id"`
	CallID               string `json:"call_id"`
	RequestIdentity      string `json:"request_identity"`
	IssuanceStateVersion string `json:"issuance_state_version"`
	ExpiresAt            string `json:"expires_at"`
	Integrity            string `json:"integrity"`
}

func (s *Store) sealApprovalRequestState(
	request actionapproval.Request,
	issuanceVersion string,
) (string, error) {
	payload := approvalRequestStatePayload{
		Schema: ApprovalRequestStateSchema, FormatVersion: actionapproval.FormatVersion,
		RequestID: request.RequestID, CallID: request.CallID,
		RequestIdentity: request.RequestIdentity, IssuanceStateVersion: issuanceVersion,
		ExpiresAt: request.ExpiresAt,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", approvalContractError("encode request state payload", err)
	}
	sealed := sealedApprovalRequestState{
		Schema: payload.Schema, FormatVersion: payload.FormatVersion,
		RequestID: payload.RequestID, CallID: payload.CallID,
		RequestIdentity: payload.RequestIdentity, IssuanceStateVersion: payload.IssuanceStateVersion,
		ExpiresAt: payload.ExpiresAt, Integrity: s.key.Identity(DomainApproval, []byte("request-state"), body),
	}
	encoded, err := json.Marshal(sealed)
	if err != nil {
		return "", approvalContractError("encode sealed request state", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func (s *Store) openApprovalRequestState(token string) (sealedApprovalRequestState, error) {
	if token == "" || len(token) > base64.RawURLEncoding.EncodedLen(MaxApprovalRequestStateBytes) {
		return sealedApprovalRequestState{}, approvalContractError("request state is outside its byte bound", nil)
	}
	body, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(body) == 0 || len(body) > MaxApprovalRequestStateBytes ||
		base64.RawURLEncoding.EncodeToString(body) != token {
		return sealedApprovalRequestState{}, approvalContractError("request state is not canonical base64url", err)
	}
	var sealed sealedApprovalRequestState
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sealed); err != nil {
		return sealedApprovalRequestState{}, approvalContractError("decode request state", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return sealedApprovalRequestState{}, approvalContractError("decode request state", err)
	}
	canonical, err := json.Marshal(sealed)
	if err != nil {
		return sealedApprovalRequestState{}, approvalContractError("encode canonical request state", err)
	}
	if !bytes.Equal(body, canonical) {
		return sealedApprovalRequestState{}, approvalContractError("request state JSON is not canonical", nil)
	}
	if err := s.validateApprovalRequestState(sealed); err != nil {
		return sealedApprovalRequestState{}, err
	}
	return sealed, nil
}

func (s *Store) validateApprovalRequestState(sealed sealedApprovalRequestState) error {
	if sealed.Schema != ApprovalRequestStateSchema || sealed.FormatVersion != actionapproval.FormatVersion ||
		!validApprovalObjectID(sealed.RequestID, "apr_") || !validCallID(sealed.CallID) ||
		!identityUsesKey(sealed.RequestIdentity, s.key.ID()) ||
		!identityUsesKey(sealed.IssuanceStateVersion, s.key.ID()) || !identityUsesKey(sealed.Integrity, s.key.ID()) {
		return approvalContractError("request state binding is invalid", nil)
	}
	parsed, err := time.Parse(time.RFC3339Nano, sealed.ExpiresAt)
	if err != nil || parsed.IsZero() || parsed.UTC().Format(time.RFC3339Nano) != sealed.ExpiresAt {
		return approvalContractError("request state expiry is invalid", err)
	}
	payload := approvalRequestStatePayload{
		Schema: sealed.Schema, FormatVersion: sealed.FormatVersion,
		RequestID: sealed.RequestID, CallID: sealed.CallID,
		RequestIdentity: sealed.RequestIdentity, IssuanceStateVersion: sealed.IssuanceStateVersion,
		ExpiresAt: sealed.ExpiresAt,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return approvalContractError("encode request state binding", err)
	}
	want := s.key.Identity(DomainApproval, []byte("request-state"), body)
	if !constantIdentityEqual(sealed.Integrity, want) {
		return approvalContractError("request state integrity is invalid", nil)
	}
	return nil
}

func (s sealedApprovalRequestState) matches(record ApprovalRecord) bool {
	return s.RequestID == record.Request.RequestID && s.CallID == record.Request.CallID &&
		s.RequestIdentity == record.Request.RequestIdentity && s.ExpiresAt == record.Request.ExpiresAt
}

func validApprovalObjectID(value, prefix string) bool {
	if len(value) != len(prefix)+26 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, character := range value[len(prefix):] {
		if character < 'a' || character > 'z' {
			if character < '2' || character > '7' {
				return false
			}
		}
	}
	return true
}
