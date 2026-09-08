package cievidence

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestDecodeRequirement(t *testing.T) {
	_, requirement, _, _ := ciFixture(t)
	configuration := Configuration{
		Schema: RequirementSchema, FormatVersion: FormatVersion, Repository: requirement.Repository,
		AuthorityKeyID: requirement.AuthorityKeyID, PublicKey: base64.RawURLEncoding.EncodeToString(requirement.PublicKey),
		RequiredChecks: requirement.RequiredChecks, MaxAgeSeconds: 300,
	}
	body, err := json.MarshalIndent(configuration, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		mutate  func([]byte) []byte
		wantErr bool
	}{
		{name: "formatted", mutate: func(body []byte) []byte { return body }},
		{name: "unknown field", mutate: func(body []byte) []byte { return append([]byte(`{"extra":true,`), body[1:]...) }, wantErr: true},
		{name: "case alias", mutate: func(body []byte) []byte { return bytes.Replace(body, []byte(`"schema"`), []byte(`"SCHEMA"`), 1) }, wantErr: true},
		{name: "missing check selection", mutate: func(body []byte) []byte {
			return bytes.Replace(body, []byte(`"provider/workflow/build"`), []byte(`null`), 1)
		}, wantErr: true},
		{name: "age overflow", mutate: func(body []byte) []byte {
			return bytes.Replace(body, []byte(`"300"`), []byte(`"9223372036854775807"`), 1)
		}, wantErr: true},
		{name: "zero age", mutate: func(body []byte) []byte { return bytes.Replace(body, []byte(`"300"`), []byte(`"0"`), 1) }, wantErr: true},
		{name: "numeric age", mutate: func(body []byte) []byte { return bytes.Replace(body, []byte(`"300"`), []byte(`300`), 1) }, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded, err := DecodeRequirement(test.mutate(bytes.Clone(body)))
			if (err != nil) != test.wantErr {
				t.Fatalf("decode requirement: error=%v, want error=%v", err, test.wantErr)
			}
			if err == nil && (decoded.Repository != requirement.Repository || !bytes.Equal(decoded.PublicKey, requirement.PublicKey) || decoded.MaxAge != requirement.MaxAge) {
				t.Fatal("decoded requirement changed trusted values")
			}
		})
	}
}
