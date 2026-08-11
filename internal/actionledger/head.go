package actionledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"reconc.dev/reconc/internal/action"
)

const (
	headSchema   = "reconc.action-ledger-head/v1"
	headFileName = "ledger.head.json"
	maxHeadBytes = 8 << 10
)

type chainHead struct {
	Schema             string `json:"schema"`
	FormatVersion      string `json:"format_version"`
	ChainVersion       string `json:"chain_version"`
	RepositoryIdentity string `json:"repository_identity"`
	FirstSequence      uint64 `json:"first_sequence"`
	FirstDigest        string `json:"first_digest"`
	LastSequence       uint64 `json:"last_sequence"`
	LastDigest         string `json:"last_digest"`
	EntryCount         uint64 `json:"entry_count"`
	UpdatedAt          string `json:"updated_at"`
}

func (h chainHead) validate() error {
	if h.Schema != headSchema || h.FormatVersion != FormatVersion || h.ChainVersion != ChainVersion ||
		!action.ValidKeyedIdentity(h.RepositoryIdentity) || h.FirstSequence == 0 ||
		h.LastSequence < h.FirstSequence || h.EntryCount == 0 ||
		h.LastSequence-h.FirstSequence+1 != h.EntryCount ||
		!lowerDigestPattern.MatchString(h.FirstDigest) || !lowerDigestPattern.MatchString(h.LastDigest) {
		return fmt.Errorf("action ledger detached head is invalid")
	}
	if _, err := canonicalTimestamp(h.UpdatedAt); err != nil {
		return fmt.Errorf("action ledger detached head timestamp is invalid: %w", err)
	}
	return nil
}

func newChainHead(record Record) chainHead {
	return chainHead{
		Schema: headSchema, FormatVersion: FormatVersion, ChainVersion: ChainVersion,
		RepositoryIdentity: record.Call.RepositoryIdentity,
		FirstSequence:      record.Sequence, FirstDigest: record.Digest,
		LastSequence: record.Sequence, LastDigest: record.Digest,
		EntryCount: 1, UpdatedAt: record.Timestamp,
	}
}

func advanceChainHead(previous chainHead, record Record) (chainHead, error) {
	if err := previous.validate(); err != nil {
		return chainHead{}, err
	}
	if previous.RepositoryIdentity != record.Call.RepositoryIdentity ||
		record.Sequence != previous.LastSequence+1 || record.PreviousDigest != previous.LastDigest {
		return chainHead{}, fmt.Errorf("action ledger record does not advance the detached head")
	}
	previous.LastSequence = record.Sequence
	previous.LastDigest = record.Digest
	previous.EntryCount++
	previous.UpdatedAt = record.Timestamp
	return previous, nil
}

func encodeChainHead(head chainHead) ([]byte, error) {
	if err := head.validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(head)
	if err != nil {
		return nil, fmt.Errorf("encode action ledger detached head: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxHeadBytes {
		return nil, fmt.Errorf("action ledger detached head exceeds %d bytes", maxHeadBytes)
	}
	return body, nil
}

func decodeChainHead(body []byte) (chainHead, error) {
	if len(body) == 0 || len(body) > maxHeadBytes {
		return chainHead{}, fmt.Errorf("action ledger detached head must contain 1 to %d bytes", maxHeadBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var head chainHead
	if err := decoder.Decode(&head); err != nil {
		return chainHead{}, fmt.Errorf("decode action ledger detached head: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return chainHead{}, fmt.Errorf("action ledger detached head contains trailing data")
	}
	if err := head.validate(); err != nil {
		return chainHead{}, err
	}
	canonical, err := encodeChainHead(head)
	if err != nil {
		return chainHead{}, err
	}
	if !bytes.Equal(body, canonical) {
		return chainHead{}, fmt.Errorf("action ledger detached head is not canonically encoded")
	}
	return head, nil
}

func canonicalTimestamp(value string) (string, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	canonical := parsed.UTC().Format(time.RFC3339Nano)
	if err != nil || parsed.IsZero() || canonical != value {
		return "", fmt.Errorf("timestamp must be canonical UTC RFC3339Nano")
	}
	return canonical, nil
}
