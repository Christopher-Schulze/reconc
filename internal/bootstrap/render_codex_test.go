package bootstrap

import (
	"strings"
	"testing"
)

func TestRenderCodexActivationPreservesUnrelatedHookKey(t *testing.T) {
	repo := t.TempDir()
	existing := "[other]\nhooks = true\n"
	writeBootstrapTestFile(t, repo, ".codex/config.toml", existing, 0o644)
	got, err := renderCodexActivation(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, existing) || !strings.Contains(got, "[features]\n") || strings.Count(got, "hooks = true") != 2 {
		t.Fatalf("unrelated hook key was not preserved: %q", got)
	}
}

func TestRenderCodexActivationReplacesFalseFeatureWithoutDuplication(t *testing.T) {
	repo := t.TempDir()
	writeBootstrapTestFile(t, repo, ".codex/config.toml", "[features]\nhooks = false\nexperimental = true\n", 0o644)
	got, err := renderCodexActivation(repo)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "hooks = false") || strings.Count(got, "hooks = true") != 1 || !strings.Contains(got, "experimental = true") {
		t.Fatalf("false feature was not replaced surgically: %q", got)
	}
}

func TestRenderCodexActivationRejectsAmbiguousFeatureTable(t *testing.T) {
	for _, existing := range []string{
		"[features]\nhooks = true\n[features]\nexperimental = true\n",
		"[features]\nhooks = true\nhooks = false\n",
		"hooks = true\n",
	} {
		repo := t.TempDir()
		writeBootstrapTestFile(t, repo, ".codex/config.toml", existing, 0o644)
		if _, err := renderCodexActivation(repo); err == nil {
			t.Fatalf("ambiguous Codex TOML was accepted: %q", existing)
		}
	}
}
