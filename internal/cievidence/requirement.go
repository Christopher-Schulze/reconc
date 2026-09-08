package cievidence

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"reconc.dev/reconc/internal/action"
)

const RequirementSchema = "reconc.ci-requirement/v1"

// Configuration is an operator-owned verification requirement. File placement
// and access control must prevent the governed candidate from changing it.
type Configuration struct {
	Schema         string   `json:"schema"`
	FormatVersion  string   `json:"format_version"`
	Repository     string   `json:"repository"`
	AuthorityKeyID string   `json:"authority_key_id"`
	PublicKey      string   `json:"public_key"`
	RequiredChecks []string `json:"required_checks"`
	MaxAgeSeconds  int64    `json:"max_age_seconds,string"`
}

// DecodeRequirement permits formatting whitespace but rejects ambiguous field
// names, duplicate keys and implicit or incomplete trust requirements.
func DecodeRequirement(body []byte) (Requirement, error) {
	if len(body) == 0 || len(body) > MaxBytes {
		return Requirement{}, fmt.Errorf("CI requirement must contain 1 to %d bytes", MaxBytes)
	}
	parsed, err := action.ParseObjectJSON(body)
	if err != nil {
		return Requirement{}, fmt.Errorf("decode CI requirement: %w", err)
	}
	canonical, err := parsed.MarshalJSON()
	if err != nil {
		return Requirement{}, fmt.Errorf("canonicalize CI requirement: %w", err)
	}
	var configuration Configuration
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configuration); err != nil {
		return Requirement{}, fmt.Errorf("decode CI requirement fields: %w", err)
	}
	normalized, err := canonicalJSON(configuration)
	if err != nil || !bytes.Equal(normalized, canonical) {
		return Requirement{}, fmt.Errorf("CI requirement fields or representations are not exact")
	}
	return compileRequirement(configuration)
}

func compileRequirement(configuration Configuration) (Requirement, error) {
	if configuration.Schema != RequirementSchema || configuration.FormatVersion != FormatVersion ||
		configuration.MaxAgeSeconds <= 0 || configuration.MaxAgeSeconds > math.MaxInt64/int64(time.Second) {
		return Requirement{}, fmt.Errorf("CI requirement schema or maximum age is invalid")
	}
	key, err := base64.RawURLEncoding.Strict().DecodeString(configuration.PublicKey)
	if err != nil || base64.RawURLEncoding.EncodeToString(key) != configuration.PublicKey {
		return Requirement{}, fmt.Errorf("CI requirement public key encoding is invalid")
	}
	requirement := Requirement{
		Repository: configuration.Repository, AuthorityKeyID: configuration.AuthorityKeyID,
		PublicKey: key, RequiredChecks: configuration.RequiredChecks,
		MaxAge: time.Duration(configuration.MaxAgeSeconds) * time.Second,
	}
	if err := validateRequirement(requirement); err != nil {
		return Requirement{}, err
	}
	return requirement, nil
}
