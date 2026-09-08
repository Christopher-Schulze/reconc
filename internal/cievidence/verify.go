package cievidence

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"reconc.dev/reconc/internal/action"
)

const signingContext = "reconc.ci-evidence-signature/v1\x00"

// Sign encodes a statement supplied by a trusted CI integration. It does not
// query a provider or establish that the supplied outcomes actually occurred.
func Sign(statement Statement, authorityKeyID string, key ed25519.PrivateKey) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("CI signing key is invalid")
	}
	envelope := Envelope{Schema: EnvelopeSchema, FormatVersion: FormatVersion, Statement: statement, AuthorityKeyID: authorityKeyID}
	body, err := signingBytes(envelope)
	if err != nil {
		return nil, err
	}
	envelope.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, body))
	return canonicalJSON(envelope)
}

// Verify authenticates one bounded envelope against independently supplied
// requirements and the exact candidate. No self-reported claim is an input.
func Verify(body []byte, requirement Requirement, candidate Candidate, now time.Time) (Verification, error) {
	if err := validateRequirement(requirement); err != nil {
		return Verification{}, err
	}
	if err := validateCandidate(candidate); err != nil {
		return Verification{}, err
	}
	envelope, err := decode(body)
	if err != nil {
		return Verification{}, err
	}
	if err := authenticate(envelope, requirement); err != nil {
		return Verification{}, err
	}
	if err := verifyBinding(envelope.Statement, requirement, candidate, now); err != nil {
		return Verification{}, err
	}
	digest := sha256.Sum256(body)
	return Verification{Statement: envelope.Statement, Identity: "sha256:" + hex.EncodeToString(digest[:])}, nil
}

func authenticate(envelope Envelope, requirement Requirement) error {
	if envelope.AuthorityKeyID != requirement.AuthorityKeyID {
		return fmt.Errorf("CI evidence authority does not match the trusted requirement")
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.RawURLEncoding.EncodeToString(signature) != envelope.Signature {
		return fmt.Errorf("CI evidence signature encoding is invalid")
	}
	body, err := signingBytes(envelope)
	if err != nil {
		return err
	}
	if !ed25519.Verify(requirement.PublicKey, body, signature) {
		return fmt.Errorf("CI evidence signature verification failed")
	}
	return nil
}

func verifyBinding(statement Statement, requirement Requirement, candidate Candidate, now time.Time) error {
	if statement.Repository != requirement.Repository || statement.Candidate != candidate {
		return fmt.Errorf("CI evidence does not bind the required repository and exact candidate")
	}
	issued, expires := time.Unix(statement.IssuedAt, 0), time.Unix(statement.ExpiresAt, 0)
	if now.IsZero() || now.Before(issued) || !now.Before(expires) || now.Sub(issued) > requirement.MaxAge {
		return fmt.Errorf("CI evidence is expired, too old, or issued in the future")
	}
	for _, id := range requirement.RequiredChecks {
		index, found := slices.BinarySearchFunc(statement.Checks, id, func(check Check, expected string) int {
			return strings.Compare(check.ID, expected)
		})
		if !found {
			return fmt.Errorf("required CI check %q is missing", id)
		}
		if statement.Checks[index].Outcome != OutcomeSuccess {
			return fmt.Errorf("required CI check %q has no successful completed result", id)
		}
	}
	return nil
}

func signingBytes(envelope Envelope) ([]byte, error) {
	if envelope.Schema != EnvelopeSchema || envelope.FormatVersion != FormatVersion {
		return nil, fmt.Errorf("CI envelope schema or format version is invalid")
	}
	if !action.SafeLabel(envelope.AuthorityKeyID) {
		return nil, fmt.Errorf("CI authority key ID is invalid")
	}
	if err := validateStatement(envelope.Statement); err != nil {
		return nil, err
	}
	unsigned := struct {
		Schema         string    `json:"schema"`
		FormatVersion  string    `json:"format_version"`
		AuthorityKeyID string    `json:"authority_key_id"`
		Statement      Statement `json:"statement"`
	}{envelope.Schema, envelope.FormatVersion, envelope.AuthorityKeyID, envelope.Statement}
	body, err := canonicalJSON(unsigned)
	if err != nil {
		return nil, err
	}
	return append([]byte(signingContext), body...), nil
}

func decode(body []byte) (Envelope, error) {
	if len(body) == 0 || len(body) > MaxBytes {
		return Envelope{}, fmt.Errorf("CI evidence must contain 1 to %d bytes", MaxBytes)
	}
	if _, err := action.ParseObjectJSON(body); err != nil {
		return Envelope{}, fmt.Errorf("decode CI evidence: %w", err)
	}
	var envelope Envelope
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return Envelope{}, fmt.Errorf("decode CI evidence fields: %w", err)
	}
	normalized, err := canonicalJSON(envelope)
	if err != nil || !bytes.Equal(body, normalized) {
		return Envelope{}, fmt.Errorf("CI evidence must use exact canonical fields and encoding")
	}
	return envelope, nil
}

func canonicalJSON(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode CI evidence: %w", err)
	}
	if len(body) > MaxBytes {
		return nil, fmt.Errorf("CI evidence exceeds %d bytes", MaxBytes)
	}
	parsed, err := action.ParseObjectJSON(body)
	if err != nil {
		return nil, fmt.Errorf("canonicalize CI evidence: %w", err)
	}
	return parsed.MarshalJSON()
}
