package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/pathidentity"
	productruntime "reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestGatewayEvidenceCancellationStopsPolicyScript(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	repo, err := pathidentity.ResolveExisting(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeGatewayCancellationFile(t, repo, "AGENTS.md", "# fixture\n", 0o644)
	writeGatewayCancellationFile(t, repo, ".reconc.yml", `rules:
  - id: gateway-script
    kind: require_script
    when_paths: ['src/**']
    script: scripts/wait.sh
    timeout_sec: 5
    kill_timeout_sec: 1
    mode: block
    message: gateway script
`, 0o644)
	writeGatewayCancellationFile(t, repo, "scripts/wait.sh", "#!/bin/sh\nprintf started > gateway-script-started\nsleep 30\n", 0o755)
	writeGatewayCancellationFile(t, repo, "src/main.go", "package fixture\n", 0o644)
	if _, err := compiler.CompileRepoPolicy(repo, "gateway-context-test"); err != nil {
		t.Fatal(err)
	}

	arguments, err := action.ParseObjectJSON([]byte(`{"path":"src/main.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan bool, 1)
	go func() {
		marker := filepath.Join(repo, "gateway-script-started")
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, statErr := os.Stat(marker); statErr == nil {
				started <- true
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		started <- false
		cancel()
	}()

	runtimeEvaluator := productruntime.NewEvaluator()
	snapshot, err := (gatewayPolicyLoader{evaluator: runtimeEvaluator}).Load(
		context.Background(), repo,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (gatewayEvidenceProvider{evaluator: runtimeEvaluator}).Observe(
		ctx,
		snapshot,
		action.Request{Arguments: &arguments},
		action.Tool{Effect: action.Effect{Kind: action.EffectRepositoryWrite, PathFields: []string{"/path"}}},
	)
	if !<-started {
		t.Fatalf("gateway policy script did not start before cancellation: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("gateway cancellation = %v, want context canceled", err)
	}
}

func writeGatewayCancellationFile(t *testing.T, root, relative, body string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}
