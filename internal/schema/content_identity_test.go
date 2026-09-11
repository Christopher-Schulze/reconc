package schema

import (
	"bytes"
	"strings"
	"testing"
)

func TestContentIdentityNormalizesOnlyExactSelfIdentityStrings(t *testing.T) {
	original := []byte(`{"$id":"urn:example:original","properties":{"$schema":{"const":"urn:example:original"}},"$ref":"urn:example:dependency","type":"object"}`)
	want, err := ContentIdentity(PolicyLock, "6", original)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body []byte
		same bool
	}{
		{name: "same bytes", body: original, same: true},
		{name: "self identity replacement", body: bytes.ReplaceAll(original, []byte("urn:example:original"), []byte(want)), same: true},
		{name: "validation change", body: bytes.ReplaceAll(original, []byte(`"object"`), []byte(`"array"`))},
		{name: "dependency change", body: bytes.ReplaceAll(original, []byte("urn:example:dependency"), []byte("urn:example:other"))},
		{name: "formatting change", body: append(bytes.Clone(original), '\n')},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ContentIdentity(PolicyLock, "6", test.body)
			if err != nil || (got == want) != test.same {
				t.Fatalf("ContentIdentity() = %q (%v), same=%t, want same=%t", got, err, got == want, test.same)
			}
		})
	}
}

func TestContentPublicationRequiresExplicitTagWithoutChangingIdentity(t *testing.T) {
	contract, ok := CurrentContract(PolicyLock)
	if !ok {
		t.Fatal("current policy-lock contract missing")
	}
	tests := []struct {
		tag  string
		want string
	}{
		{tag: "reconc-v12.34.56", want: "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v12.34.56/schemas/v6/policy-lock.schema.json"},
		{tag: "reconc-v98.76.54", want: "https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v98.76.54/schemas/v6/policy-lock.schema.json"},
		{tag: ""}, {tag: "main"}, {tag: "reconc-v01.2.3"}, {tag: "reconc-v1.2.3/../main"},
	}
	for _, test := range tests {
		t.Run(test.tag, func(t *testing.T) {
			got, err := PublicationURL(contract, test.tag)
			if test.want == "" {
				if err == nil {
					t.Fatalf("PublicationURL accepted invalid tag %q", test.tag)
				}
				return
			}
			if err != nil || got != test.want || !strings.HasPrefix(contract.DefaultURL, "urn:reconc:schema:") {
				t.Fatalf("PublicationURL() = %q (%v), want %q; identity=%q", got, err, test.want, contract.DefaultURL)
			}
		})
	}
}
