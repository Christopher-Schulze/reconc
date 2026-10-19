package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequireEvidenceReportsEachCaptureSetFailureOnce(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", `rules:
  - id: evidence
    kind: require_evidence
    when_paths: ['src/**']
    evidence:
      - file: docs/review.md
        must_exist: true
    mode: block
    message: review evidence required
`)
	inputs := ExecutionInputs{}
	for index := 0; index < 300; index++ {
		inputs.WritePaths = append(inputs.WritePaths, fmt.Sprintf("src/file%03d.go", index))
	}
	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionBlock || len(report.Violations) != 1 {
		t.Fatalf("missing evidence must block once: decision=%s violations=%+v", report.Decision, report.Violations)
	}
	explanation := report.Violations[0].Explanation
	failures := explanation[strings.Index(explanation, "Failures: "):]
	if strings.Count(failures, "docs/review.md") != 1 || strings.Contains(failures, "omitted") {
		t.Fatalf("one capture set must report its failure once: %s", failures)
	}
}

func TestRequireFreshFileStatsEachCaptureSetOnce(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", `rules:
  - id: fresh
    kind: require_fresh_file
    when_paths: ['src/**']
    required_files:
      - path: docs/generated.md
    mode: block
    message: regenerate docs
`)
	required := filepath.Join(repo, "docs", "generated.md")
	if err := os.MkdirAll(filepath.Dir(required), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(required, []byte("generated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stats := 0
	previous := statEvidencePath
	t.Cleanup(func() { statEvidencePath = previous })
	statEvidencePath = func(path string) (os.FileInfo, string, bool, error) {
		if filepath.Base(path) == "generated.md" {
			stats++
		}
		return previous(path)
	}
	inputs := ExecutionInputs{}
	for index := 0; index < 300; index++ {
		inputs.WritePaths = append(inputs.WritePaths, fmt.Sprintf("src/file%03d.go", index))
	}
	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass {
		t.Fatalf("present required file must pass: decision=%s violations=%+v", report.Decision, report.Violations)
	}
	if stats == 0 || stats > 3 {
		t.Fatalf("required file identity probes = %d, want one snapshot read for the shared capture set", stats)
	}
}
