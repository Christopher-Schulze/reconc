package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/impactlab"
)

func TestImpactComparesCandidateWithoutMutatingRepository(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	candidate := filepath.Join(t.TempDir(), "candidate.yml")
	writeCLIFile(t, filepath.Dir(candidate), filepath.Base(candidate),
		"rules:\n  - id: candidate-deny\n    kind: deny_write\n    paths: [src/**]\n    mode: block\n    message: blocked\n")
	lockPath := filepath.Join(repo, compiler.LockfileRelativePath)
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "nested", "impact.json")
	var stdout, stderr bytes.Buffer
	err = Run([]string{
		"impact", repo, "--candidate", candidate, "--write", "src/main.go",
		"--json", "--output", outputPath,
	}, "test", &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := os.ReadFile(outputPath)
	if readErr != nil || !bytes.Equal(body, stdout.Bytes()) || stderr.Len() != 0 {
		t.Fatalf("impact output = %v, equal=%t, stderr=%q", readErr, bytes.Equal(body, stdout.Bytes()), stderr.String())
	}
	var report impactlab.Report
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.Summary.DecisionChanges != 1 || report.Summary.NewlyBlockingCases != 1 ||
		len(report.Cases) != 1 || !report.Cases[0].DecisionChanged {
		t.Fatalf("impact report = %+v", report)
	}
	after, err := os.ReadFile(lockPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("impact mutated current lock: %v", err)
	}
	for _, absent := range []string{".reconc/audit.jsonl", ".reconc/run/state.bin"} {
		if _, err := os.Stat(filepath.Join(repo, absent)); !os.IsNotExist(err) {
			t.Fatalf("impact created %s: %v", absent, err)
		}
	}
}

func TestImpactExportRedactsAndImportsCorpus(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	corpusPath := filepath.Join(t.TempDir(), "corpus.json")
	var stdout, stderr bytes.Buffer
	err := Run([]string{
		"impact", "export", repo, "--write", "src/main.go",
		"--command", "curl --token sk-supersecretvalue https://example.test",
		"--complete", "all", "--output", corpusPath,
	}, "test", &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := os.ReadFile(corpusPath)
	if readErr != nil || !bytes.Equal(body, stdout.Bytes()) || bytes.Contains(body, []byte("supersecretvalue")) {
		t.Fatalf("corpus export = %v, equal=%t, body=%s", readErr, bytes.Equal(body, stdout.Bytes()), body)
	}
	corpus, err := impactlab.DecodeCorpus(body)
	if err != nil {
		t.Fatal(err)
	}
	if corpus.Completeness.CompleteReplay || corpus.Completeness.RedactionCount == 0 {
		t.Fatalf("corpus completeness = %+v", corpus.Completeness)
	}
	candidate := filepath.Join(t.TempDir(), "candidate.yml")
	if err := os.WriteFile(candidate, []byte("rules:\n  - id: docs-only\n    kind: deny_write\n    paths: [docs/**]\n    message: docs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := Run([]string{
		"impact", repo, "--candidate", candidate, "--corpus", corpusPath, "--json",
	}, "test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "unmatched in this corpus") ||
		!strings.Contains(stdout.String(), `"corpus_unmatched_rules"`) {
		t.Fatalf("imported impact output = %s", stdout.String())
	}
}

func TestImpactRejectsUnsafeCandidateAndInvalidOptions(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	root := t.TempDir()
	target := filepath.Join(root, "candidate.yml")
	link := filepath.Join(root, "candidate-link.yml")
	if err := os.WriteFile(target, []byte("rules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	tests := [][]string{
		{"impact", repo, "--candidate", link, "--write", "src/main.go"},
		{"impact", repo, "--candidate", target, "--pack", "default", "--write", "src/main.go"},
		{"impact", repo, "--candidate", target},
		{"impact", "export", repo, "--write", "src/main.go", "--complete", "unknown"},
	}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if err := Run(args, "test", &stdout, &stderr); err == nil || ExitCode(err) != 1 {
			t.Fatalf("impact accepted %v: %v", args, err)
		}
	}
}

func TestImpactHelpAndExportHelpAreCanonical(t *testing.T) {
	for _, args := range [][]string{{"impact", "--help"}, {"help", "impact", "export"}} {
		var stdout, stderr bytes.Buffer
		if err := Run(args, "test", &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"impact", "--output"} {
			if !strings.Contains(stdout.String(), expected) {
				t.Errorf("%v help omitted %q:\n%s", args, expected, stdout.String())
			}
		}
	}
}
