package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkBuildDoctorDeepReport(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	repo := b.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# benchmark\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		b.Fatal(err)
	}
	policy := "rules:\n  - id: benchmark-deny\n    kind: deny_write\n    paths: ['generated/**']\n    mode: block\n    message: generated files are read-only\n"
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(policy), 0o644); err != nil {
		b.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"compile", repo}, "benchmark", &stdout, &stderr); err != nil {
		b.Fatalf("compile benchmark repo: %v: %s", err, stderr.String())
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := buildDoctorDeepReport(repo); err != nil {
			b.Fatal(err)
		}
	}
}
