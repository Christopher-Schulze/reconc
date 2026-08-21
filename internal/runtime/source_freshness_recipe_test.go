package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestRuntimeFreshnessObserverUsesCachedRecipeWithoutDecodingConfig(t *testing.T) {
	withRECONCHome(t)
	repo := makeFreshnessRecipeRepo(t)
	evaluator := NewEvaluator()
	plan, err := evaluator.loadFreshRuntimePlan(repo)
	if err != nil {
		t.Fatal(err)
	}
	cached := evaluator.plans[repo]
	stable, err := observeRuntimeSourceFreshness(repo, plan)
	if err != nil || stable != cached.freshness {
		t.Fatalf("stable observation = (%x, %v), want %x", stable, err, cached.freshness)
	}

	writeFile(t, repo, ".reconc.yml", "include: [\n")
	changed, err := observeRuntimeSourceFreshness(repo, plan)
	if err != nil {
		t.Fatalf("cached recipe observer decoded malformed config: %v", err)
	}
	if changed == cached.freshness {
		t.Fatal("malformed config bytes did not change freshness identity")
	}
	if _, err := evaluator.loadFreshRuntimePlan(repo); err == nil || !strings.Contains(err.Error(), "invalid yaml") {
		t.Fatalf("full reload did not fail closed on malformed config: %v", err)
	}
}

func TestRuntimeFreshnessRecipeInvalidatesConfigMutations(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*testing.T, string)
		wantError bool
	}{
		{name: "content edit", wantError: true, mutate: func(t *testing.T, repo string) {
			writeFile(t, repo, ".reconc.yml", "include:\n  - custom/*.yml\n# changed\n")
		}},
		{name: "same-content replacement", mutate: func(t *testing.T, repo string) {
			replacement := filepath.Join(repo, "replacement.yml")
			writeFile(t, repo, "replacement.yml", "include:\n  - custom/*.yml\n")
			if err := os.Rename(replacement, filepath.Join(repo, ".reconc.yml")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "removal", wantError: true, mutate: func(t *testing.T, repo string) {
			if err := os.Remove(filepath.Join(repo, ".reconc.yml")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "second candidate", mutate: func(t *testing.T, repo string) {
			writeFile(t, repo, ".reconc.yaml", "rules: []\n")
		}},
		{name: "malformed", wantError: true, mutate: func(t *testing.T, repo string) {
			writeFile(t, repo, ".reconc.yml", "include: [\n")
		}},
		{name: "unsupported include", wantError: true, mutate: func(t *testing.T, repo string) {
			writeFile(t, repo, ".reconc.yml", "include:\n  - ../outside/*.yml\n")
		}},
		{name: "oversized", wantError: true, mutate: func(t *testing.T, repo string) {
			writeFile(t, repo, ".reconc.yml", strings.Repeat("#", maxFreshnessFileBytes+1))
		}},
		{name: "symlink", wantError: true, mutate: func(t *testing.T, repo string) {
			outside := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(outside, []byte("include:\n  - custom/*.yml\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(repo, ".reconc.yml")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(repo, ".reconc.yml")); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withRECONCHome(t)
			repo := makeFreshnessRecipeRepo(t)
			evaluator := NewEvaluator()
			first, err := evaluator.loadFreshRuntimePlan(repo)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, repo)
			second, err := evaluator.loadFreshRuntimePlan(repo)
			if test.wantError {
				if err == nil {
					t.Fatalf("mutation reused or rebuilt a stale plan: %p", second)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if second == first {
				t.Fatal("mutation reused the old runtime plan")
			}
		})
	}
}

func TestRuntimeFreshnessRecipeInvalidatesNewPrimaryConfig(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules: []\n")
	evaluator := NewEvaluator()
	if _, err := evaluator.loadFreshRuntimePlan(repo); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, ".reconc.yml", "rules: []\n")
	if _, err := evaluator.loadFreshRuntimePlan(repo); err == nil {
		t.Fatal("new primary compiler config did not invalidate the cached plan")
	}
}

func TestRuntimeFreshnessRecipeInvalidatesIncludedSourceSetMutations(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*testing.T, string)
		wantError bool
	}{
		{name: "same-content replacement", mutate: func(t *testing.T, repo string) {
			writeFile(t, repo, "custom/replacement.yml", "rules: []\n")
			if err := os.Rename(filepath.Join(repo, "custom", "replacement.yml"), filepath.Join(repo, "custom", "one.yml")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "addition", wantError: true, mutate: func(t *testing.T, repo string) {
			writeFile(t, repo, "custom/two.yml", "rules: []\n")
		}},
		{name: "removal", wantError: true, mutate: func(t *testing.T, repo string) {
			if err := os.Remove(filepath.Join(repo, "custom", "one.yml")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "rename", wantError: true, mutate: func(t *testing.T, repo string) {
			if err := os.Rename(filepath.Join(repo, "custom", "one.yml"), filepath.Join(repo, "custom", "renamed.yml")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withRECONCHome(t)
			repo := makeFreshnessRecipeRepo(t)
			evaluator := NewEvaluator()
			first, err := evaluator.loadFreshRuntimePlan(repo)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, repo)
			second, err := evaluator.loadFreshRuntimePlan(repo)
			if test.wantError {
				if err == nil {
					t.Fatalf("source-set mutation reused or rebuilt a stale plan: %p", second)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if second == first {
				t.Fatal("source replacement reused the old runtime plan")
			}
		})
	}
}

func TestRuntimeFreshnessRecipeBoundsAndOwnership(t *testing.T) {
	patterns := make([]string, maxFreshnessIncludes+1)
	for index := range patterns {
		patterns[index] = "p" + strings.Repeat("0", 3) + "/*.yml"
	}
	if _, err := newSourceFreshnessRecipe(t.TempDir(), patterns); err == nil {
		t.Fatal("oversized recipe was accepted")
	}
	if _, err := newSourceFreshnessRecipe(t.TempDir(), []string{"z/*.yml", "a/*.yml"}); err == nil {
		t.Fatal("unsorted recipe was accepted")
	}
}

func makeFreshnessRecipeRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, ".reconc.yml", "include:\n  - custom/*.yml\n")
	writeFile(t, repo, "policies/rules.yml", "rules: []\n")
	writeFile(t, repo, "custom/one.yml", "rules: []\n")
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	return repo
}
