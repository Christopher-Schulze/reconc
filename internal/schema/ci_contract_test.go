package schema_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/cievidence"
	"reconc.dev/reconc/internal/schema"
)

func TestSchemasValidateActualCIEncodings(t *testing.T) {
	compiled := compileRegisteredSchemas(t)
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	statement := cievidence.Statement{
		Schema: cievidence.Schema, FormatVersion: cievidence.FormatVersion, Repository: "provider/team/repo",
		Candidate: cievidence.Candidate{Kind: cievidence.CandidateMergeQueue, ObjectID: strings.Repeat("a", 40)},
		IssuedAt:  1_800_000_000, ExpiresAt: 1_800_000_600,
		Checks: []cievidence.Check{{ID: "provider/workflow/build", RunID: "42/attempt/1", Outcome: cievidence.OutcomeSuccess}},
	}
	signed, err := cievidence.Sign(statement, "ci-production", private)
	if err != nil {
		t.Fatal(err)
	}
	statementBody, err := json.Marshal(statement)
	if err != nil {
		t.Fatal(err)
	}
	configurationBody, err := json.Marshal(cievidence.Configuration{
		Schema: cievidence.RequirementSchema, FormatVersion: cievidence.FormatVersion,
		Repository: statement.Repository, AuthorityKeyID: "ci-production", PublicKey: base64.RawURLEncoding.EncodeToString(public),
		RequiredChecks: []string{"provider/workflow/build"}, MaxAgeSeconds: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		artifact schema.Artifact
		body     []byte
	}{
		{artifact: schema.CIStatement, body: statementBody},
		{artifact: schema.CIEvidence, body: signed},
		{artifact: schema.CIRequirement, body: configurationBody},
	} {
		t.Run(string(test.artifact), func(t *testing.T) {
			definition := compiled[schema.DefaultURL(test.artifact)]
			for _, mutation := range []struct {
				name  string
				body  []byte
				valid bool
			}{
				{name: "actual encoding", body: test.body, valid: true},
				{name: "unknown field", body: append([]byte(`{"unexpected":true,`), test.body[1:]...)},
				{name: "unknown format", body: bytes.Replace(test.body, []byte(`"format_version":"1"`), []byte(`"format_version":"2"`), 1)},
			} {
				t.Run(mutation.name, func(t *testing.T) {
					var value any
					if err := json.Unmarshal(mutation.body, &value); err != nil {
						t.Fatal(err)
					}
					if err := definition.Validate(value); (err == nil) != mutation.valid {
						t.Fatalf("schema validation: error=%v, valid=%v", err, mutation.valid)
					}
				})
			}
		})
	}
}
