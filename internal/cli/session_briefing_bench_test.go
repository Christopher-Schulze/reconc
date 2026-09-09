package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func BenchmarkBuildSessionBriefing(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	for _, test := range []struct {
		name      string
		ruleCount int
	}{
		{name: "small", ruleCount: 2},
		{name: "large", ruleCount: 128},
	} {
		b.Run(test.name, func(b *testing.B) {
			repo := benchmarkSessionBriefingRepo(b, test.ruleCount)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				briefing := buildSessionBriefing(repo)
				if briefing["lockfile_status"] != "fresh" {
					b.Fatalf("briefing lockfile status = %v", briefing["lockfile_status"])
				}
			}
		})
	}
}

func benchmarkSessionBriefingRepo(b *testing.B, ruleCount int) string {
	b.Helper()
	repo := b.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# benchmark\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		b.Fatal(err)
	}
	var policy strings.Builder
	policy.WriteString("rules:\n")
	for index := 0; index < ruleCount; index++ {
		fmt.Fprintf(&policy, "  - id: rule-%03d\n    kind: deny_write\n    paths: ['generated/%03d/**']\n    mode: block\n    message: benchmark\n", index, index)
	}
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(policy.String()), 0o644); err != nil {
		b.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "benchmark"); err != nil {
		b.Fatal(err)
	}
	return repo
}
