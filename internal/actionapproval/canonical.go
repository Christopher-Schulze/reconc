package actionapproval

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"reconc.dev/reconc/internal/action"
)

type unsignedReceipt struct {
	Schema         string   `json:"schema"`
	FormatVersion  string   `json:"format_version"`
	Request        Request  `json:"request"`
	Decision       Decision `json:"decision"`
	AuthorityKeyID string   `json:"authority_key_id"`
	ReceiptID      string   `json:"receipt_id"`
	SignedAt       string   `json:"signed_at"`
}

func EncodeRequest(request Request) ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return canonicalJSON(request)
}

func DecodeRequest(body []byte) (Request, error) {
	var request Request
	if err := decodeCanonicalObject(body, &request); err != nil {
		return Request{}, approvalError(action.ReasonApprovalInvalid, "decode canonical approval request", err)
	}
	if err := request.Validate(); err != nil {
		return Request{}, approvalError(action.ReasonApprovalInvalid, "validate approval request", err)
	}
	return request, nil
}

func EncodeReceipt(receipt Receipt) ([]byte, error) {
	if err := receipt.Validate(); err != nil {
		return nil, err
	}
	return canonicalJSON(receipt)
}

func DecodeReceipt(body []byte) (Receipt, error) {
	var receipt Receipt
	if err := decodeCanonicalObject(body, &receipt); err != nil {
		return Receipt{}, approvalError(action.ReasonApprovalInvalid, "decode canonical approval receipt", err)
	}
	if err := receipt.Validate(); err != nil {
		return Receipt{}, approvalError(action.ReasonApprovalInvalid, "validate approval receipt", err)
	}
	return receipt, nil
}

func (r Receipt) Validate() error {
	if r.Schema != ReceiptSchema || r.FormatVersion != FormatVersion || !r.Decision.Valid() ||
		!action.SafeLabel(r.AuthorityKeyID) || !validPrefixedID(r.ReceiptID, "arc_") {
		return fmt.Errorf("approval receipt metadata is invalid")
	}
	if err := r.Request.Validate(); err != nil {
		return err
	}
	if err := validateReceiptSignedAt(r.Request, r.SignedAt); err != nil {
		return err
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(r.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		base64.RawURLEncoding.EncodeToString(signature) != r.Signature {
		return fmt.Errorf("approval receipt signature is invalid or non-canonical")
	}
	return nil
}

func SignReceipt(
	request Request,
	authorityKeyID string,
	privateKey ed25519.PrivateKey,
	decision Decision,
	signedAt time.Time,
	entropy io.Reader,
) (Receipt, []byte, error) {
	if err := request.Validate(); err != nil {
		return Receipt{}, nil, err
	}
	if !action.SafeLabel(authorityKeyID) || !decision.Valid() || len(privateKey) != ed25519.PrivateKeySize || entropy == nil {
		return Receipt{}, nil, fmt.Errorf("approval signing input is invalid")
	}
	receiptID, err := randomPrefixedID("arc_", entropy)
	if err != nil {
		return Receipt{}, nil, err
	}
	receipt := Receipt{
		Schema: ReceiptSchema, FormatVersion: FormatVersion, Request: cloneRequest(request),
		Decision: decision, AuthorityKeyID: authorityKeyID, ReceiptID: receiptID,
		SignedAt: signedAt.UTC().Format(time.RFC3339Nano),
	}
	signingBytes, err := ReceiptSigningBytes(receipt)
	if err != nil {
		return Receipt{}, nil, err
	}
	receipt.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, signingBytes))
	body, err := EncodeReceipt(receipt)
	if err != nil {
		return Receipt{}, nil, err
	}
	return receipt, body, nil
}

