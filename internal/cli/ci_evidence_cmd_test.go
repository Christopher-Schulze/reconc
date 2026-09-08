package cli

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/cievidence"
)

func ciEvidenceArguments(t *testing.T, objectID string, outcome cievidence.Outcome) []string {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	statement := cievidence.Statement{
		Schema: cievidence.Schema, FormatVersion: cievidence.FormatVersion, Repository: "provider/team/repo",
		Candidate: cievidence.Candidate{Kind: cievidence.CandidateCommit, ObjectID: objectID},
		IssuedAt:  now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
		Checks: []cievidence.Check{{ID: "provider/workflow/build", RunID: "42/attempt/1", Outcome: outcome}},
	}
	body, err := cievidence.Sign(statement, "ci-production", private)
	if err != nil {
		t.Fatal(err)
	}
	configuration := cievidence.Configuration{
		Schema: cievidence.RequirementSchema, FormatVersion: cievidence.FormatVersion,
		Repository: statement.Repository, AuthorityKeyID: "ci-production",
		PublicKey: base64.RawURLEncoding.EncodeToString(public), RequiredChecks: []string{"provider/workflow/build"}, MaxAgeSeconds: 300,
	}
	configurationBody, err := json.MarshalIndent(configuration, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	evidencePath, requirementPath := filepath.Join(directory, "evidence.json"), filepath.Join(directory, "requirement.json")
	if err := os.WriteFile(evidencePath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requirementPath, configurationBody, 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"ci", "verify-evidence", "--evidence", evidencePath, "--requirement", requirementPath, "--candidate", objectID, "--candidate-kind", "commit", "--json"}
}

func TestCIVerifyEvidence(t *testing.T) {
	for _, test := range []struct {
		name    string
		outcome cievidence.Outcome
		code    int
	}{
		{name: "success", outcome: cievidence.OutcomeSuccess},
		{name: "failed", outcome: cievidence.OutcomeFailure, code: 2},
		{name: "pending", outcome: cievidence.OutcomePending, code: 2},
		{name: "cancelled", outcome: cievidence.OutcomeCancelled, code: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := ciEvidenceArguments(t, strings.Repeat("a", 40), test.outcome)
			var stdout, stderr bytes.Buffer
			err := Run(args, "test", &stdout, &stderr)
			if ExitCode(err) != test.code {
				t.Fatalf("exit=%d want=%d, error=%v output=%s", ExitCode(err), test.code, err, stdout.String())
			}
			var report ciEvidenceReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if (report.Status == "verified") != (test.code == 0) || (report.Identity != "") != (test.code == 0) {
				t.Fatalf("incorrect verification status: %+v", report)
			}
		})
	}
}

func TestCIVerifyEvidenceRejectsInvalidOptions(t *testing.T) {
	for _, args := range [][]string{
		{}, {"--evidence"}, {"--unknown", "value"}, {"--claim", "ci-green"}, {"--json", "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output bytes.Buffer
			if err := Run(append([]string{"ci", "verify-evidence"}, args...), "test", &output, &output); ExitCode(err) != 1 {
				t.Fatalf("invalid options returned %v", err)
			}
		})
	}
}

func TestCIVerifyEvidenceGatesExactGitPublication(t *testing.T) {
	for _, test := range []struct {
		name          string
		outcome       cievidence.Outcome
		publish       bool
		staleEvidence bool
	}{
		{name: "failed CI preserves ref", outcome: cievidence.OutcomeFailure},
		{name: "stale CI preserves ref", outcome: cievidence.OutcomeSuccess, staleEvidence: true},
		{name: "successful CI publishes exact commit", outcome: cievidence.OutcomeSuccess, publish: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			initGitRepo(t, repo)
			gitCommand(t, repo, "commit", "--allow-empty", "-m", "base")
			base := ciGitHead(t, repo, "HEAD")
			gitCommand(t, repo, "update-ref", "refs/heads/published", base)
			gitCommand(t, repo, "commit", "--allow-empty", "-m", "candidate")
			candidate := ciGitHead(t, repo, "HEAD")
			evidenceCandidate := candidate
			if test.staleEvidence {
				evidenceCandidate = base
			}
			args := ciEvidenceArguments(t, evidenceCandidate, test.outcome)
			for index, argument := range args {
				if argument == "--candidate" {
					args[index+1] = candidate
				}
			}
			var output bytes.Buffer
			if err := Run(args, "test", &output, &output); err == nil {
				// The trusted caller uses the same immutable candidate for its
				// gate and compare-and-swap publication operation.
				gitCommand(t, repo, "update-ref", "refs/heads/published", candidate, base)
			} else if ExitCode(err) != 2 {
				t.Fatalf("verification operational failure: %v", err)
			}
			want := base
			if test.publish {
				want = candidate
			}
			if got := ciGitHead(t, repo, "refs/heads/published"); got != want {
				t.Fatalf("published ref=%s, want=%s", got, want)
			}
		})
	}
}

func ciGitHead(t *testing.T, repo, ref string) string {
	t.Helper()
	command := exec.Command("git", "-C", repo, "rev-parse", "--verify", ref)
	body, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(body))
}

func TestCIEvidenceHelpPreservesLegacyInvocations(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "legacy repository", args: []string{"ci", "./project", "--help"}, want: "Derive write_paths"},
		{name: "legacy range", args: []string{"ci", "--base", "main", "--help"}, want: "Derive write_paths"},
		{name: "legacy named repository", args: []string{"ci", "./verify-evidence", "--help"}, want: "Derive write_paths"},
		{name: "evidence subcommand", args: []string{"ci", "verify-evidence", "--help"}, want: "operator-owned requirements"},
		{name: "explicit help target", args: []string{"help", "ci", "verify-evidence"}, want: "operator-owned requirements"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := Run(test.args, "test", &output, &output); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), test.want) {
				t.Fatalf("help missing %q: %s", test.want, output.String())
			}
		})
	}
}
