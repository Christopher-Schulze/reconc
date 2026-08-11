package actionapproval

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
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

type Authority struct {
	ID         string `json:"id"`
	PublicKey  string `json:"public_key"`
	ActiveFrom string `json:"active_from"`
	RevokedAt  string `json:"revoked_at,omitempty"`
}

type AuthorityPolicy struct {
	ID              string   `json:"id"`
	AuthorityKeyIDs []string `json:"authority_key_ids"`
}

type Registry struct {
	Schema            string            `json:"schema"`
	FormatVersion     string            `json:"format_version"`
	Authorities       []Authority       `json:"authorities"`
	AuthorityPolicies []AuthorityPolicy `json:"authority_policies"`
}

type compiledAuthority struct {
	key        ed25519.PublicKey
	activeFrom time.Time
	revokedAt  time.Time
}

type CompiledRegistry struct {
	registry    Registry
	authorities map[string]compiledAuthority
	policies    map[string]map[string]struct{}
	identity    string
}

func DecodeRegistry(body []byte) (*CompiledRegistry, error) {
	if len(body) == 0 || len(body) > MaxAuthorityRegistryBytes {
		return nil, fmt.Errorf("approval authority registry must contain 1 to %d bytes", MaxAuthorityRegistryBytes)
	}
	parsed, err := action.ParseObjectJSON(body)
	if err != nil {
		return nil, fmt.Errorf("decode approval authority registry: %w", err)
	}
	canonical, err := parsed.MarshalJSON()
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(body, canonical) {
		return nil, fmt.Errorf("approval authority registry is not canonical JSON")
	}
	var registry Registry
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&registry); err != nil {
		return nil, fmt.Errorf("decode approval authority registry: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode approval authority registry trailing data: %w", err)
	}
	compiled, err := CompileRegistry(registry)
	if err != nil {
		return nil, err
	}
	normalized, err := canonicalJSON(compiled.registry)
	if err != nil {
		return nil, fmt.Errorf("encode canonical approval authority registry: %w", err)
	}
	if !bytes.Equal(body, normalized) {
		return nil, fmt.Errorf("approval authority registry collections are not canonically ordered")
	}
	return compiled, nil
}

func CompileRegistry(input Registry) (*CompiledRegistry, error) {
	registry := cloneRegistry(input)
	if registry.Schema != RegistrySchema || registry.FormatVersion != FormatVersion ||
		registry.Authorities == nil || len(registry.Authorities) == 0 || len(registry.Authorities) > MaxAuthorities ||
		registry.AuthorityPolicies == nil || len(registry.AuthorityPolicies) == 0 ||
		len(registry.AuthorityPolicies) > MaxAuthorityPolicies {
		return nil, fmt.Errorf("approval authority registry metadata or bounds are invalid")
	}
	sort.Slice(registry.Authorities, func(i, j int) bool { return registry.Authorities[i].ID < registry.Authorities[j].ID })
	authorities := make(map[string]compiledAuthority, len(registry.Authorities))
	publicKeys := make(map[string]string, len(registry.Authorities))
	for index, authority := range registry.Authorities {
		if !action.SafeLabel(authority.ID) || index > 0 && registry.Authorities[index-1].ID == authority.ID {
			return nil, fmt.Errorf("approval authority ID is invalid or duplicated")
		}
		key, err := base64.RawURLEncoding.Strict().DecodeString(authority.PublicKey)
		if err != nil || len(key) != ed25519.PublicKeySize ||
			base64.RawURLEncoding.EncodeToString(key) != authority.PublicKey {
			return nil, fmt.Errorf("approval authority %q public key is invalid", authority.ID)
		}
		if previous, duplicate := publicKeys[authority.PublicKey]; duplicate {
			return nil, fmt.Errorf("approval authorities %q and %q alias one public key", previous, authority.ID)
		}
		publicKeys[authority.PublicKey] = authority.ID
		active, err := parseCanonicalTime(authority.ActiveFrom)
		if err != nil {
			return nil, fmt.Errorf("approval authority %q activation is invalid: %w", authority.ID, err)
		}
		revoked := time.Time{}
		if authority.RevokedAt != "" {
			revoked, err = parseCanonicalTime(authority.RevokedAt)
			if err != nil || !revoked.After(active) {
				return nil, fmt.Errorf("approval authority %q revocation must follow activation", authority.ID)
			}
		}
		authorities[authority.ID] = compiledAuthority{
			key: append(ed25519.PublicKey(nil), key...), activeFrom: active, revokedAt: revoked,
		}
	}
	sort.Slice(registry.AuthorityPolicies, func(i, j int) bool {
		return registry.AuthorityPolicies[i].ID < registry.AuthorityPolicies[j].ID
	})
	policies := make(map[string]map[string]struct{}, len(registry.AuthorityPolicies))
	for index := range registry.AuthorityPolicies {
		policy := &registry.AuthorityPolicies[index]
		sort.Strings(policy.AuthorityKeyIDs)
		if !action.SafeLabel(policy.ID) || index > 0 && registry.AuthorityPolicies[index-1].ID == policy.ID ||
			policy.AuthorityKeyIDs == nil || len(policy.AuthorityKeyIDs) == 0 ||
			len(policy.AuthorityKeyIDs) > MaxAuthorities {
			return nil, fmt.Errorf("approval authority policy is invalid or duplicated")
		}
		allowed := make(map[string]struct{}, len(policy.AuthorityKeyIDs))
		for keyIndex, keyID := range policy.AuthorityKeyIDs {
			if _, exists := authorities[keyID]; !exists ||
				keyIndex > 0 && policy.AuthorityKeyIDs[keyIndex-1] == keyID {
				return nil, fmt.Errorf("approval authority policy %q contains an unknown or duplicate key", policy.ID)
			}
			allowed[keyID] = struct{}{}
		}
		policies[policy.ID] = allowed
	}
	body, err := canonicalJSON(registry)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(body)
	return &CompiledRegistry{
		registry: registry, authorities: authorities, policies: policies,
		identity: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func (r *CompiledRegistry) Identity() string {
	if r == nil {
		return ""
	}
	return r.identity
}

func (r *CompiledRegistry) authorityKey(policyID, keyID string, issued time.Time) (ed25519.PublicKey, error) {
	if r == nil {
		return nil, approvalError(action.ReasonAuthorityUnavailable, "approval authority registry is unavailable", nil)
	}
	allowed, exists := r.policies[policyID]
	if !exists {
		return nil, approvalError(action.ReasonAuthorityUnavailable, "approval authority policy is unknown", nil)
	}
	if _, exists := allowed[keyID]; !exists {
		return nil, approvalError(action.ReasonAuthorityUnavailable, "approval authority key is not allowed by operator policy", nil)
	}
	authority, exists := r.authorities[keyID]
	if !exists || issued.Before(authority.activeFrom) ||
		(!authority.revokedAt.IsZero() && !issued.Before(authority.revokedAt)) {
		return nil, approvalError(action.ReasonAuthorityUnavailable, "approval authority key was inactive at issuance", nil)
	}
	return append(ed25519.PublicKey(nil), authority.key...), nil
}

func cloneRegistry(input Registry) Registry {
	out := input
	out.Authorities = append([]Authority(nil), input.Authorities...)
	out.AuthorityPolicies = append([]AuthorityPolicy(nil), input.AuthorityPolicies...)
	for index := range out.AuthorityPolicies {
		out.AuthorityPolicies[index].AuthorityKeyIDs = append(
			[]string(nil), input.AuthorityPolicies[index].AuthorityKeyIDs...,
		)
	}
	return out
}
