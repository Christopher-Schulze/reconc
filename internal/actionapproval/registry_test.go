package actionapproval

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"testing"
	"time"
)

func TestApprovalRegistryRequiresCanonicalCollectionOrder(t *testing.T) {
	now := time.Date(2026, 8, 11, 16, 0, 0, 0, time.UTC)
	first := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x11}, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	second := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x22}, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	canonical := Registry{
		Schema: RegistrySchema, FormatVersion: FormatVersion,
		Authorities: []Authority{
			{ID: "authority-a", PublicKey: base64.RawURLEncoding.EncodeToString(first), ActiveFrom: now.Format(time.RFC3339Nano)},
			{ID: "authority-b", PublicKey: base64.RawURLEncoding.EncodeToString(second), ActiveFrom: now.Format(time.RFC3339Nano)},
		},
		AuthorityPolicies: []AuthorityPolicy{
			{ID: "policy-a", AuthorityKeyIDs: []string{"authority-a", "authority-b"}},
			{ID: "policy-b", AuthorityKeyIDs: []string{"authority-b"}},
		},
	}
	body, err := canonicalJSON(canonical)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := DecodeRegistry(body)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Identity() == "" {
		t.Fatal("canonical registry lacks an identity")
	}

	unsortedAuthorities := canonical
	unsortedAuthorities.Authorities = []Authority{canonical.Authorities[1], canonical.Authorities[0]}
	assertRegistryDecodeFails(t, unsortedAuthorities)
	unsortedPolicies := canonical
	unsortedPolicies.AuthorityPolicies = []AuthorityPolicy{canonical.AuthorityPolicies[1], canonical.AuthorityPolicies[0]}
	assertRegistryDecodeFails(t, unsortedPolicies)
	unsortedKeys := canonical
	unsortedKeys.AuthorityPolicies = append([]AuthorityPolicy(nil), canonical.AuthorityPolicies...)
	unsortedKeys.AuthorityPolicies[0].AuthorityKeyIDs = []string{"authority-b", "authority-a"}
	assertRegistryDecodeFails(t, unsortedKeys)
}

func TestApprovalRegistryRejectsUnknownDuplicateAndAliasedAuthorities(t *testing.T) {
	now := time.Date(2026, 8, 11, 16, 0, 0, 0, time.UTC)
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x31}, ed25519.SeedSize))
	key := base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey))
	base := Registry{
		Schema: RegistrySchema, FormatVersion: FormatVersion,
		Authorities:       []Authority{{ID: "authority-a", PublicKey: key, ActiveFrom: now.Format(time.RFC3339Nano)}},
		AuthorityPolicies: []AuthorityPolicy{{ID: "policy-a", AuthorityKeyIDs: []string{"authority-a"}}},
	}
	tests := []struct {
		name   string
		mutate func(*Registry)
	}{
		{name: "unknown policy key", mutate: func(value *Registry) { value.AuthorityPolicies[0].AuthorityKeyIDs[0] = "missing" }},
		{name: "duplicate authority", mutate: func(value *Registry) { value.Authorities = append(value.Authorities, value.Authorities[0]) }},
		{name: "aliased public key", mutate: func(value *Registry) {
			value.Authorities = append(value.Authorities, Authority{ID: "authority-b", PublicKey: key, ActiveFrom: now.Format(time.RFC3339Nano)})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := cloneRegistry(base)
			test.mutate(&value)
			if _, err := CompileRegistry(value); err == nil {
				t.Fatal("invalid approval authority registry was accepted")
			}
		})
	}
}

func assertRegistryDecodeFails(t testing.TB, registry Registry) {
	t.Helper()
	body, err := canonicalJSON(registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRegistry(body); err == nil {
		t.Fatal("non-canonically ordered approval registry was accepted")
	}
}
