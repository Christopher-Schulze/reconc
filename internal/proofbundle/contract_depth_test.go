package proofbundle

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/completiongate"
	"reconc.dev/reconc/internal/schema"
)

func validProofBundle() *Bundle {
	value := &Bundle{
		Schema: schema.ProofBundleURL, FormatVersion: FormatVersion,
		OK: true, Decision: "pass", RepoRoot: ".",
		Build: Build{Version: "test"},
		Task:  Task{Configured: false, State: "absent"},
		Candidate: Candidate{
			Fingerprint: strings.Repeat("a", 64), PolicyLockHash: strings.Repeat("b", 64),
			WorktreeHash: strings.Repeat("d", 64), WorktreeTrusted: true, DirtyPaths: []string{},
		},
		Checks: []Check{},
		Evidence: Evidence{
			RequiredCommands: []string{}, RequiredPaths: []string{}, RequiredClaims: []string{},
			SatisfiedChecks: []string{}, CommandProofs: []CommandProof{},
		},
		Violations:       []Violation{},
		SupersededBlocks: []SupersededBlock{},
		CompletionDigest: strings.Repeat("c", 64),
	}
	value.Digest = digest(value)
	return value
}

func TestVerifyRejectsInvalidPublicContractFields(t *testing.T) {
	if err := Verify(nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil bundle was accepted: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Bundle)
		err    string
	}{
		{name: "schema", mutate: func(b *Bundle) { b.Schema = "other" }, err: "unsupported"},
		{name: "format", mutate: func(b *Bundle) { b.FormatVersion = "2" }, err: "unsupported"},
		{name: "root", mutate: func(b *Bundle) { b.RepoRoot = "/private" }, err: "unsupported"},
		{name: "decision", mutate: func(b *Bundle) { b.Decision = "unknown" }, err: "decision"},
		{name: "decision boolean", mutate: func(b *Bundle) { b.OK = false }, err: "decision"},
		{name: "build version", mutate: func(b *Bundle) { b.Build.Version = " " }, err: "identity"},
		{name: "candidate fingerprint", mutate: func(b *Bundle) { b.Candidate.Fingerprint = "bad" }, err: "identity"},
		{name: "policy hash", mutate: func(b *Bundle) { b.Candidate.PolicyLockHash = "bad" }, err: "identity"},
		{name: "Git HEAD", mutate: func(b *Bundle) {
			b.Candidate.GitAvailable = true
			b.Candidate.GitHead = "bad"
			b.Candidate.GitIndexHash = strings.Repeat("b", 64)
		}, err: "Git candidate identity"},
		{name: "completion digest", mutate: func(b *Bundle) { b.CompletionDigest = "bad" }, err: "identity"},
		{name: "task state", mutate: func(b *Bundle) { b.Task.State = "queued" }, err: "TASK identity"},
		{name: "task configuration", mutate: func(b *Bundle) { b.Task.Configured = true }, err: "TASK identity"},
		{name: "null checks", mutate: func(b *Bundle) { b.Checks = nil }, err: "null collection"},
		{name: "null evidence", mutate: func(b *Bundle) { b.Evidence.RequiredClaims = nil }, err: "null collection"},
		{name: "invalid check id", mutate: func(b *Bundle) {
			b.Checks = []Check{{Status: completiongate.StatusPass}}
		}, err: "invalid check"},
		{name: "invalid check status", mutate: func(b *Bundle) {
			b.Checks = []Check{{ID: "policy", Status: "unknown"}}
		}, err: "invalid check"},
		{name: "invalid command proof", mutate: func(b *Bundle) {
			b.Evidence.CommandProofs = []CommandProof{}
			b.Evidence.CommandProofs = append(b.Evidence.CommandProofs, CommandProof{})
		}, err: "invalid command proof"},
		{name: "digest", mutate: func(b *Bundle) { b.Digest = strings.Repeat("0", 64) }, err: "digest mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle := validProofBundle()
			test.mutate(bundle)
			err := Verify(bundle)
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Fatalf("expected %q error, got %v", test.err, err)
			}
		})
	}
}

