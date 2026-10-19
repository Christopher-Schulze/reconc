package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestBatchScriptTimeoutDoesNotRetryEveryRule(t *testing.T) {
	repo := batchTimeoutFixture(t)
	report, err := CheckRepoPolicy(repo, ExecutionInputs{WritePaths: []string{"src/main.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionBlock || len(report.Violations) != 2 || report.cacheableDecision {
		t.Fatalf("timeout did not produce two non-cacheable failures: %+v", report)
	}
	for _, violation := range report.Violations {
		if !strings.Contains(violation.Explanation, "timed out") {
			t.Fatalf("timeout lost from failure: %+v", violation)
		}
	}
	body, err := os.ReadFile(filepath.Join(repo, "counter"))
	if err != nil || string(body) != "x" {
		t.Fatalf("timeout ran more than one process: %q, err=%v", body, err)
	}
}

func batchTimeoutFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, "audits/run-workflow-audit", "#!/bin/sh\nprintf x >> counter\nsleep 3\n")
	if err := os.Chmod(filepath.Join(repo, "audits/run-workflow-audit"), 0o755); err != nil {
		t.Fatal(err)
	}
	var rules strings.Builder
	rules.WriteString("rules:\n")
	for _, id := range []string{"a", "b"} {
		fmt.Fprintf(&rules, "  - id: %s\n    kind: require_script\n    when_paths: ['src/**']\n    script: audits/run-workflow-audit\n    args: ['%s']\n    timeout_sec: 1\n    kill_timeout_sec: 1\n    mode: block\n    message: audit\n", id, id)
	}
	writeFile(t, repo, "policies/rules.yml", rules.String())
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	return repo
}
