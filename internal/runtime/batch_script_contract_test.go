package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestBatchScriptBlockingProcessCannotReportAllModesPassing(t *testing.T) {
	repo, _ := setupBatchContractRepo(t,
		`{"results":[{"mode":"mode-a","failures":[]},{"mode":"mode-b","failures":[]}]}`,
		2,
	)
	report, err := CheckRepoPolicy(repo, ExecutionInputs{WritePaths: []string{"src/main.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionBlock || report.BlockingViolationCount != 2 {
		t.Fatalf("contradictory batch result = decision %s, blocking %d, violations=%+v", report.Decision, report.BlockingViolationCount, report.Violations)
	}
}

func TestBatchScriptMissingModeFallsBackToPerRuleExecution(t *testing.T) {
	repo, counter := setupBatchContractRepo(t,
		`{"results":[{"mode":"mode-a","failures":[]}]}`,
		0,
	)
	report, err := CheckRepoPolicy(repo, ExecutionInputs{WritePaths: []string{"src/main.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass {
		t.Fatalf("missing-mode fallback decision = %s, violations=%+v", report.Decision, report.Violations)
	}
	body, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "xxx" {
		t.Fatalf("script invocations = %q, want one batch plus two per-rule calls", body)
	}
}

func setupBatchContractRepo(t *testing.T, batchJSON string, batchExit int) (string, string) {
	t.Helper()
	repo := t.TempDir()
	counter := filepath.Join(repo, "audits", "counter")
	writeFile(t, repo, "AGENTS.md", "# project\n")
	script := `#!/bin/sh
set -eu
printf x >> "__COUNTER__"
if [ "${1:-}" = "--batch-json" ]; then
  printf '%s\n' '__BATCH_JSON__'
  exit __BATCH_EXIT__
fi
exit 0
`
	script = strings.NewReplacer(
		"__COUNTER__", counter,
		"__BATCH_JSON__", batchJSON,
		"__BATCH_EXIT__", itoa(batchExit),
	).Replace(script)
	writeFile(t, repo, "audits/run-workflow-audit", script)
	if err := os.Chmod(filepath.Join(repo, "audits", "run-workflow-audit"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "policies/rules.yml", `rules:
  - id: mode-a
    kind: require_script
    when_paths: ['src/**']
    script: audits/run-workflow-audit
    args: ['mode-a']
    mode: block
    message: mode a
  - id: mode-b
    kind: require_script
    when_paths: ['src/**']
    script: audits/run-workflow-audit
    args: ['mode-b']
    mode: block
    message: mode b
`)
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	return repo, counter
}