func ReceiptSigningBytes(receipt Receipt) ([]byte, error) {
	if receipt.Schema != ReceiptSchema || receipt.FormatVersion != FormatVersion ||
		!receipt.Decision.Valid() || !action.SafeLabel(receipt.AuthorityKeyID) ||
		!validPrefixedID(receipt.ReceiptID, "arc_") {
		return nil, fmt.Errorf("unsigned approval receipt metadata is invalid")
	}
	if err := receipt.Request.Validate(); err != nil {
		return nil, err
	}
	if err := validateReceiptSignedAt(receipt.Request, receipt.SignedAt); err != nil {
		return nil, err
	}
	body, err := canonicalJSON(unsignedReceipt{
		Schema: receipt.Schema, FormatVersion: receipt.FormatVersion, Request: receipt.Request,
		Decision: receipt.Decision, AuthorityKeyID: receipt.AuthorityKeyID, ReceiptID: receipt.ReceiptID,
		SignedAt: receipt.SignedAt,
	})
	if err != nil {
		return nil, err
	}
	prefix := append([]byte(SigningContext), 0)
	return append(prefix, body...), nil
}

func VerifyReceipt(registry *CompiledRegistry, expected Request, body []byte, now time.Time) (Verification, error) {
	verified, err := VerifySignedReceipt(registry, expected, body, now)
	if err != nil {
		return Verification{}, err
	}
	if verified.Receipt.Decision != DecisionApprove {
		return Verification{}, approvalError(action.ReasonApprovalRejected, "approval authority rejected the request", nil)
	}
	return verified, nil
}

