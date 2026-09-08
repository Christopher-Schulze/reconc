package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/cievidence"
)

func issuerFixture(t *testing.T) (cievidence.Statement, cievidence.Requirement, []byte) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	statement := cievidence.Statement{
		Schema: cievidence.Schema, FormatVersion: cievidence.FormatVersion, Repository: "provider/team/repo",
		Candidate: cievidence.Candidate{Kind: cievidence.CandidateMergeQueue, ObjectID: strings.Repeat("b", 40)},
		IssuedAt:  1_800_000_000, ExpiresAt: 1_800_000_600,
		Checks: []cievidence.Check{{ID: "provider/workflow/build", RunID: "42/attempt/1", Outcome: cievidence.OutcomeSuccess}},
	}
	requirement := cievidence.Requirement{
		Repository: statement.Repository, AuthorityKeyID: "ci-production", PublicKey: public,
		RequiredChecks: []string{"provider/workflow/build"}, MaxAge: time.Minute,
	}
	return statement, requirement, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func issuerFile(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIssuerProducesVerifiableEvidence(t *testing.T) {
	for _, outcome := range []cievidence.Outcome{cievidence.OutcomeSuccess, cievidence.OutcomeFailure} {
		t.Run(string(outcome), func(t *testing.T) {
			statement, requirement, key := issuerFixture(t)
			statement.Checks[0].Outcome = outcome
			body, err := json.MarshalIndent(statement, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"--statement", issuerFile(t, "statement.json", body), "--key", issuerFile(t, "key.pem", key), "--authority", requirement.AuthorityKeyID}
			var output bytes.Buffer
			if err := run(args, &output); err != nil {
				t.Fatal(err)
			}
			verified, err := cievidence.Verify(output.Bytes(), requirement, statement.Candidate, time.Unix(statement.IssuedAt, 0))
			if (err == nil) != (outcome == cievidence.OutcomeSuccess) {
				t.Fatalf("signed %s: verification error=%v", outcome, err)
			}
			if outcome == cievidence.OutcomeFailure && !strings.Contains(err.Error(), "has no successful completed result") {
				t.Fatalf("failed result was rejected before authenticated outcome evaluation: %v", err)
			}
			if err == nil && verified.Statement.Checks[0].RunID != "42/attempt/1" {
				t.Fatal("issuer changed the provider run identity")
			}
			if bytes.Contains(output.Bytes(), []byte("PRIVATE KEY")) || bytes.Contains(output.Bytes(), key) {
				t.Fatal("issuer output contains private key material")
			}
		})
	}
}

func TestIssuerRejectsAmbiguousStatements(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "case alias", mutate: func(body []byte) []byte {
			return bytes.Replace(body, []byte(`"repository"`), []byte(`"REPOSITORY"`), 1)
		}},
		{name: "unknown field", mutate: func(body []byte) []byte { return append([]byte(`{"claim":"ci-green",`), body[1:]...) }},
		{name: "duplicate field", mutate: func(body []byte) []byte { return append([]byte(`{"schema":"reconc.ci-evidence/v1",`), body[1:]...) }},
		{name: "self claim only", mutate: func([]byte) []byte { return []byte(`{"claim":"ci-green"}`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			statement, requirement, key := issuerFixture(t)
			body, err := json.Marshal(statement)
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"--statement", issuerFile(t, "statement.json", test.mutate(body)), "--key", issuerFile(t, "key.pem", key), "--authority", requirement.AuthorityKeyID}
			var output bytes.Buffer
			if err := run(args, &output); err == nil || output.Len() != 0 {
				t.Fatalf("invalid statement signed: error=%v bytes=%d", err, output.Len())
			}
		})
	}
}

func TestIssuerRejectsMalformedKeyContainers(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "leading garbage", mutate: func(body []byte) []byte { return append([]byte("garbage\n"), body...) }},
		{name: "trailing garbage", mutate: func(body []byte) []byte { return append(body, []byte("garbage")...) }},
		{name: "two keys", mutate: func(body []byte) []byte { return append(body, body...) }},
		{name: "invalid DER", mutate: func([]byte) []byte {
			return []byte("-----BEGIN " + "PRIVATE KEY-----\nAA==\n-----END " + "PRIVATE KEY-----\n")
		}},
		{name: "malformed PEM", mutate: func([]byte) []byte { return []byte("-----BEGIN " + "PRIVATE KEY-----\nmalformed") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, key := issuerFixture(t)
			if _, err := decodeKey(test.mutate(key)); err == nil {
				t.Fatal("accepted malformed signing-key container")
			}
		})
	}
}
