package ingest

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/policy"
)

// withRECONCHome isolates RECONC_HOME for tests so user-level state
// doesn't leak in.
func withRECONCHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RECONC_HOME", dir)
	return dir
}

func TestLoadPolicySourcesEmptyRepoFails(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	_, err := LoadPolicySources(repo)
	if err == nil {
		t.Fatal("expected error for empty repo")
	}
	var pse *rerrors.PolicySourceError
	if !stderrors.As(err, &pse) {
		t.Errorf("expected *PolicySourceError, got %T", err)
	}
}

func TestLoadPolicySourcesAgentsMDOnly(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\nDescription.\n")

	bundle, err := LoadPolicySources(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle == nil {
		t.Fatal("expected non-nil bundle")
	}
	if len(bundle.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(bundle.Sources))
	}
	if bundle.Sources[0].Kind != policy.SourceAgentsMD {
		t.Errorf("expected agents_md kind, got %s", bundle.Sources[0].Kind)
	}
}

func TestLoadPolicySourcesExtractsInlineBlocks(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	content := "# AGENTS\n\nSome prose.\n\n```reconc\nrules:\n  - id: inline-rule\n    kind: deny_write\n    paths: ['x/**']\n    mode: warn\n    message: x\n```\n\nMore prose.\n"
	writeFile(t, repo, "AGENTS.md", content)

	bundle, err := LoadPolicySources(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect: agents_md + 1 inline block
	if len(bundle.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(bundle.Sources))
	}
	if bundle.Sources[1].Kind != policy.SourceInlineBlock {
		t.Errorf("expected inline_block as second source, got %s", bundle.Sources[1].Kind)
	}
	if !strings.Contains(bundle.Sources[1].Content, "inline-rule") {
		t.Errorf("inline block content missing rule id, got: %s", bundle.Sources[1].Content)
	}
	if bundle.Sources[1].LineStart < 5 {
		t.Errorf("expected LineStart >= 5, got %d", bundle.Sources[1].LineStart)
	}
}

