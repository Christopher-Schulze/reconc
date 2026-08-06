package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/policy"
)

func TestValidatePolicyLockfileRejectsEmbeddedRuleTampering(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Test repository\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	policyDir := filepath.Join(repo, "policies")
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	policySource := "rules:\n  - id: protected\n    kind: deny_write\n    paths: ['protected/**']\n    mode: block\n    message: protected path\n"
	if err := os.WriteFile(filepath.Join(policyDir, "rules.yml"), []byte(policySource), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "integrity-test"); err != nil {
		t.Fatalf("compile policy: %v", err)
	}

	lockPath := filepath.Join(repo, compiler.LockfileRelativePath)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	rules, ok := payload["rules"].([]interface{})
	if !ok || len(rules) != 1 {
		t.Fatalf("unexpected compiled rules: %#v", payload["rules"])
	}
	rule, ok := rules[0].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected compiled rule: %#v", rules[0])
	}
	rule["mode"] = "observe"
	tampered, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, append(tampered, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	err = ValidatePolicyLockfile(repo)
	var target *rerrors.LockfileError
	if !errors.As(err, &target) {
		t.Fatalf("expected LockfileError for tampered rules, got %T: %v", err, err)
	}
}

func TestLockfileDefaultModeIsCheckedBeforeEvaluation(t *testing.T) {
	valid := map[string]interface{}{"default_mode": string(policy.ModeBlock)}
	mode, err := lockfileDefaultMode(valid)
	if err != nil || mode != policy.ModeBlock {
		t.Fatalf("lockfileDefaultMode(valid) = %q, %v", mode, err)
	}
	for _, payload := range []map[string]interface{}{
		{},
		{"default_mode": 1},
		{"default_mode": nil},
	} {
		if _, err := lockfileDefaultMode(payload); err == nil {
			t.Fatalf("lockfileDefaultMode(%#v) unexpectedly succeeded", payload)
		}
	}
}
