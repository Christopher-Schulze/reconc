package harness_test

import (
	"os"
	"strings"
	"testing"
)

func TestBootstrapAndScaffoldKeepReceiptSourceAndRunStateContracts(t *testing.T) {
	bootstrap := readHarnessContractFile(t, "template/BOOTSTRAP.md")
	for _, required := range []string{
		"immutable, receipt-owned source template",
		"Never rename, move, or remove it during a rollout",
		"Copy the installed template to `tools/reconc/harness/<project-name>/`",
		"never overwrite, remove, or replace it merely to repeat the copy",
		"An explicit user stop is the only\nmanual `reconc run off <target-repo>` action",
		"automatically records the distinct\n`blocked_task` disable reason",
	} {
		if !strings.Contains(bootstrap, required) {
			t.Fatalf("BOOTSTRAP.md is missing contract text %q", required)
		}
	}
	if strings.Contains(bootstrap, "rename `tools/reconc/harness/template/`") ||
		strings.Contains(bootstrap, "on explicit stop or a real blocker") {
		t.Fatal("BOOTSTRAP.md retains the destructive copy or blocker run-off instruction")
	}

	start := readHarnessContractFile(t, "template/repo-root-scaffold/start.md")
	if strings.Contains(start, "on explicit user stop or a real blocker") ||
		!strings.Contains(start, "do not\nissue `run off` for a blocker") {
		t.Fatal("scaffold start guide does not distinguish explicit stop from blocked TASK state")
	}

	for _, path := range []string{
		"../skills/reconc/references/workflow-and-evidence.md",
		"../internal/agentguide/guide.md",
		"../docs/commands.md",
		"../docs/documentation.md",
	} {
		guidance := readHarnessContractFile(t, path)
		if strings.Contains(guidance, "without silently disabling it") ||
			strings.Contains(guidance, "on explicit stop or a real blocker") {
			t.Fatalf("guidance %s retains the stale run-state contract", path)
		}
	}
	workflow := readHarnessContractFile(t, "../skills/reconc/references/workflow-and-evidence.md")
	if !strings.Contains(workflow, "only an explicit user stop authorizes `run off`") {
		t.Fatal("portable workflow guidance does not match the run-state contract")
	}
}

func readHarnessContractFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read contract file %s: %v", path, err)
	}
	return string(body)
}