func TestLoadPolicySourcesMultipleInlineBlocks(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	content := "intro\n\n```reconc\nrules: []\n```\n\nmiddle\n\n```reconc\nrules: []\n```\n"
	writeFile(t, repo, "AGENTS.md", content)

	bundle, err := LoadPolicySources(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect: agents_md + 2 inline blocks
	if len(bundle.Sources) != 3 {
		t.Fatalf("expected 3 sources, got %d: %v", len(bundle.Sources), sourceKinds(bundle))
	}
}

func TestLoadPolicySourcesCompilerConfig(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# agents\n")
	writeFile(t, repo, ".reconc.yml", "default_mode: warn\nrules: []\n")

	bundle, err := LoadPolicySources(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect: agents_md + compiler_config
	if len(bundle.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(bundle.Sources))
	}
	if bundle.Sources[1].Kind != policy.SourceCompilerConfig {
		t.Errorf("expected compiler_config, got %s", bundle.Sources[1].Kind)
	}
}

func TestLoadPolicySourcesExtendsBundledPreset(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# agents\n")
	writeFile(t, repo, ".reconc.yml", "extends:\n  - default\nrules: []\n")

	bundle, err := LoadPolicySources(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect: agents_md + compiler_config + preset
	if len(bundle.Sources) != 3 {
		t.Fatalf("expected 3 sources, got %d: %v", len(bundle.Sources), sourceKinds(bundle))
	}
	if bundle.Sources[2].Kind != policy.SourcePreset {
		t.Errorf("expected preset, got %s", bundle.Sources[2].Kind)
	}
	if !strings.Contains(bundle.Sources[2].Content, "preset-default-generated-read-only") {
		t.Errorf("preset content missing expected rule id")
	}
}

func TestLoadPolicySourcesExtendsUnknownPresetFails(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# agents\n")
	writeFile(t, repo, ".reconc.yml", "extends:\n  - definitely-not-bundled\n")

	_, err := LoadPolicySources(repo)
	if err == nil {
		t.Fatal("expected error for unknown preset")
	}
	var pnf *rerrors.PresetNotFoundError
	if !stderrors.As(err, &pnf) {
		t.Errorf("expected *PresetNotFoundError, got %T (msg: %v)", err, err)
	}
}

func TestLoadPolicySourcesExtendsDeduplicates(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# agents\n")
	writeFile(t, repo, ".reconc.yml", "extends:\n  - default\n  - default\n  - strict\n")

	bundle, err := LoadPolicySources(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	presetCount := 0
	for _, s := range bundle.Sources {
		if s.Kind == policy.SourcePreset {
			presetCount++
		}
	}
	if presetCount != 2 {
		t.Errorf("expected 2 unique presets after dedup, got %d", presetCount)
	}
}

func TestLoadPolicySourcesPolicyFragments(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# agents\n")
	writeFile(t, repo, "policies/rules.yml", "rules: []\n")
	writeFile(t, repo, "policies/extra.yaml", "rules: []\n")

	bundle, err := LoadPolicySources(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fragmentCount := 0
	for _, s := range bundle.Sources {
		if s.Kind == policy.SourcePolicyFile {
			fragmentCount++
		}
	}
	if fragmentCount != 2 {
		t.Errorf("expected 2 policy fragment sources, got %d", fragmentCount)
	}
}

func TestLoadPolicySourcesIncludePatterns(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# agents\n")
	writeFile(t, repo, ".reconc.yml", "include:\n  - 'extras/*.yml'\n")
	writeFile(t, repo, "extras/extra.yml", "rules: []\n")

	bundle, err := LoadPolicySources(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	foundExtras := false
	for _, s := range bundle.Sources {
		if s.Kind == policy.SourcePolicyFile && strings.Contains(s.Path, "extras/extra.yml") {
			foundExtras = true
		}
	}
	if !foundExtras {
		t.Errorf("expected extras/extra.yml to be loaded via include pattern; got sources: %v", sourceKinds(bundle))
	}
}

func TestLoadPolicySourcesIncludeRejectsAbsolutePath(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# agents\n")
	writeFile(t, repo, ".reconc.yml", "include:\n  - '/etc/passwd'\n")

	_, err := LoadPolicySources(repo)
	if err == nil {
		t.Fatal("expected error for absolute include path")
	}
	if !strings.Contains(err.Error(), "stay within the repo root") {
		t.Errorf("expected boundary error, got: %v", err)
	}
}

func TestLoadPolicySourcesIncludeRejectsParentEscape(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# agents\n")
	writeFile(t, repo, ".reconc.yml", "include:\n  - '../outside/*.yml'\n")

	_, err := LoadPolicySources(repo)
	if err == nil {
		t.Fatal("expected error for parent-escape include path")
	}
}

func TestLoadPolicySourcesGlobalPolicy(t *testing.T) {
	home := withRECONCHome(t)
	if err := os.WriteFile(filepath.Join(home, "global-policy.yml"), []byte("rules:\n  - id: g\n    kind: deny_write\n    paths: ['secret/*']\n    mode: block\n    message: g\n"), 0o644); err != nil {
		t.Fatalf("write global: %v", err)
	}
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# agents\n")

	bundle, err := LoadPolicySources(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle.Sources[0].Kind != policy.SourceGlobal {
		t.Errorf("expected global as first source, got %s", bundle.Sources[0].Kind)
	}
}

func TestLoadPolicySourcesPrecedenceOrder(t *testing.T) {
	home := withRECONCHome(t)
	if err := os.WriteFile(filepath.Join(home, "global-policy.yml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatalf("write global: %v", err)
	}
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# agents\n```reconc\nrules: []\n```\n")
	writeFile(t, repo, ".reconc.yml", "extends:\n  - default\nrules: []\n")
	writeFile(t, repo, "policies/extra.yml", "rules: []\n")

	bundle, err := LoadPolicySources(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []policy.SourceKind{
		policy.SourceGlobal,
		policy.SourceAgentsMD,
		policy.SourceInlineBlock,
		policy.SourceCompilerConfig,
		policy.SourcePreset,
		policy.SourcePolicyFile,
	}
	got := []policy.SourceKind{}
	for _, s := range bundle.Sources {
		got = append(got, s.Kind)
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d sources, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: expected %s, got %s", i, want[i], got[i])
		}
	}
}

func TestLoadPolicySourcesInvalidConfigYAMLFails(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# agents\n")
	writeFile(t, repo, ".reconc.yml", "extends:\n  - [invalid\n")

	_, err := LoadPolicySources(repo)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	var pse *rerrors.PolicySourceError
	if !stderrors.As(err, &pse) {
		t.Errorf("expected *PolicySourceError, got %T", err)
	}
}

// helper to dump source kinds for assertion failures
func sourceKinds(b *SourceBundle) []policy.SourceKind {
	out := make([]policy.SourceKind, 0, len(b.Sources))
	for _, s := range b.Sources {
		out = append(out, s.Kind)
	}
	return out
}
