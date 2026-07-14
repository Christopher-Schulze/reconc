package assurance

import (
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestGoConcurrencyBoundaryFindsChangedProductionGoStatements(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "worker"), 0o755); err != nil {
		t.Fatal(err)
	}
	production := "package worker\n\nfunc Start() { go work() }\nfunc work() {}\n"
	if err := os.WriteFile(filepath.Join(root, "worker", "worker.go"), []byte(production), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "worker", "worker_test.go"), []byte(production), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := policy.AssuranceGate{
		ID: "go-concurrency", Type: policy.AssuranceGoConcurrency,
		ScanPaths: []string{"**/*.go"}, ExcludePaths: []string{"**/*_test.go"},
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{
		ChangedPaths: []string{"worker/worker.go", "worker/worker_test.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || len(findings[0].Paths) != 1 || findings[0].Paths[0] != "worker/worker.go" {
		t.Fatalf("unexpected Go concurrency findings: %+v", findings)
	}
}

func TestGoConcurrencyBoundaryFailsClosedOnInvalidGo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package broken\nfunc"), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := policy.AssuranceGate{ID: "go-concurrency", Type: policy.AssuranceGoConcurrency, ScanPaths: []string{"**/*.go"}}
	if _, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"broken.go"}}); err == nil {
		t.Fatal("invalid changed Go source must fail closed")
	}
}
