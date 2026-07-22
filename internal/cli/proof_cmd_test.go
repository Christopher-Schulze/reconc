package cli

import (
	"bytes"
	"encoding/json"
	"os"
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
	for _, expected := range []string{"Usage: reconc proof", "--format json|markdown", "--output PATH", "read-only", "Exit 0 = pass, 2 = blocked"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("help omitted %q", expected)
		}
	}
}