func TestRenderMarkdownIncludesRichEvidenceContract(t *testing.T) {
	bundle := validProofBundle()
	bundle.OK = false
	bundle.Decision = "block"
	bundle.Task = Task{Configured: true, ID: "092", State: "active"}
	bundle.Candidate.GitAvailable = true
	bundle.Candidate.GitHead = strings.Repeat("a", 40)
	bundle.Candidate.GitIndexHash = strings.Repeat("b", 64)
	bundle.Candidate.WorktreeHash = strings.Repeat("c", 64)
	bundle.Candidate.PolicyReportHash = strings.Repeat("f", 64)
	bundle.Candidate.DirtyPaths = []string{"internal/runtime/events.go"}
	bundle.Checks = []Check{{ID: "policy", Status: completiongate.StatusFail, Detail: "review | warning"}}
	bundle.Evidence.RequiredCommands = []string{"go test ./..."}
	bundle.Evidence.RequiredPaths = []string{"README.md"}
	bundle.Evidence.RequiredClaims = []string{"tests-green"}
	bundle.Evidence.SatisfiedChecks = []string{}
	bundle.Evidence.CommandProofs = []CommandProof{{
		Command: "go [arguments redacted]", CommandHash: strings.Repeat("d", 64),
		ExecutionMode: "direct", Outcome: "success", Head: strings.Repeat("a", 40), IndexTree: strings.Repeat("b", 40),
		ReceiptDigest: strings.Repeat("e", 64), CandidateBound: true, Fresh: true,
	}}
	bundle.Violations = []Violation{{
		RuleID: "docs", Kind: "require_path", Mode: "block", Message: "missing `docs`",
		RecommendedAction: "add docs", MatchedPaths: []string{"README.md"},
		RequiredPaths: []string{"docs/documentation.md"}, RequiredCommands: []string{"make coverage"},
		RequiredClaims: []string{"documented"},
	}}
	bundle.SupersededBlocks = []SupersededBlock{{
		CandidateFingerprint: strings.Repeat("f", 64), PolicyReportHash: strings.Repeat("1", 64),
		Violations: []Violation{{
			RuleID: "old", Kind: "deny_write", Mode: "block", Message: "old block",
			MatchedPaths: []string{}, RequiredPaths: []string{}, RequiredCommands: []string{}, RequiredClaims: []string{},
		}},
	}}
	bundle.NextAction = "Resolve the warning."
	bundle.Digest = digest(bundle)

	var output bytes.Buffer
	if err := RenderMarkdown(&output, bundle); err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	for _, expected := range []string{
		"ID: `092`", "HEAD: `" + strings.Repeat("a", 40) + "`", "Command | Outcome", "Remediation: add docs",
		"Matched paths:", "Superseded Blocks", "Resolve the warning.",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("Markdown omitted %q:\n%s", expected, output.String())
		}
	}
}

type failingProofWriter struct{}

func (failingProofWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestRenderMarkdownPropagatesWriterFailure(t *testing.T) {
	if err := RenderMarkdown(failingProofWriter{}, validProofBundle()); err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("writer failure was not propagated: %v", err)
	}
}

func TestProofBundleRenderersEnforceByteBudget(t *testing.T) {
	bundle := validProofBundle()
	bundle.Checks = make([]Check, maxItems)
	bundle.Evidence.SatisfiedChecks = make([]string, maxItems)
	for index := range bundle.Checks {
		id := fmt.Sprintf("check-%03d", index)
		bundle.Checks[index] = Check{ID: id, Status: completiongate.StatusPass, Detail: strings.Repeat("x", maxTextBytes)}
		bundle.Evidence.SatisfiedChecks[index] = id
	}
	bundle.Digest = digest(bundle)
	if _, err := MarshalJSON(bundle); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized JSON bundle was accepted: %v", err)
	}
	if err := RenderMarkdown(&bytes.Buffer{}, bundle); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized Markdown bundle was accepted: %v", err)
	}
}
