package actionledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Seal binds one validated event to its sequence and previous digest, then
// returns the exact compact JSON record written to the ledger.
func Seal(record Record, sequence uint64, previousDigest string) (Record, []byte, error) {
	record.Schema = Schema
	record.FormatVersion = FormatVersion
	record.ChainVersion = ChainVersion
	record.Sequence = sequence
	record.PreviousDigest = previousDigest
	record.Digest = ""
	if err := record.Validate(); err != nil {
		return Record{}, nil, err
	}
	digest, err := recordDigest(record)
	if err != nil {
		return Record{}, nil, err
	}
	record.Digest = digest
	body, err := encodeRecord(record)
	if err != nil {
		return Record{}, nil, err
	}
	return record, body, nil
}

func Encode(record Record) ([]byte, error) {
	if err := record.Validate(); err != nil {
		return nil, err
	}
	if record.Digest == "" {
		return nil, fmt.Errorf("ledger record is not sealed")
	}
	want, err := recordDigest(record)
	if err != nil || want != record.Digest {
		return nil, fmt.Errorf("ledger record digest mismatch")
	}
	return encodeRecord(record)
}

func Decode(body []byte) (Record, error) {
	if len(body) == 0 || len(body) > MaxRecordBytes {
		return Record{}, fmt.Errorf("ledger record must contain 1 to %d bytes", MaxRecordBytes)
	}
	body = bytes.TrimSuffix(body, []byte("\n"))
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var record Record
	if err := decoder.Decode(&record); err != nil {
		return Record{}, fmt.Errorf("decode ledger record: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Record{}, fmt.Errorf("ledger record contains trailing data")
	}
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	encoded, err := encodeRecord(record)
	if err != nil {
		return Record{}, err
	}
	if !bytes.Equal(body, encoded) {
		return Record{}, fmt.Errorf("ledger record is not canonically encoded")
	}
	want, err := recordDigest(record)
	if err != nil || want != record.Digest {
		return Record{}, fmt.Errorf("ledger record digest mismatch")
	}
	return record, nil
}

func encodeRecord(record Record) ([]byte, error) {
	body, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("encode ledger record: %w", err)
	}
	if len(body)+1 > MaxRecordBytes {
		return nil, fmt.Errorf("ledger record is %d bytes; maximum is %d", len(body)+1, MaxRecordBytes)
	}
	return body, nil
}

func recordDigest(record Record) (string, error) {
	record.Digest = ""
	body, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("encode ledger digest input: %w", err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}
