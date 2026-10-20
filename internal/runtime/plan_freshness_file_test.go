package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func countingSourceLoads(evaluator *Evaluator) *int {
	loads := 0
	evaluator.loadHook = func(stage runtimePlanLoadStage) {
		if stage == runtimePlanLoadAfterSourceSnapshot {
			loads++
		}
	}
	return &loads
}

// A cold evaluator that finds the lockfile validated against unchanged source
// content compiles the plan without re-reading and re-digesting the sources,
// as a warm evaluator reuses its cached plan.
func TestPersistedPlanFreshnessSkipsSourceValidationForUnchangedSources(t *testing.T) {
	withRECONCHome(t)
	rules := "rules:\n  - id: abc\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: one\n"
	repo := makeRepo(t, "# project\n", "", rules)
	record := filepath.Join(t.TempDir(), "state", "plan-freshness.json")
	if err := os.Mkdir(filepath.Dir(record), 0o700); err != nil {
		t.Fatal(err)
	}
	locate := func(root string) string {
		if root != repo {
			t.Fatalf("plan freshness located for %q, want %q", root, repo)
		}
		return record
	}

	first := NewEvaluatorWithPlanFreshnessFile(locate)
	firstLoads := countingSourceLoads(first)
	if _, err := first.loadFreshRuntimePlan(repo); err != nil {
		t.Fatal(err)
	}
	if *firstLoads != 1 {
		t.Fatalf("first cold load read the sources %d times, want 1", *firstLoads)
	}
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("validated plan freshness was not persisted: %v", err)
	}

	second := NewEvaluatorWithPlanFreshnessFile(locate)
	secondLoads := countingSourceLoads(second)
	plan, err := second.loadFreshRuntimePlan(repo)
	if err != nil {
		t.Fatal(err)
	}
	if *secondLoads != 0 || len(plan.rules) != 1 || plan.rules[0].ID != "abc" {
		t.Fatalf("persisted cold load read the sources %d times and compiled %d rules", *secondLoads, len(plan.rules))
	}

	// Same size and restored metadata: only the content hash differs, and the
	// stale lockfile must still fail closed through the full validation.
	updated := strings.Replace(rules, "message: one", "message: two", 1)
	policyPath := filepath.Join(repo, "policies", "rules.yml")
	before, err := os.Lstat(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte(updated), before.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(policyPath, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	third := NewEvaluatorWithPlanFreshnessFile(locate)
	thirdLoads := countingSourceLoads(third)
	if _, err := third.loadFreshRuntimePlan(repo); err == nil || !freshnessInvalidationError(err) {
		t.Fatalf("changed source behind a persisted freshness was accepted: %v", err)
	}
	if *thirdLoads != 1 {
		t.Fatalf("changed source was validated %d times, want one full validation", *thirdLoads)
	}
}

func TestPersistedPlanFreshnessIgnoresUntrustedRecords(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules: []\n")
	record := filepath.Join(t.TempDir(), "plan-freshness.json")
	locate := func(string) string { return record }
	if _, err := NewEvaluatorWithPlanFreshnessFile(locate).loadFreshRuntimePlan(repo); err != nil {
		t.Fatal(err)
	}
	valid, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"malformed":    "{not json",
		"foreign root": strings.Replace(string(valid), filepath.ToSlash(repo), "/foreign/root", 1),
		"other lock":   strings.Replace(string(valid), `"lock_sha256": "`, `"lock_sha256": "0`, 1),
		"version":      strings.Replace(string(valid), `"format_version": 1`, `"format_version": 9`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(record, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			evaluator := NewEvaluatorWithPlanFreshnessFile(locate)
			loads := countingSourceLoads(evaluator)
			if _, err := evaluator.loadFreshRuntimePlan(repo); err != nil {
				t.Fatalf("untrusted record failed the load: %v", err)
			}
			if *loads != 1 {
				t.Fatalf("untrusted record skipped the full validation: %d source loads", *loads)
			}
		})
	}
}

func TestPersistedPlanFreshnessCreatesNoDirectory(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules: []\n")
	record := filepath.Join(t.TempDir(), "missing", "plan-freshness.json")
	if _, err := NewEvaluatorWithPlanFreshnessFile(func(string) string { return record }).loadFreshRuntimePlan(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Dir(record)); !os.IsNotExist(err) {
		t.Fatalf("plan freshness created its parent directory: %v", err)
	}
}

func TestDefaultEvaluatorPersistsNoPlanFreshness(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules: []\n")
	evaluator := NewEvaluator()
	if _, err := evaluator.loadFreshRuntimePlan(repo); err != nil {
		t.Fatal(err)
	}
	if evaluator.planFreshnessFile != nil {
		t.Fatal("the default evaluator locates a plan freshness file")
	}
}
