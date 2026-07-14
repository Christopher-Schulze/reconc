package bootstrap

import (
	"strings"
	"testing"
)

func TestEnableCodexHooksPreservesUnrelatedHookKey(t *testing.T) {
	existing := "[other]\nhooks = true\n"
	got, err := enableCodexHooks(existing)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, existing) || !strings.Contains(got, "[features]\n") || strings.Count(got, "hooks = true") != 2 {
		t.Fatalf("unrelated hook key was not preserved: %q", got)
	}
}

func TestEnableCodexHooksReplacesFalseFeatureWithoutDuplication(t *testing.T) {
	got, err := enableCodexHooks("[features]\nhooks = false\nexperimental = true\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "hooks = false") || strings.Count(got, "hooks = true") != 1 || !strings.Contains(got, "experimental = true") {
		t.Fatalf("false feature was not replaced surgically: %q", got)
	}
}

func TestEnableCodexHooksRejectsAmbiguousFeatureTable(t *testing.T) {
	for _, existing := range []string{
		"[features]\nhooks = true\n[features]\nexperimental = true\n",
		"[features]\nhooks = true\nhooks = false\n",
		"hooks = true\n",
	} {
		if _, err := enableCodexHooks(existing); err == nil {
			t.Fatalf("ambiguous Codex TOML was accepted: %q", existing)
		}
	}
}
