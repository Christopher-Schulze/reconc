package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/proofbundle"
)

func TestProofJSONMarkdownAndAtomicOutput(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"proof", repo}, "0.8.6-test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var bundle proofbundle.Bundle
	if err := json.Unmarshal(stdout.Bytes(), &bundle); err != nil {
		t.Fatalf("decode JSON proof: %v\n%s", err, stdout.String())
	}
	if err := proofbundle.Verify(&bundle); err != nil {
		t.Fatal(err)
	}
	if !bundle.OK || bundle.Build.Version != "0.8.6-test" {
		t.Fatalf("unexpected proof: %#v", bundle)
	}

	stdout.Reset()
	output := filepath.Join(t.TempDir(), "nested", "proof.md")
	if err := Run([]string{"proof", repo, "--format", "markdown", "--output", output}, "0.8.6-test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, stdout.Bytes()) || !strings.Contains(string(written), "# Reconc Proof Bundle") || !strings.Contains(string(written), bundle.Candidate.Fingerprint) {
		t.Fatalf("Markdown file/stdout drift:\n%s", written)
	}
}

func TestProofBlockedStillEmitsVerifiableArtifact(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	if err := os.WriteFile(filepath.Join(repo, "docs", "tasks.md"), []byte("# TASK Control Plane\n\n## Active\n\n## Queue\n\n- [ ] 001 Pending -> tasks/001-pending.md\n\n## Blocked\n\n## Done\n"), 0o644); err != nil {
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "docs", "tasks.md"), []byte("# TASK Control Plane\n\n## Active\n\n## Queue\n\n- [ ] 001 Pending -> tasks/001-pending.md\n\n## Blocked\n\n## Done\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	err := Run([]string{"proof", repo}, "0.8.6-test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("blocked proof exit = %d, error = %v", ExitCode(err), err)
	}
	var bundle proofbundle.Bundle
	if decodeErr := json.Unmarshal(stdout.Bytes(), &bundle); decodeErr != nil {
		t.Fatalf("blocked proof was not emitted: %v", decodeErr)
	}
	if bundle.OK || proofbundle.Verify(&bundle) != nil {
		t.Fatalf("blocked artifact invalid: %#v", bundle)
	}
}

func TestProofRejectsInvalidFlagsAndOutputFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	for _, arguments := range [][]string{{"proof", "--format"}, {"proof", "--format", "html"}, {"proof", "--output"}, {"proof", "--fictional"}} {
		if err := Run(arguments, "test", &stdout, &stderr); err == nil || ExitCode(err) != 1 {
			t.Fatalf("arguments %v were accepted: %v", arguments, err)
		}
	}
	repo := makeCheckRepo(t, "rules: []\n")
	err := Run([]string{"proof", repo, "--output", t.TempDir()}, "test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "write output") {
		t.Fatalf("output failure = %v", err)
	}
}

func TestProofHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"proof", "--help"}, "test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Usage: reconc proof", "proof verify FILE", "--format json|markdown", "--output PATH", "read-only", "Exit 0 = pass, 2 = blocked"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("help omitted %q", expected)
		}
	}
}

func TestProofVerifyValidBundleAndFreshRepositoryBinding(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	initGitRepo(t, repo)
	commitProofVerifyFixture(t, repo)
	bundle, err := proofbundle.Generate(repo, "test")
	if err != nil {
		t.Fatal(err)
	}
	body, err := proofbundle.MarshalJSON(bundle)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "proof.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"proof", "verify", path, "--repo", repo, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var report proofVerificationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "valid" || !report.IntegrityValid || report.LocalCandidateMatch == nil || !*report.LocalCandidateMatch || len(report.Mismatches) != 0 {
		t.Fatalf("verification report = %+v", report)
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# drift\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	err = Run([]string{"proof", "verify", path, "--repo", repo, "--json"}, "test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("candidate drift exit = %d, %v", ExitCode(err), err)
	}
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil || report.Status != "candidate-mismatch" || report.LocalCandidateMatch == nil || *report.LocalCandidateMatch {
		t.Fatalf("candidate drift report = %+v, %v", report, decodeErr)
	}
}

func commitProofVerifyFixture(t *testing.T, repo string) {
	t.Helper()
	for _, arguments := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "fixture"}} {
		command := exec.Command("git", arguments...)
		command.Dir = repo
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", arguments, err, output)
		}
	}
}

func TestProofVerifyReportsMalformedUnsupportedAndUnsafeInputs(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	bundle, err := proofbundle.Generate(repo, "test")
	if err != nil {
		t.Fatal(err)
	}
	body, err := proofbundle.MarshalJSON(bundle)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	valid := filepath.Join(root, "valid.json")
	if err := os.WriteFile(valid, body, 0o600); err != nil {
		t.Fatal(err)
	}
	unsupported := filepath.Join(root, "unsupported.json")
	if err := os.WriteFile(unsupported, bytes.Replace(body, []byte(`"format_version": "1"`), []byte(`"format_version": "999"`), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	truncated := filepath.Join(root, "truncated.json")
	if err := os.WriteFile(truncated, body[:len(body)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "link.json")
	if err := os.Symlink(valid, symlink); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, status string
	}{
		{unsupported, "unsupported"},
		{truncated, "malformed"},
		{symlink, "unsafe-input"},
	} {
		var stdout, stderr bytes.Buffer
		err := Run([]string{"proof", "verify", test.path, "--json"}, "test", &stdout, &stderr)
		if err == nil || ExitCode(err) != 1 {
			t.Fatalf("%s exit = %d, %v", test.status, ExitCode(err), err)
		}
		var report proofVerificationReport
		if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil || report.Status != test.status || report.IntegrityValid || !strings.Contains(report.Trust, "not author identity") {
			t.Fatalf("%s report = %+v, %v", test.status, report, decodeErr)
		}
	}
}

func TestProofVerifyPreservesValidBlockingDecisionAndExit(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	board := "# TASK Control Plane\n\n## Active\n\n## Queue\n\n- [ ] 001 Pending -> tasks/001-pending.md\n\n## Blocked\n\n## Done\n"
	if err := os.WriteFile(filepath.Join(repo, "docs", "tasks.md"), []byte(board), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle, err := proofbundle.Generate(repo, "test")
	if err != nil {
		t.Fatal(err)
	}
	body, err := proofbundle.MarshalJSON(bundle)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "blocked.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err = Run([]string{"proof", "verify", path, "--json"}, "test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("blocking verify exit = %d, %v", ExitCode(err), err)
	}
	var report proofVerificationReport
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil || report.Status != "blocking" || !report.IntegrityValid || report.Decision != "block" {
		t.Fatalf("blocking report = %+v, %v", report, decodeErr)
	}
}

func TestProofVerifyHelpAndArgumentContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"proof", "verify", "--help"}, "test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"proof verify FILE", "--repo REPO", "--json", "unsigned self-digest", "Exit 0"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("verify help omitted %q", expected)
		}
	}
	for _, args := range [][]string{{"proof", "verify"}, {"proof", "verify", "a", "b"}, {"proof", "verify", "a", "--repo"}, {"proof", "verify", "a", "--unknown"}} {
		if err := Run(args, "test", &stdout, &stderr); err == nil || ExitCode(err) != 1 {
			t.Fatalf("arguments %v accepted: %v", args, err)
		}
	}
}
