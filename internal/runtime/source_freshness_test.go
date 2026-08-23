package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestRuntimePlanFreshnessDetectsSourceSetChanges(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules: []\n")
	evaluator := NewEvaluator()
	if _, err := evaluator.loadFreshRuntimePlan(repo); err != nil {
		t.Fatal(err)
	}
	newPolicy := filepath.Join(repo, "policies", "new.yml")
	if err := os.WriteFile(newPolicy, []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := evaluator.loadFreshRuntimePlan(repo); err == nil || !freshnessInvalidationError(err) {
		t.Fatalf("added policy source was not detected: %v", err)
	}
}

func TestRuntimePlanFreshnessDetectsConfiguredIncludeAdditions(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "include:\n  - custom/*.yml\n", "rules: []\n")
	evaluator := NewEvaluator()
	if _, err := evaluator.loadFreshRuntimePlan(repo); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(repo, "custom", "new.yml")
	if err := os.MkdirAll(filepath.Dir(custom), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(custom, []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := evaluator.loadFreshRuntimePlan(repo); err == nil || !freshnessInvalidationError(err) {
		t.Fatalf("added configured source was not detected: %v", err)
	}
}

func TestRuntimePlanFreshnessDetectsSameSizeContentChange(t *testing.T) {
	withRECONCHome(t)
	original := "rules:\n  - id: abc\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: one\n"
	updated := strings.Replace(original, "message: one", "message: two", 1)
	if len(original) != len(updated) {
		t.Fatalf("test fixture changed size: %d != %d", len(original), len(updated))
	}
	repo := makeRepo(t, "# project\n", "", original)
	evaluator := NewEvaluator()
	if _, err := evaluator.loadFreshRuntimePlan(repo); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(repo, "policies", "rules.yml")
	if err := os.WriteFile(policyPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := evaluator.loadFreshRuntimePlan(repo); err == nil || !freshnessInvalidationError(err) {
		t.Fatalf("same-size source content change was not detected: %v", err)
	}
}

func freshnessInvalidationError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "source_digest") || strings.Contains(err.Error(), "source_count")
}

func TestRuntimePlanFreshnessRejectsSourceSymlinkOnFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture is not portable to Windows")
	}
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules: []\n")
	evaluator := NewEvaluator()
	if _, err := evaluator.loadFreshRuntimePlan(repo); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(repo, "policies", "rules.yml")
	outside := filepath.Join(t.TempDir(), "rules.yml")
	if err := os.WriteFile(outside, []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(policyPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, policyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := evaluator.loadFreshRuntimePlan(repo); err == nil {
		t.Fatal("source symlink was accepted")
	}
}

func TestRuntimePlanCacheEvictsLeastRecentlyUsedEntries(t *testing.T) {
	withRECONCHome(t)
	evaluator := NewEvaluator()
	repos := make([]string, maxRuntimePlanCacheEntries+1)
	for index := range repos {
		repos[index] = makeRepo(t, "# project\n", "", "rules: []\n")
		if _, err := evaluator.loadFreshRuntimePlan(repos[index]); err != nil {
			t.Fatalf("load repo %d: %v", index, err)
		}
	}
	if got := len(evaluator.plans); got != maxRuntimePlanCacheEntries {
		t.Fatalf("runtime plan cache size = %d, want %d", got, maxRuntimePlanCacheEntries)
	}
	if _, ok := evaluator.plans[repos[0]]; ok {
		t.Fatal("least recently used runtime plan was not evicted")
	}
}

func BenchmarkRuntimePlanFreshnessHit(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	repo := benchmarkFreshnessRepo(b, 2)
	evaluator := NewEvaluator()
	first, err := evaluator.loadFreshRuntimePlan(repo)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		plan, err := evaluator.loadFreshRuntimePlan(repo)
		if err != nil {
			b.Fatal(err)
		}
		if plan != first {
			b.Fatal("freshness hit rebuilt the runtime plan")
		}
	}
}

func BenchmarkRuntimePlanConcurrentRoots(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	const rootCount = 4
	repositories := make([]string, rootCount)
	evaluator := NewEvaluator()
	for index := range repositories {
		repositories[index] = benchmarkFreshnessRepo(b, 2)
		if _, err := evaluator.loadFreshRuntimePlan(repositories[index]); err != nil {
			b.Fatal(err)
		}
	}
	var sequence atomic.Uint64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			root := repositories[sequence.Add(1)%rootCount]
			if _, err := evaluator.loadFreshRuntimePlan(root); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func BenchmarkRuntimePlanFreshnessColdLoad(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	repo := benchmarkFreshnessRepo(b, 1)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := NewEvaluator().loadFreshRuntimePlan(repo); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRuntimePlanFreshnessSingleSourceEdit(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	repo := benchmarkFreshnessRepo(b, 1)
	evaluator := NewEvaluator()
	plan, err := evaluator.loadFreshRuntimePlan(repo)
	if err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(repo, "policies", "rules.yml")
	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		body := "rules: []\n# edit\n"
		if index%2 == 0 {
			body = "rules: []\n# edit2\n"
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
		if _, err := observeRuntimeSourceFreshness(repo, plan); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRuntimePlanFreshnessConfigEdit(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	repo := benchmarkFreshnessRepo(b, 2)
	evaluator := NewEvaluator()
	plan, err := evaluator.loadFreshRuntimePlan(repo)
	if err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(repo, ".reconc.yml")
	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		body := "include:\n  - policies/extra/*.yml\n# edit-a\n"
		if index%2 == 0 {
			body = "include:\n  - policies/extra/*.yml\n# edit-b\n"
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
		if _, err := observeRuntimeSourceFreshness(repo, plan); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRuntimePlanFreshnessIncludeSetEdit(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	repo := benchmarkFreshnessRepo(b, 2)
	evaluator := NewEvaluator()
	plan, err := evaluator.loadFreshRuntimePlan(repo)
	if err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(repo, "policies", "extra", "dynamic.yml")
	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		if index%2 == 0 {
			if err := os.WriteFile(path, []byte("rules: []\n"), 0o644); err != nil {
				b.Fatal(err)
			}
		} else if err := os.Remove(path); err != nil {
			b.Fatal(err)
		}
		if _, err := observeRuntimeSourceFreshness(repo, plan); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRuntimePlanFreshnessLargeSourceSet(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	repo := benchmarkFreshnessRepo(b, 128)
	evaluator := NewEvaluator()
	first, err := evaluator.loadFreshRuntimePlan(repo)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		plan, err := evaluator.loadFreshRuntimePlan(repo)
		if err != nil {
			b.Fatal(err)
		}
		if plan != first {
			b.Fatal("large-source freshness hit rebuilt the runtime plan")
		}
	}
}

func benchmarkFreshnessRepo(b *testing.B, sourceCount int) string {
	b.Helper()
	repo := b.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# project\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	if sourceCount > 1 {
		if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("include:\n  - policies/extra/*.yml\n"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	for index := 0; index < sourceCount; index++ {
		name := "rules.yml"
		if index > 0 {
			name = filepath.Join("extra", "rule-"+strconv.Itoa(index)+".yml")
		}
		path := filepath.Join(repo, "policies", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("rules: []\n"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := compiler.CompileRepoPolicy(repo, "bench"); err != nil {
		b.Fatal(err)
	}
	return repo
}
