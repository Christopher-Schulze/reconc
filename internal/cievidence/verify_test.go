package cievidence

import (
	"bytes"
	"crypto/ed25519"
	"strings"
	"testing"
	"time"
)

func ciFixture(t testing.TB) (Statement, Requirement, ed25519.PrivateKey, time.Time) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	statement := Statement{
		Schema: Schema, FormatVersion: FormatVersion, Repository: "https://example.test/team/repo",
		Candidate: Candidate{Kind: CandidateCommit, ObjectID: strings.Repeat("a", 40)},
		IssuedAt:  now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
		Checks: []Check{{ID: "provider/workflow/build", RunID: "123/attempt/2", Outcome: OutcomeSuccess}},
	}
	requirement := Requirement{
		Repository: statement.Repository, AuthorityKeyID: "ci-production", PublicKey: public,
		RequiredChecks: []string{"provider/workflow/build"}, MaxAge: 5 * time.Minute,
	}
	return statement, requirement, private, now
}

func signedCI(t *testing.T, statement Statement, requirement Requirement, key ed25519.PrivateKey) []byte {
	t.Helper()
	body, err := Sign(statement, requirement.AuthorityKeyID, key)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestVerifyExactCandidate(t *testing.T) {
	for _, test := range []struct {
		name string
		kind CandidateKind
		size int
	}{
		{name: "commit sha1", kind: CandidateCommit, size: 40},
		{name: "commit sha256", kind: CandidateCommit, size: 64},
		{name: "merge queue", kind: CandidateMergeQueue, size: 40},
	} {
		t.Run(test.name, func(t *testing.T) {
			statement, requirement, private, now := ciFixture(t)
			statement.Candidate = Candidate{Kind: test.kind, ObjectID: strings.Repeat("b", test.size)}
			body := signedCI(t, statement, requirement, private)
			verified, err := Verify(body, requirement, statement.Candidate, now)
			if err != nil || verified.Statement.Candidate != statement.Candidate || !strings.HasPrefix(verified.Identity, "sha256:") {
				t.Fatalf("exact signed candidate: result=%+v error=%v", verified, err)
			}
			if !bytes.Equal(body, signedCI(t, statement, requirement, private)) {
				t.Fatal("identical CI statement did not produce deterministic signed bytes")
			}
		})
	}
}

func TestVerifyRejectsSignedWrongEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Statement)
	}{
		{name: "wrong repository", mutate: func(statement *Statement) { statement.Repository += "-other" }},
		{name: "stale commit", mutate: func(statement *Statement) { statement.Candidate.ObjectID = strings.Repeat("b", 40) }},
		{name: "source commit is not merge queue", mutate: func(statement *Statement) { statement.Candidate.Kind = CandidateMergeQueue }},
		{name: "missing required check", mutate: func(statement *Statement) { statement.Checks[0].ID = "provider/workflow/other" }},
		{name: "failed", mutate: func(statement *Statement) { statement.Checks[0].Outcome = OutcomeFailure }},
		{name: "pending", mutate: func(statement *Statement) { statement.Checks[0].Outcome = OutcomePending }},
		{name: "cancelled", mutate: func(statement *Statement) { statement.Checks[0].Outcome = OutcomeCancelled }},
		{name: "expired at boundary", mutate: func(statement *Statement) { statement.ExpiresAt -= 60 }},
		{name: "future issuance", mutate: func(statement *Statement) { statement.IssuedAt += 61 }},
		{name: "too old", mutate: func(statement *Statement) { statement.IssuedAt -= 301 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			statement, requirement, private, now := ciFixture(t)
			candidate := statement.Candidate
			test.mutate(&statement)
			body := signedCI(t, statement, requirement, private)
			if _, err := Verify(body, requirement, candidate, now); err == nil {
				t.Fatal("accepted a valid signature over unsuitable CI evidence")
			}
		})
	}
}

func TestVerifyRejectsUnauthenticatedAndAmbiguousInput(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "self claim", mutate: func([]byte) []byte { return []byte(`{"claims":["ci-green"]}`) }},
		{name: "tampered run", mutate: func(body []byte) []byte {
			return bytes.Replace(body, []byte("123/attempt/2"), []byte("124/attempt/2"), 1)
		}},
		{name: "whitespace", mutate: func(body []byte) []byte { return append(body, '\n') }},
		{name: "duplicate field", mutate: func(body []byte) []byte {
			return bytes.Replace(body, []byte(`"format_version":"1"`), []byte(`"format_version":"1","format_version":"1"`), 1)
		}},
		{name: "case alias", mutate: func(body []byte) []byte { return bytes.Replace(body, []byte(`"run_id"`), []byte(`"RUN_ID"`), 1) }},
		{name: "unknown field", mutate: func(body []byte) []byte { return append([]byte(`{"extra":true,`), body[1:]...) }},
		{name: "trailing object", mutate: func(body []byte) []byte { return append(body, '{', '}') }},
		{name: "oversized", mutate: func([]byte) []byte { return bytes.Repeat([]byte(" "), MaxBytes+1) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			statement, requirement, private, now := ciFixture(t)
			body := test.mutate(signedCI(t, statement, requirement, private))
			if _, err := Verify(body, requirement, statement.Candidate, now); err == nil {
				t.Fatal("accepted unauthenticated or ambiguous CI evidence")
			}
		})
	}
}

