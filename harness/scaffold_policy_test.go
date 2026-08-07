package harness_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/policy"
)

// scaffoldPolicyPath is the policy the advanced harness ships into every
// repository that bootstraps it.
const scaffoldPolicyPath = "template/repo-root-scaffold/.reconc.yml"

// TestScaffoldPolicyCompilesWithTheShippedCompiler closes a gap the rest of the
// suite left open: nothing compiled the policy the product itself ships.
// String-level checks and the pack digest prove the file is present and
// unchanged, not that the shipped compiler accepts it, so an unknown field, a
// malformed rule, or an invalid declaration would have reached a user's
// repository and failed there at bootstrap time.
func TestScaffoldPolicyCompilesWithTheShippedCompiler(t *testing.T) {
	body, err := os.ReadFile(scaffoldPolicyPath)
	if err != nil {
		t.Fatalf("read scaffold policy: %v", err)
	}
	repo := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	compiled, err := compiler.CompileRepoPolicy(repo, "test")
	if err != nil {
		t.Fatalf("the shipped scaffold policy does not compile: %v", err)
	}
	if compiled == nil || compiled.RuleCount == 0 {
		t.Fatal("the shipped scaffold policy compiled to no rules")
	}

	// Inspect the lockfile the runtime actually loads, not a compile summary.
	lock, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(compiled.LockfilePath)))
	if err != nil {
		t.Fatalf("read compiled lockfile: %v", err)
	}
	var decoded struct {
		Rules []struct {
			ID          string   `json:"id"`
			Kind        string   `json:"kind"`
			CacheInputs []string `json:"cache_inputs"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(lock, &decoded); err != nil {
		t.Fatalf("decode compiled lockfile: %v", err)
	}
	scripts := 0
	declared := 0
	for _, rule := range decoded.Rules {
		if rule.Kind != string(policy.KindRequireScript) {
			continue
		}
		scripts++
		if len(rule.CacheInputs) == 0 {
			continue
		}
		declared++
		for _, input := range rule.CacheInputs {
			if strings.ContainsAny(input, "*?[]{}") || strings.HasPrefix(input, "/") || strings.Contains(input, "..") {
				t.Fatalf("rule %s declares an unbindable cache input %q", rule.ID, input)
			}
		}
	}
	if scripts == 0 {
		t.Fatal("the shipped scaffold policy declares no require_script gate")
	}
	if declared == 0 {
		t.Fatal("no scaffolded gate declares cache inputs; Stop reuse would never apply")
	}
}

// TestScaffoldPolicyCompilesDeterministically keeps the shipped policy on the
// same byte-stable contract as any other compiled policy: two compiles of the
// same sources must produce the same lock.
func TestScaffoldPolicyCompilesDeterministically(t *testing.T) {
	body, err := os.ReadFile(scaffoldPolicyPath)
	if err != nil {
		t.Fatalf("read scaffold policy: %v", err)
	}
	digests := make([]string, 0, 2)
	for attempt := 0; attempt < 2; attempt++ {
		repo := t.TempDir()
		t.Setenv("RECONC_HOME", t.TempDir())
		if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), body, 0o644); err != nil {
			t.Fatal(err)
		}
		compiled, err := compiler.CompileRepoPolicy(repo, "test")
		if err != nil {
			t.Fatalf("compile attempt %d: %v", attempt, err)
		}
		digests = append(digests, compiled.SourceDigest)
	}
	if digests[0] != digests[1] || digests[0] == "" {
		t.Fatalf("scaffold policy compiles non-deterministically: %q vs %q", digests[0], digests[1])
	}
}
