package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Runs the shipped extension against a bounded protocol peer, never a DSH host.
// Real policy decisions are separately covered by internal/cli/dsh_e2e_test.go.
func TestDSHGeneratedCompositionContract(t *testing.T) {
	runDSHContract(t, "composition")
}

func TestDSHWorkerLifecycleAndResourceContracts(t *testing.T) {
	for _, mode := range []string{"restart", "crash", "cancel", "deadline", "shutdown", "priority", "bytes", "count", "json", "observations", "decision-limit", "session-cancel", "session-limit", "advisory-setup", "advisory-evaluation", "advisory-stop", "advisory-combined"} {
		t.Run(mode, func(t *testing.T) { runDSHContract(t, mode) })
	}
}

func runDSHContract(t *testing.T, mode string) {
	t.Helper()
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("Bun is required for DSH adapter contracts: %v", err)
	}
	repo := t.TempDir()
	artifact, err := Generate(KindDSH)
	if err != nil {
		t.Fatal(err)
	}
	content := artifact.Content
	if mode == "deadline" {
		content = strings.Replace(content, `"dsh-post-tool-use":{"timeoutMilliseconds":5000`, `"dsh-post-tool-use":{"timeoutMilliseconds":150`, 1)
		if content == artifact.Content {
			t.Fatal("deadline fixture did not find the generated budget")
		}
	}
	if mode == "bytes" {
		content = strings.Replace(content, "const maxQueuedBytes = 128 * 1024 * 1024", "const maxQueuedBytes = 64 * 1024", 1)
		content = strings.Replace(content, "const maxRequestBytes = 64 * 1024 * 1024 + 64 * 1024", "const maxRequestBytes = 32 * 1024", 1)
		if strings.Contains(content, "128 * 1024 * 1024") || strings.Contains(content, "64 * 1024 * 1024") {
			t.Fatal("byte fixture did not replace the generated budgets")
		}
	}
	for path, content := range map[string]string{
		artifact.TargetPath: content + "\nexport { WorkerTransport, jsonBytes }\n",
		WrapperPath:         "#!/bin/sh\nexec \"$RECONC_DSH_TEST_BUN\" \"$RECONC_DSH_TEST_PEER\"\n",
	} {
		target := filepath.Join(repo, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	driver, err := filepath.Abs("testdata/dsh-contract.mjs")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := filepath.Abs("testdata/dsh-worker-peer.mjs")
	if err != nil {
		t.Fatal(err)
	}
	runBunContractDriver(t, []string{"RECONC_DSH_TEST_BUN=" + bun, "RECONC_DSH_TEST_PEER=" + peer, "RECONC_DSH_TEST_LOG=" + filepath.Join(repo, "worker.jsonl"), "RECONC_DSH_TEST_MODE=" + mode},
		bun, driver, filepath.Join(repo, filepath.FromSlash(artifact.TargetPath)), repo, mode)
	if mode == "observations" {
		body, err := os.ReadFile(filepath.Join(repo, "worker.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("DSH wire measurements (metadata only):\n%s", body)
	}
}
