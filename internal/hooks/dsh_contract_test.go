package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Runs the shipped extension against a bounded protocol peer, never a DSH host.
// Real policy decisions are separately covered by internal/cli/dsh_e2e_test.go.
func TestDSHGeneratedCompositionContract(t *testing.T) {
	runDSHContract(t, "composition")
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
	for path, content := range map[string]string{
		artifact.TargetPath: artifact.Content + "\nexport { WorkerTransport }\n",
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
	runBunContractDriver(t, []string{"RECONC_DSH_TEST_BUN=" + bun, "RECONC_DSH_TEST_PEER=" + peer},
		bun, driver, filepath.Join(repo, filepath.FromSlash(artifact.TargetPath)), repo, mode)
}
