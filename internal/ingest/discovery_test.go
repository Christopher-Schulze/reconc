package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo creates a fresh temp directory and returns its path.
func newRepo(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// writeFile creates name under dir with content, creating parent dirs
// as needed. Test fatalizes on any IO error.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func TestDiscoverFailsOnMissingStartPath(t *testing.T) {
	_, err := DiscoverPolicyRepo("/nonexistent/definitely/not/here")
	if err == nil {
		t.Fatal("expected error for nonexistent start path")
	}
}

func TestDiscoverReturnsUndiscoveredWhenNoMarkers(t *testing.T) {
	repo := newRepo(t)
	r, err := DiscoverPolicyRepo(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Discovered {
		t.Errorf("expected Discovered=false in empty repo, got true")
	}
	if len(r.Warnings) == 0 {
		t.Errorf("expected a warning when nothing found")
	}
}

func TestDiscoverFindsAgentsMD(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "AGENTS.md", "# agents\n")

	r, err := DiscoverPolicyRepo(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Discovered {
		t.Fatal("expected Discovered=true when AGENTS.md present")
	}
	if r.AgentsPath == nil || *r.AgentsPath != "AGENTS.md" {
		t.Errorf("expected AgentsPath = AGENTS.md, got %v", r.AgentsPath)
	}
	if r.ClaudePath != nil {
		t.Errorf("expected ClaudePath nil, got %v", *r.ClaudePath)
	}
}

func TestDiscoverFindsStartMD(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "start.md", "# start\n")

	r, err := DiscoverPolicyRepo(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Discovered {
		t.Fatal("expected Discovered=true when start.md present")
	}
	if r.StartMDPath == nil || *r.StartMDPath != "start.md" {
		t.Errorf("expected StartMDPath = start.md, got %v", r.StartMDPath)
	}
}

func TestDiscoverFindsLegacyCLAUDEMd(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "CLAUDE.md", "# claude\n")

	r, err := DiscoverPolicyRepo(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Discovered {
		t.Fatal("expected Discovered=true when CLAUDE.md present")
	}
	if r.ClaudePath == nil || *r.ClaudePath != "CLAUDE.md" {
		t.Errorf("expected ClaudePath = CLAUDE.md, got %v", r.ClaudePath)
	}
}

func TestDiscoverPrefersYmlConfigOverYaml(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "AGENTS.md", "# agents\n")
	writeFile(t, repo, ".reconc.yml", "default_mode: warn\n")
	writeFile(t, repo, ".reconc.yaml", "default_mode: warn\n")

	r, err := DiscoverPolicyRepo(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.ConfigPath == nil || *r.ConfigPath != ".reconc.yml" {
		t.Errorf("expected preferred ConfigPath = .reconc.yml, got %v", r.ConfigPath)
	}
	if len(r.ConfigCandidates) != 2 {
		t.Errorf("expected 2 ConfigCandidates, got %d", len(r.ConfigCandidates))
	}
	// Warning about multiple configs should surface
	if !hasWarningContaining(r.Warnings, "multiple compiler config files") {
		t.Errorf("expected multiple-config warning, got %v", r.Warnings)
	}
}

func TestDiscoverWalksUpToParent(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "AGENTS.md", "# agents\n")
	writeFile(t, repo, "src/pkg/helper.go", "package pkg\n")

	nested := filepath.Join(repo, "src", "pkg")
	r, err := DiscoverPolicyRepo(nested)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Discovered {
		t.Fatal("expected Discovered=true when walking up")
	}
	if r.RepoRoot != repo {
		resolved, _ := filepath.EvalSymlinks(repo)
		if r.RepoRoot != resolved {
			t.Errorf("expected RepoRoot to be repo (%s) or its resolved form (%s), got %s", repo, resolved, r.RepoRoot)
		}
	}
}

func TestDiscoverListsPolicyFragments(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "AGENTS.md", "# agents\n")
	writeFile(t, repo, "policies/rules.yml", "rules: []\n")
	writeFile(t, repo, "policies/extra.yaml", "rules: []\n")

	r, err := DiscoverPolicyRepo(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.PolicyPaths) != 2 {
		t.Errorf("expected 2 policy fragments, got %d: %v", len(r.PolicyPaths), r.PolicyPaths)
	}
}

func TestDiscoverWarnsWhenLockfileMissing(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "AGENTS.md", "# agents\n")

	r, err := DiscoverPolicyRepo(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasWarningContaining(r.Warnings, "lockfile not found") {
		t.Errorf("expected lockfile-missing warning, got %v", r.Warnings)
	}
	if r.LockfilePath != nil {
		t.Errorf("expected LockfilePath nil when lockfile missing, got %v", *r.LockfilePath)
	}
}

func TestDiscoverSetsLockfilePathWhenPresent(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "AGENTS.md", "# agents\n")
	writeFile(t, repo, LockfilePath, `{"format_version":"1"}`)

	r, err := DiscoverPolicyRepo(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.LockfilePath == nil || *r.LockfilePath != LockfilePath {
		t.Errorf("expected LockfilePath = %s, got %v", LockfilePath, r.LockfilePath)
	}
	if hasWarningContaining(r.Warnings, "lockfile not found") {
		t.Errorf("should not warn about lockfile when present, got %v", r.Warnings)
	}
}

func TestDiscoverAcceptsFileAsStartPath(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "AGENTS.md", "# agents\n")
	writeFile(t, repo, "main.go", "package main\n")

	r, err := DiscoverPolicyRepo(filepath.Join(repo, "main.go"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Discovered {
		t.Fatal("expected Discovered=true when starting from a file")
	}
}

func hasWarningContaining(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
