package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/cievidence"
	"reconc.dev/reconc/internal/gitexec"
)

// This proves the actual issuer and CLI process boundary with real Git objects.
// Provider observations are explicit fixtures, not a live-provider claim.
func TestBuiltIssuerAndVerifierGatePublication(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	issuer := buildBoundaryBinary(t, root, "./scripts/release/sign-ci-evidence")
	verifier := buildBoundaryBinary(t, root, "./cmd/reconc")
	for _, test := range []struct {
		name        string
		outcome     cievidence.Outcome
		stale       bool
		destination bool
		verified    bool
	}{
		{name: "success", outcome: cievidence.OutcomeSuccess, verified: true},
		{name: "failure", outcome: cievidence.OutcomeFailure},
		{name: "stale candidate", outcome: cievidence.OutcomeSuccess, stale: true},
		{name: "changed destination", outcome: cievidence.OutcomeSuccess, destination: true, verified: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			boundaryGit(t, repo, "init", "--quiet")
			boundaryGit(t, repo, "config", "user.name", "CI Boundary Test")
			boundaryGit(t, repo, "config", "user.email", "ci@example.invalid")
			boundaryGit(t, repo, "commit", "--allow-empty", "-m", "base")
			base := boundaryGit(t, repo, "rev-parse", "HEAD")
			boundaryGit(t, repo, "update-ref", "refs/heads/published", base)
			boundaryGit(t, repo, "commit", "--allow-empty", "-m", "candidate")
			candidate := boundaryGit(t, repo, "rev-parse", "HEAD")
			statement, requirement, key := issuerFixture(t)
			statement.Candidate.ObjectID = candidate
			if test.stale {
				statement.Candidate.ObjectID = base
			}
			statement.IssuedAt, statement.ExpiresAt = time.Now().Add(-time.Second).Unix(), time.Now().Add(time.Minute).Unix()
			statement.Checks[0].Outcome = test.outcome
			statementBody, err := json.Marshal(statement)
			if err != nil {
				t.Fatal(err)
			}
			statementPath := issuerFile(t, "statement.json", statementBody)
			keyPath := issuerFile(t, "key.pem", key)
			issued := boundaryProcess(t, root, issuer, "--statement", statementPath, "--key", keyPath, "--authority", requirement.AuthorityKeyID)
			configuration, err := json.Marshal(cievidence.Configuration{
				Schema: cievidence.RequirementSchema, FormatVersion: cievidence.FormatVersion,
				Repository: requirement.Repository, AuthorityKeyID: requirement.AuthorityKeyID,
				PublicKey:      base64.RawURLEncoding.EncodeToString(requirement.PublicKey),
				RequiredChecks: requirement.RequiredChecks, MaxAgeSeconds: 60,
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			verify := exec.CommandContext(ctx, verifier, "ci", "verify-evidence",
				"--evidence", issuerFile(t, "evidence.json", issued),
				"--requirement", issuerFile(t, "requirement.json", configuration),
				"--candidate", candidate, "--candidate-kind", "merge-queue", "--json")
			verify.Dir = repo
			output, verifyErr := verify.CombinedOutput()
			if (verifyErr == nil) != test.verified {
				t.Fatalf("verification: %v: %s", verifyErr, output)
			}
			if verifyErr != nil {
				var exit *exec.ExitError
				if !errors.As(verifyErr, &exit) || exit.ExitCode() != 2 {
					t.Fatalf("expected blocking exit 2: %v: %s", verifyErr, output)
				}
			}
			want := base
			if test.destination {
				boundaryGit(t, repo, "commit", "--allow-empty", "-m", "concurrent publication")
				want = boundaryGit(t, repo, "rev-parse", "HEAD")
				boundaryGit(t, repo, "update-ref", "refs/heads/published", want, base)
			}
			if verifyErr == nil {
				publish := gitexec.CommandContext(ctx, repo, nil, "update-ref", "refs/heads/published", candidate, base)
				output, publishErr := publish.CombinedOutput()
				if (publishErr != nil) != test.destination {
					t.Fatalf("publication: %v: %s", publishErr, output)
				}
				if publishErr == nil {
					want = candidate
				}
			}
			if got := boundaryGit(t, repo, "rev-parse", "refs/heads/published"); got != want {
				t.Fatalf("publication changed wrong object: got %s want %s", got, want)
			}
		})
	}
}

func buildBoundaryBinary(t *testing.T, root, target string) string {
	t.Helper()
	name := "boundary"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	boundaryProcess(t, root, "go", "build", "-o", path, target)
	return path
}

func boundaryProcess(t *testing.T, directory, executable string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v: %s", executable, err, output)
	}
	return output
}

func boundaryGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := gitexec.CommandContext(ctx, directory, nil, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
