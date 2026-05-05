package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestBuildAndRender(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte("rules:\n  - id: deny\n    kind: deny_write\n    paths: ['generated/**']\n    message: no generated writes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile: %v", err)
	}

	view, err := Build(repo)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if view.LockfileStatus != "fresh" {
		t.Fatalf("expected fresh lockfile, got %q", view.LockfileStatus)
	}
	if view.RuleCount != 1 {
		t.Fatalf("expected one rule, got %d", view.RuleCount)
	}
	text := RenderText(view)
	for _, want := range []string{"reconc tui:", "Sources:", "Rules:", "deny"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered TUI missing %q:\n%s", want, text)
		}
	}
}
