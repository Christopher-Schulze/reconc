package completiongate_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/completiongate"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

// BenchmarkCompletionPolicy256 measures the complete gate in a real non-Git
// repository. Each call owns a fresh evaluator; only its two plan loads can reuse.
func BenchmarkCompletionPolicy256(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	b.Setenv(agentsession.StateRootEnv, b.TempDir())
	repo := b.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# fixture\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	var policyText strings.Builder
	if _, err := policyText.WriteString("rules:\n"); err != nil {
		b.Fatal(err)
	}
	for index := range 256 {
		if _, err := fmt.Fprintf(&policyText, "  - id: generated-%03d\n    kind: deny_write\n    paths: ['generated/%03d/**']\n    mode: block\n    message: generated output is read-only\n", index, index); err != nil {
			b.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(policyText.String()), 0o644); err != nil {
		b.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "completion-benchmark"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		report, err := completiongate.Evaluate(repo, completiongate.Options{})
		if err != nil {
			b.Fatal(err)
		}
		if !report.OK {
			b.Fatalf("completion blocked: %+v", report.Checks)
		}
	}
}
