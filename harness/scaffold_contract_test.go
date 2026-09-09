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

func TestTaskCompletionLoopIsBoundedByAcceptanceAndRequiredGates(t *testing.T) {
	workflow := readHarnessContractFile(t, "template/repo-root-scaffold/docs/task-loop-workflow.md")
	agents := readHarnessContractFile(t, "template/repo-root-scaffold/AGENTS.md")
	for _, source := range []struct {
		name string
		body string
	}{
		{name: "task-loop-workflow.md", body: workflow},
		{name: "AGENTS.md", body: agents},
	} {
		t.Run(source.name, func(t *testing.T) {
			for _, forbidden := range []string{
				"If there is ANY potential work - ALWAYS do it",
				"nothing left to fix or improve",
				"until everything passes this honest, hard Reality-Check and there is nothing left to do",
			} {
				if strings.Contains(source.body, forbidden) {
					t.Fatalf("%s retains unbounded continuation text %q", source.name, forbidden)
				}
			}
		})
	}

	scenarios := []struct {
		name     string
		required []string
	}{
		{
			name: "acceptance and optional improvement",
			required: []string{
				"explicit acceptance",
				"required verification",
				"scoped review",
				"optional improvement belongs in the",
				"current TASK only when its acceptance explicitly includes it",
			},
		},
		{
			name: "failed required gate",
			required: []string{
				"failed required gate",
				"real fix",
				"never permits bypassing safety, test integrity",
			},
		},
		{
			name: "unrelated finding",
			required: []string{
				"separate visible proposal or queued TASK",
				"do not silently expand this",
				"unrelated proposals remain",
				"visible for later prioritization",
			},
		},
		{
			name: "empty in-scope queue",
			required: []string{
				"empty queue of",
				"in-scope findings is a valid terminal state",
				"When acceptance, required",
				"verification, and scoped review pass",
			},
		},
		{
			name: "explicit user stop",
			required: []string{
				"explicit user stop pauses the TASK and never certifies it",
			},
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			for _, required := range scenario.required {
				if !strings.Contains(workflow, required) {
					t.Fatalf("workflow is missing %q", required)
				}
			}
		})
	}

	for _, required := range []string{
		"Reality Check",
		"Loop` field",
		"promote-task-done",
		"remains blocked unless this field is present",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("workflow dropped required completion gate text %q", required)
		}
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
