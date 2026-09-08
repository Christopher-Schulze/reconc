package cievidence

import (
	"bytes"
	"encoding/json"
	"fmt"

	"reconc.dev/reconc/internal/action"
)

// DecodeStatement accepts a formatted statement from a trusted issuer's
// provider integration. Parsing does not authenticate the supplied results.
func DecodeStatement(body []byte) (Statement, error) {
	if len(body) == 0 || len(body) > MaxBytes {
		return Statement{}, fmt.Errorf("CI statement must contain 1 to %d bytes", MaxBytes)
	}
	parsed, err := action.ParseObjectJSON(body)
	if err != nil {
		return Statement{}, fmt.Errorf("decode CI statement: %w", err)
	}
	canonical, err := parsed.MarshalJSON()
	if err != nil {
		return Statement{}, fmt.Errorf("canonicalize CI statement: %w", err)
	}
	var statement Statement
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&statement); err != nil {
		return Statement{}, fmt.Errorf("decode CI statement fields: %w", err)
	}
	normalized, err := canonicalJSON(statement)
	if err != nil || !bytes.Equal(canonical, normalized) {
		return Statement{}, fmt.Errorf("CI statement fields or representations are not exact")
	}
	if err := validateStatement(statement); err != nil {
		return Statement{}, err
	}
	return statement, nil
}
