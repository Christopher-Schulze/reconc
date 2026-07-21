package agentsession

import (
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func BenchmarkRepositoryRunStopHotpath(b *testing.B) {
	repo := setupStopBenchmarkRepo(b)
	writeRunControlBenchmarkTask(b, repo)
	if _, err := SetRepositoryRun(repo, true); err != nil {
		b.Fatal(err)
	}
	payload := []byte(`{"session_id":"benchmark","runtime":"codex"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		if _, err := MutateSessionState(repo, "benchmark", func(state SessionState) SessionState {
			state.RepositoryRunAwaiting = false
			state.RepositoryRunNudges = 0
			return state
		}); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		result := RunStop(repo, payload)
		if result.ExitCode != 0 || result.Stdout == "" || result.Stderr != "" {
			b.Fatalf("unexpected Stop result: %+v", result)
		}
	}
}

func writeRunControlBenchmarkTask(tb testing.TB, repo string) {
	tb.Helper()
	files := map[string]string{
		".reconc.yml":                 "task_lifecycle:\n  profile: sections-v1\nrules: []\n",
		"docs/tasks.md":               "# Tasks\n\n## Active\n\n- [~] 001 Benchmark -> tasks/001-benchmark.md\n\n## Queue\n\n## Blocked\n\n## Done\n",
		"docs/tasks/001-benchmark.md": "# TASK 001: Benchmark\n\n## Why\n\nMeasure Stop.\n\n## Acceptance\n\n- Measured.\n\n## Sub-Tasks\n\n- [~] Measure hotpath.\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n",
	}
	for rel, body := range files {
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	if _, err := compiler.CompileRepoPolicy(repo, "benchmark"); err != nil {
		tb.Fatalf("compile task policy: %v", err)
	}
}
