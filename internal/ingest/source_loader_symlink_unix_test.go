//go:build !windows

package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFragmentOutsideRootFailsFilesystemIdentityCheck(t *testing.T) {
	outside := t.TempDir()
	repo := t.TempDir()
	writeFile(t, outside, "extra.yml", "rules: []\n")
	writeFile(t, repo, ".reconc.yml", "default_mode: warn\ninclude:\n  - policies/*.yml\n")
	if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "extra.yml"), filepath.Join(repo, "policies", "extra.yml")); err != nil {
		t.Fatal(err)
	}
	_, err := LoadPolicySources(repo)
	if err == nil || !strings.Contains(err.Error(), "resolves outside the repository root") {
		t.Fatalf("expected filesystem-identity rejection, got %v", err)
	}
}

func TestConfigAndInstructionSymlinksOutsideRootFailClosed(t *testing.T) {
	for _, relative := range []string{".reconc.yml", "AGENTS.md"} {
		t.Run(relative, func(t *testing.T) {
			outside := t.TempDir()
			repo := t.TempDir()
			target := filepath.Join(outside, filepath.Base(relative))
			content := "# project\n"
			if relative == ".reconc.yml" {
				content = "rules: []\n"
				writeFile(t, repo, "AGENTS.md", "# project\n")
			}
			if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(repo, relative)); err != nil {
				t.Fatal(err)
			}
			_, err := LoadPolicySources(repo)
			if err == nil || !strings.Contains(err.Error(), "resolves outside the repository root") {
				t.Fatalf("expected %s symlink rejection, got %v", relative, err)
			}
		})
	}
}