func TestVerifyRejectsWrongTrustAndMissingRequirements(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Requirement)
	}{
		{name: "wrong key", mutate: func(requirement *Requirement) { requirement.PublicKey[0] ^= 1 }},
		{name: "wrong authority", mutate: func(requirement *Requirement) { requirement.AuthorityKeyID += "-other" }},
		{name: "short key", mutate: func(requirement *Requirement) { requirement.PublicKey = requirement.PublicKey[:1] }},
		{name: "no required checks", mutate: func(requirement *Requirement) { requirement.RequiredChecks = nil }},
		{name: "duplicate required checks", mutate: func(requirement *Requirement) {
			requirement.RequiredChecks = append(requirement.RequiredChecks, requirement.RequiredChecks[0])
		}},
		{name: "no age bound", mutate: func(requirement *Requirement) { requirement.MaxAge = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			statement, requirement, private, now := ciFixture(t)
			body := signedCI(t, statement, requirement, private)
			test.mutate(&requirement)
			if _, err := Verify(body, requirement, statement.Candidate, now); err == nil {
				t.Fatal("accepted invalid independent trust requirements")
			}
		})
	}
}

func TestVerifyRequiredChecksAndClockBoundaries(t *testing.T) {
	for _, test := range []struct {
		name    string
		offset  time.Duration
		wantErr bool
	}{
		{name: "issuance included", offset: -time.Minute},
		{name: "before expiry", offset: time.Minute - time.Nanosecond},
		{name: "expiry excluded", offset: time.Minute, wantErr: true},
		{name: "before issuance", offset: -time.Minute - time.Nanosecond, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			statement, requirement, private, now := ciFixture(t)
			statement.Checks = append(statement.Checks, Check{ID: "provider/workflow/optional", RunID: "124", Outcome: OutcomeFailure})
			body := signedCI(t, statement, requirement, private)
			_, err := Verify(body, requirement, statement.Candidate, now.Add(test.offset))
			if (err != nil) != test.wantErr {
				t.Fatalf("clock boundary: error=%v, want error=%v", err, test.wantErr)
			}
		})
	}
}

func TestSignRejectsMalformedStatements(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Statement)
	}{
		{name: "schema", mutate: func(statement *Statement) { statement.Schema = "other" }},
		{name: "empty checks", mutate: func(statement *Statement) { statement.Checks = nil }},
		{name: "duplicate checks", mutate: func(statement *Statement) { statement.Checks = append(statement.Checks, statement.Checks[0]) }},
		{name: "no run ID", mutate: func(statement *Statement) { statement.Checks[0].RunID = "" }},
		{name: "unknown outcome", mutate: func(statement *Statement) { statement.Checks[0].Outcome = "skipped" }},
		{name: "ref instead of object", mutate: func(statement *Statement) { statement.Candidate.ObjectID = "main" }},
		{name: "null object", mutate: func(statement *Statement) { statement.Candidate.ObjectID = strings.Repeat("0", 40) }},
		{name: "unknown candidate kind", mutate: func(statement *Statement) { statement.Candidate.Kind = "branch" }},
		{name: "hidden check characters", mutate: func(statement *Statement) { statement.Checks[0].ID += "\u202e" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			statement, requirement, private, _ := ciFixture(t)
			test.mutate(&statement)
			if _, err := Sign(statement, requirement.AuthorityKeyID, private); err == nil {
				t.Fatal("signed malformed CI evidence")
			}
		})
	}
}

func FuzzVerifyDoesNotAcceptMutatedUnsignedEvidence(f *testing.F) {
	statement, requirement, private, now := ciFixture(f)
	body, err := Sign(statement, requirement.AuthorityKeyID, private)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(body)
	f.Add([]byte(`{"claims":["ci-green"]}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		_, err := Verify(input, requirement, statement.Candidate, now)
		if err != nil {
			return
		}
		if !bytes.Equal(input, body) {
			t.Fatal("accepted different unsigned bytes")
		}
	})
}
