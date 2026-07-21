package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withRECONCHome(t *testing.T) {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
}

func TestInitializeFreshRepoCreatesBoth(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	rep, err := Initialize(repo, Options{})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if len(rep.Created) != 3 {
		t.Errorf("expected 3 created files, got %v", rep.Created)
	}
	for _, want := range []string{".gitignore", ".reconc.yml", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(repo, want)); err != nil {
			t.Errorf("expected %s to be created: %v", want, err)
		}
	}
}

func TestInitializePreservesAndDeduplicatesManagedIgnoreBlock(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	ignorePath := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(ignorePath, []byte("vendor/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Initialize(repo, Options{}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := Initialize(repo, Options{Force: true}); err != nil {
		t.Fatalf("force init: %v", err)
	}
	body, err := os.ReadFile(ignorePath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(body)
	if !strings.HasPrefix(content, "vendor/\n") || strings.Count(content, "# >>> reconc bootstrap runtime") != 1 {
		t.Fatalf("managed ignore block is not preserved and idempotent: %q", content)
	}
}

func TestInitializeAgentsStubUsesPublishedProjectLocation(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	if _, err := Initialize(repo, Options{}); err != nil {
		t.Fatalf("init: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "https://github.com/Christopher-Schulze/reconc") || strings.Contains(content, "https://reconc.dev") {
		t.Fatalf("AGENTS.md project link is not canonical: %s", content)
	}
}

func TestInitializeDefaultsToDefaultAndAgentPresets(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	rep, err := Initialize(repo, Options{})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if len(rep.Presets) != 2 || rep.Presets[0] != "default" || rep.Presets[1] != "agent" {
		t.Errorf("expected presets=[default agent], got %v", rep.Presets)
	}
	data, _ := os.ReadFile(filepath.Join(repo, ".reconc.yml"))
	for _, want := range []string{"  - default", "  - agent"} {
		if !strings.Contains(string(data), want) {
			t.Errorf(".reconc.yml should reference %q, got: %s", want, string(data))
		}
	}
}

func TestInitializeMultiplePresets(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	rep, err := Initialize(repo, Options{Presets: []string{"default", "strict", "docs-sync"}})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if len(rep.Presets) != 3 {
		t.Errorf("expected 3 presets, got %v", rep.Presets)
	}
	data, _ := os.ReadFile(filepath.Join(repo, ".reconc.yml"))
	for _, want := range []string{"  - default", "  - strict", "  - docs-sync"} {
		if !strings.Contains(string(data), want) {
			t.Errorf(".reconc.yml missing %q, got: %s", want, string(data))
		}
	}
}

func TestInitializeRejectsUnknownPreset(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	_, err := Initialize(repo, Options{Presets: []string{"definitely-not-bundled"}})
	if err == nil {
		t.Fatal("expected error for unknown preset")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("expected 'not available' in error, got: %v", err)
	}
}

func TestInitializeDedupesPresets(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	rep, err := Initialize(repo, Options{Presets: []string{"default", "default", "strict"}})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if len(rep.Presets) != 2 {
		t.Errorf("expected dedup to 2 presets, got %v", rep.Presets)
	}
}

func TestInitializeRefusesOverwriteWithoutForce(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	if _, err := Initialize(repo, Options{}); err != nil {
		t.Fatalf("first init: %v", err)
	}
	_, err := Initialize(repo, Options{})
	if err == nil {
		t.Fatal("expected error on second init without Force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists', got: %v", err)
	}
}

func TestInitializeOverwritesWithForce(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	if _, err := Initialize(repo, Options{}); err != nil {
		t.Fatalf("first init: %v", err)
	}
	rep, err := Initialize(repo, Options{Presets: []string{"strict"}, Force: true})
	if err != nil {
		t.Fatalf("force init: %v", err)
	}
	for _, u := range rep.Updated {
		if u == ".reconc.yml" {
			return
		}
	}
	t.Errorf("expected .reconc.yml in Updated list, got: %+v", rep)
}

func TestInitializeNeverOverwritesAGENTSmd(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	custom := "# my custom AGENTS\nstuff\n"
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Initialize(repo, Options{})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, s := range rep.Skipped {
		if s == "AGENTS.md" {
			data, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
			if string(data) != custom {
				t.Errorf("AGENTS.md was modified despite skip")
			}
			return
		}
	}
	t.Errorf("expected AGENTS.md in Skipped list, got: %+v", rep)
}

func TestInitializeSkipsAgentsMdWhenCLAUDEMdPresent(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("# c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Initialize(repo, Options{})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); err == nil {
		t.Error("AGENTS.md should NOT be created when CLAUDE.md exists")
	}
	for _, s := range rep.Skipped {
		if s == "AGENTS.md" {
			return
		}
	}
	t.Errorf("expected AGENTS.md in Skipped, got: %+v", rep)
}

func TestInitializeRejectsMissingRepo(t *testing.T) {
	withRECONCHome(t)
	_, err := Initialize("/no/such/path/for/init", Options{})
	if err == nil {
		t.Fatal("expected error for missing repo path")
	}
}

func TestInitializeRejectsFileAsRepo(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	notDir := filepath.Join(repo, "file.txt")
	_ = os.WriteFile(notDir, []byte("x"), 0o644)
	_, err := Initialize(notDir, Options{})
	if err == nil {
		t.Fatal("expected error for non-directory")
	}
}

func TestInitializedRepoCanBeCompiled(t *testing.T) {
	// The whole point of init is that the result compiles cleanly.
	withRECONCHome(t)
	repo := t.TempDir()
	if _, err := Initialize(repo, Options{Presets: []string{"default"}}); err != nil {
		t.Fatalf("init: %v", err)
	}
	// Now compile via the package directly.
	out, err := compileForTest(repo)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_ = out
}