// VerifySignedReceipt verifies canonical encoding, the exact request binding,
// authority policy, signature, activation interval, and trusted time. A valid
// signed rejection is returned as a verified decision so persistence owners can
// record its provenance atomically; it never becomes an approval.
func VerifySignedReceipt(registry *CompiledRegistry, expected Request, body []byte, now time.Time) (Verification, error) {
	if registry == nil {
		return Verification{}, approvalError(action.ReasonAuthorityUnavailable, "approval authority registry is unavailable", nil)
	}
	if err := expected.Validate(); err != nil {
		return Verification{}, approvalError(action.ReasonApprovalInvalid, "expected approval request is invalid", err)
	}
	receipt, err := DecodeReceipt(body)
	if err != nil {
		return Verification{}, err
	}
	equal, err := RequestsEqual(expected, receipt.Request)
	if err != nil || !equal {
		return Verification{}, approvalError(action.ReasonApprovalInvalid, "approval receipt does not bind the exact request", err)
	}
	issued, err := parseCanonicalTime(receipt.Request.IssuedAt)
	if err != nil {
		return Verification{}, approvalError(action.ReasonApprovalInvalid, "approval issuance is invalid", err)
	}
	expires, err := parseCanonicalTime(receipt.Request.ExpiresAt)
	if err != nil {
		return Verification{}, approvalError(action.ReasonApprovalInvalid, "approval expiry is invalid", err)
	}
	signedAt, err := parseCanonicalTime(receipt.SignedAt)
	if err != nil {
		return Verification{}, approvalError(action.ReasonApprovalInvalid, "approval signature time is invalid", err)
	}
	now = now.UTC()
	if now.IsZero() {
		return Verification{}, approvalError(action.ReasonAuthorityUnavailable, "trusted approval clock is unavailable", nil)
	}
	publicKey, err := registry.authorityKey(receipt.Request.AuthorityPolicyID, receipt.AuthorityKeyID, signedAt)
	if err != nil {
		return Verification{}, err
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(receipt.Signature)
	if err != nil {
		return Verification{}, approvalError(action.ReasonApprovalInvalid, "approval signature encoding is invalid", err)
	}
	signingBytes, err := ReceiptSigningBytes(receipt)
	if err != nil || !ed25519.Verify(publicKey, signingBytes, signature) {
		return Verification{}, approvalError(action.ReasonApprovalInvalid, "approval signature verification failed", err)
	}
	if issued.After(now.Add(MaximumApprovalFutureSkew)) {
		return Verification{}, approvalError(action.ReasonApprovalInvalid, "approval issuance is too far in the future", nil)
	}
	if signedAt.After(now.Add(MaximumApprovalFutureSkew)) {
		return Verification{}, approvalError(action.ReasonApprovalInvalid, "approval signature time is too far in the future", nil)
	}
	if !now.Before(expires) {
		return Verification{}, approvalError(action.ReasonApprovalExpired, "approval receipt expired", nil)
	}
	digest := sha256.Sum256(body)
	return Verification{Receipt: receipt, Identity: "sha256:" + hex.EncodeToString(digest[:])}, nil
}

func validateReceiptSignedAt(request Request, value string) error {
	signedAt, err := parseCanonicalTime(value)
	if err != nil {
		return fmt.Errorf("approval receipt signed_at is invalid: %w", err)
	}
	issuedAt, err := parseCanonicalTime(request.IssuedAt)
	if err != nil {
		return fmt.Errorf("approval request issued_at is invalid: %w", err)
	}
	expiresAt, err := parseCanonicalTime(request.ExpiresAt)
	if err != nil {
		return fmt.Errorf("approval request expires_at is invalid: %w", err)
	}
	if signedAt.Before(issuedAt) || !signedAt.Before(expiresAt) {
		return fmt.Errorf("approval receipt signed_at must be within the request validity interval")
	}
	return nil
}

// RequestsEqual compares two fully validated requests through their canonical
// bytes. It never ignores a field or treats a mutable state binding specially.
func RequestsEqual(left, right Request) (bool, error) {
	leftBody, err := EncodeRequest(left)
	if err != nil {
		return false, err
	}
	rightBody, err := EncodeRequest(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftBody, rightBody), nil
}

func canonicalJSON(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	parsed, err := action.ParseObjectJSON(body)
	if err != nil {
		return nil, err
	}
	return parsed.MarshalJSON()
}

func decodeCanonicalObject(body []byte, target any) error {
	if len(body) == 0 || len(body) > MaxApprovalObjectBytes {
		return fmt.Errorf("approval object must contain 1 to %d bytes", MaxApprovalObjectBytes)
	}
	parsed, err := action.ParseObjectJSON(body)
	if err != nil {
		return err
	}
	canonical, err := parsed.MarshalJSON()
	if err != nil {
		return err
	}
	if !bytes.Equal(body, canonical) {
		return fmt.Errorf("approval object is not canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("approval object contains a trailing value")
		}
		return err
	}
	normalized, err := canonicalJSON(target)
	if err != nil {
		return fmt.Errorf("re-encode approval object: %w", err)
	}
	if !bytes.Equal(body, normalized) {
		return fmt.Errorf("approval object field names or representations are not exact")
	}
	return nil
}

func randomPrefixedID(prefix string, entropy io.Reader) (string, error) {
	raw := make([]byte, 16)
	if _, err := io.ReadFull(entropy, raw); err != nil {
		return "", fmt.Errorf("read approval identity entropy: %w", err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	clear(raw)
	return prefix + string(bytes.ToLower([]byte(encoded))), nil
}

func validPrefixedID(value, prefix string) bool {
	if len(value) != len(prefix)+26 || !bytes.HasPrefix([]byte(value), []byte(prefix)) {
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

// ValidRequestID reports whether value is a canonical approval request ID.
func ValidRequestID(value string) bool {
	return validPrefixedID(value, "apr_")
}

// ValidReceiptID reports whether value is a canonical approval receipt ID.
func ValidReceiptID(value string) bool {
	return validPrefixedID(value, "arc_")
}

func validCallID(value string) bool {
	return validPrefixedID(value, "act_")
}

func lowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func parseCanonicalTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.IsZero() || value != parsed.UTC().Format(time.RFC3339Nano) {
		return time.Time{}, fmt.Errorf("timestamp must be canonical UTC RFC3339Nano")
	}
	return parsed, nil
}

func validateSafeLabels(values []string, maximum int, name string) error {
	if values == nil || len(values) > maximum || !sort.StringsAreSorted(values) {
		return fmt.Errorf("%s must be an explicit sorted array of at most %d values", name, maximum)
	}
	for index, value := range values {
		if !action.SafeLabel(value) || index > 0 && values[index-1] == value {
			return fmt.Errorf("%s contains an invalid or duplicate value", name)
		}
	}
	return nil
}

func cloneRequest(input Request) Request {
	out := input
	out.CredentialLabels = append([]string(nil), input.CredentialLabels...)
	out.SelectedArguments = append([]SelectedArgument(nil), input.SelectedArguments...)
	out.RuleIDs = append([]string(nil), input.RuleIDs...)
	return out
}
