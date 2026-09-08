// Package cievidence verifies externally signed CI results for an exact Git
// candidate. Callers own trusted key configuration and the pre-action boundary.
package cievidence

import (
	"crypto/ed25519"
	"time"
)

const (
	Schema         = "reconc.ci-evidence/v1"
	EnvelopeSchema = "reconc.ci-signed-evidence/v1"
	FormatVersion  = "1"
	MaxBytes       = 256 << 10
	MaxChecks      = 256
)

type CandidateKind string

const (
	CandidateCommit     CandidateKind = "commit"
	CandidateMergeQueue CandidateKind = "merge-queue"
)

type Candidate struct {
	Kind     CandidateKind `json:"kind"`
	ObjectID string        `json:"object_id"`
}

type Outcome string

const (
	OutcomeSuccess   Outcome = "success"
	OutcomeFailure   Outcome = "failure"
	OutcomePending   Outcome = "pending"
	OutcomeCancelled Outcome = "cancelled"
)

// ID must identify the provider and its workflow/check contract, not just an
// untrusted display name. The signing integration owns that mapping.
type Check struct {
	ID      string  `json:"id"`
	RunID   string  `json:"run_id"`
	Outcome Outcome `json:"outcome"`
}

// IssuedAt and ExpiresAt are Unix seconds encoded as canonical decimal JSON
// strings, avoiding exponent normalization of integers by the JSON canonicalizer.
type Statement struct {
	Schema        string    `json:"schema"`
	FormatVersion string    `json:"format_version"`
	Repository    string    `json:"repository"`
	Candidate     Candidate `json:"candidate"`
	IssuedAt      int64     `json:"issued_at,string"`
	ExpiresAt     int64     `json:"expires_at,string"`
	Checks        []Check   `json:"checks"`
}

type Envelope struct {
	Schema         string    `json:"schema"`
	FormatVersion  string    `json:"format_version"`
	AuthorityKeyID string    `json:"authority_key_id"`
	Statement      Statement `json:"statement"`
	Signature      string    `json:"signature"`
}

// Requirement must come from trusted configuration, never from the envelope.
// Candidate must come from the operation being gated. Verification is offline;
// it cannot discover CI reruns, revocations or results published after issuance.
type Requirement struct {
	Repository     string
	AuthorityKeyID string
	PublicKey      ed25519.PublicKey
	RequiredChecks []string
	MaxAge         time.Duration
}

type Verification struct {
	Statement Statement
	Identity  string
}
