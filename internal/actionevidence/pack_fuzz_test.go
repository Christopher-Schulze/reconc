package actionevidence

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func FuzzDecodePack(f *testing.F) {
	valid, err := json.Marshal(testPack([]FactID{FactLedgerEventsComplete}))
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{
		valid,
		[]byte(`{"schema":"reconc.action-control-map/v1"}`),
		[]byte(`{"schema":"reconc.action-control-map/v1","schema":"other"}`),
		{0xff},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > MaxPackBytes {
			t.Skip()
		}
		first, err := DecodePack(input)
		if err != nil {
			return
		}
		canonical, err := json.Marshal(first)
		if err != nil {
			t.Fatal(err)
		}
		second, err := DecodePack(canonical)
		if err != nil {
			t.Fatalf("accepted control-map pack did not round-trip: %v", err)
		}
		firstIdentity, firstErr := PackIdentity(first)
		secondIdentity, secondErr := PackIdentity(second)
		if firstErr != nil || secondErr != nil || firstIdentity != secondIdentity {
			t.Fatalf("accepted control-map pack identity changed: %v / %v", firstErr, secondErr)
		}
	})
}

func FuzzDecodeMappingAuthorityRegistry(f *testing.F) {
	publicKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x41}, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	valid, err := json.Marshal(MappingAuthorityRegistry{
		Schema:        AuthorityRegistrySchema,
		FormatVersion: FormatVersion,
		Authorities: []MappingAuthority{{
			ID: "reviewer", PublicKey: base64.RawURLEncoding.EncodeToString(publicKey),
		}},
	})
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{valid, []byte(`{}`), []byte(`{"authorities":[]}`), {0xff}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > MaxAuthorityRegistrySize {
			t.Skip()
		}
		registry, err := decodeMappingAuthorityRegistry(input)
		if err == nil && len(registry) == 0 {
			t.Fatal("accepted control-map authority registry is empty")
		}
	})
}
