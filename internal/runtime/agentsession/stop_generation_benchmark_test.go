package agentsession

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/runtime"
)

func BenchmarkStopGenerationOracle(b *testing.B) {
	b.Run("tracked-dirty-exact", func(b *testing.B) {
		repo := setupStopBenchmarkRepo(b)
		if err := os.WriteFile(filepath.Join(repo, "src", "a.go"), bytes.Repeat([]byte{'d'}, stopGenerationMinBytes), 0o644); err != nil {
			b.Fatal(err)
		}
		benchmarkExactStopGenerationFallback(b, repo, "tracked-dirty-exact")
	})
	b.Run("tracked-dirty", func(b *testing.B) {
		repo := setupStopBenchmarkRepo(b)
		if err := os.WriteFile(filepath.Join(repo, "src", "a.go"), bytes.Repeat([]byte{'d'}, stopGenerationMinBytes), 0o644); err != nil {
			b.Fatal(err)
		}
		benchmarkWarmStopGeneration(b, repo, "tracked-dirty")
	})
	b.Run("untracked-directory", func(b *testing.B) {
		repo := setupStopBenchmarkRepo(b)
		for index := 0; index < stopGenerationMinEntries; index++ {
			path := filepath.Join(repo, "scratch", fmt.Sprintf("%03d.txt", index))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("scratch\n"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
		benchmarkWarmStopGeneration(b, repo, "untracked-directory")
	})
	b.Run("large-repository", func(b *testing.B) {
		repo := setupStopBenchmarkRepo(b)
		for index := 0; index < 2_000; index++ {
			path := filepath.Join(repo, "large", fmt.Sprintf("%04d.txt", index))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("tracked\n"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
		benchmarkGit(b, repo, "add", "large")
		benchmarkGit(b, repo, "commit", "-m", "large fixture", "--quiet")
		if err := os.WriteFile(filepath.Join(repo, "large", "0000.txt"), bytes.Repeat([]byte{'d'}, stopGenerationMinBytes), 0o644); err != nil {
			b.Fatal(err)
		}
		benchmarkWarmStopGeneration(b, repo, "large-repository")
	})
	b.Run("linked-worktree", func(b *testing.B) {
		repo := setupStopBenchmarkRepo(b)
		worktree := filepath.Join(b.TempDir(), "linked")
		benchmarkGit(b, repo, "worktree", "add", "--quiet", "-b", "generation-benchmark", worktree, "HEAD")
		if err := os.WriteFile(filepath.Join(worktree, "src", "a.go"), bytes.Repeat([]byte{'d'}, stopGenerationMinBytes), 0o644); err != nil {
			b.Fatal(err)
		}
		benchmarkWarmStopGeneration(b, worktree, "linked-worktree")
	})
	b.Run("dirty-submodule", func(b *testing.B) {
		repo := setupStopBenchmarkRepo(b)
		child := b.TempDir()
		benchmarkGit(b, child, "init", "--quiet")
		benchmarkGit(b, child, "config", "user.email", "bench@reconc.dev")
		benchmarkGit(b, child, "config", "user.name", "reconc benchmark")
		if err := os.WriteFile(filepath.Join(child, "state.txt"), []byte("clean\n"), 0o644); err != nil {
			b.Fatal(err)
		}
		benchmarkGit(b, child, "add", "state.txt")
		benchmarkGit(b, child, "commit", "-m", "child fixture", "--quiet")
		benchmarkGit(b, repo, "-c", "protocol.file.allow=always", "submodule", "add", "--quiet", child, "modules/child")
		benchmarkGit(b, repo, "commit", "-am", "submodule fixture", "--quiet")
		if err := os.WriteFile(filepath.Join(repo, "modules", "child", "state.txt"), []byte("dirty\n"), 0o644); err != nil {
			b.Fatal(err)
		}
		benchmarkExactStopGenerationFallback(b, repo, "dirty-submodule")
	})
	b.Run("concurrent", func(b *testing.B) {
		repo := setupStopBenchmarkRepo(b)
		if err := os.WriteFile(filepath.Join(repo, "src", "a.go"), bytes.Repeat([]byte{'d'}, stopGenerationMinBytes), 0o644); err != nil {
			b.Fatal(err)
		}
		state, cache, evaluator := warmStopGenerationBenchmark(b, repo, "concurrent")
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(parallel *testing.PB) {
			for parallel.Next() {
				result, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil)
				if err != nil || !result.GenerationHit {
					b.Fatalf("concurrent generation hit=%t err=%v", result.GenerationHit, err)
				}
			}
		})
	})
}

func BenchmarkStopAttemptIdentityReuse(b *testing.B) {
	repo := setupStopBenchmarkRepo(b)
	target := filepath.Join(repo, "src", "a.go")
	if err := os.WriteFile(target, bytes.Repeat([]byte{'d'}, stopGenerationMinBytes), 0o644); err != nil {
		b.Fatal(err)
	}
	state, err := InitializeSessionState(repo, "attempt-identity")
	if err != nil {
		b.Fatal(err)
	}
	state = AppendWritePath(state, "src/a.go")
	taskSnapshot, err := captureStopTaskSnapshot(repo)
	if err != nil {
		b.Fatal(err)
	}
	gitSnapshot := stopPolicyGitSnapshotFor(repo)
	var hashBytes int64
	var hashReads int
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		scanCache := &stopPolicyScanCache{}
		first := stopPolicyFingerprintInputForSnapshotWithScan(
			repo, state, gitSnapshot, taskSnapshot, stopGenerationCapture{}, scanCache,
		)
		second := stopPolicyFingerprintInputForSnapshotWithScan(
			repo, state, gitSnapshot, taskSnapshot, stopGenerationCapture{}, scanCache,
		)
		if hashStopPolicyFingerprintInput(first) != hashStopPolicyFingerprintInput(second) {
			b.Fatal("unchanged attempt identities diverged")
		}
		hashBytes += scanCache.metrics.contentHashBytes
		hashReads += scanCache.metrics.contentHashReads
	}
	b.ReportMetric(float64(hashBytes)/float64(b.N), "hash-B/op")
	b.ReportMetric(float64(hashReads)/float64(b.N), "hash-read/op")
}

func benchmarkExactStopGenerationFallback(b *testing.B, repo, sessionID string) {
	b.Helper()
	state, err := InitializeSessionState(repo, sessionID)
	if err != nil {
		b.Fatal(err)
	}
	evaluator := runtime.NewEvaluator()
	if _, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, nil, nil); err != nil {
		b.Fatal(err)
	}
	state, err = LoadSessionState(repo, sessionID)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, nil, nil)
		if err != nil || result.GenerationHit {
			b.Fatalf("exact fallback generation hit=%t err=%v", result.GenerationHit, err)
		}
	}
}

func benchmarkWarmStopGeneration(b *testing.B, repo, sessionID string) {
	b.Helper()
	state, cache, evaluator := warmStopGenerationBenchmark(b, repo, sessionID)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil)
		if err != nil || !result.GenerationHit {
			b.Fatalf("generation hit=%t err=%v", result.GenerationHit, err)
		}
	}
}

func warmStopGenerationBenchmark(b *testing.B, repo, sessionID string) (SessionState, *StopDecisionCache, *runtime.Evaluator) {
	b.Helper()
	state, err := InitializeSessionState(repo, sessionID)
	if err != nil {
		b.Fatal(err)
	}
	cache := NewStopDecisionCache()
	evaluator := runtime.NewEvaluator()
	if _, err := runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repo, state, evaluator, cache, nil); err != nil {
		b.Fatal(err)
	}
	state, err = LoadSessionState(repo, sessionID)
	if err != nil {
		b.Fatal(err)
	}
	return state, cache, evaluator
}

func benchmarkGit(b *testing.B, repo string, args ...string) {
	b.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		b.Fatalf("git -C %s %v: %v\n%s", repo, args, err, output)
	}
}
