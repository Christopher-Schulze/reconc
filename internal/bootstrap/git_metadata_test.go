package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectRepositoryGitMetadataContracts(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		if present, err := inspectRepositoryGitMetadata(t.TempDir()); err != nil || present {
			t.Fatalf("missing metadata = %v, %v", present, err)
		}
	})
	t.Run("directory", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if present, err := inspectRepositoryGitMetadata(root); err != nil || !present {
			t.Fatalf("directory metadata = %v, %v", present, err)
		}
	})
	t.Run("worktree file", func(t *testing.T) {
		root := t.TempDir()
		gitDirectory := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitDirectory+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if present, err := inspectRepositoryGitMetadata(root); err != nil || !present {
			t.Fatalf("worktree metadata = %v, %v", present, err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Symlink(t.TempDir(), filepath.Join(root, ".git")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := inspectRepositoryGitMetadata(root); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
			t.Fatalf("symlink metadata was accepted: %v", err)
		}
	})
	t.Run("malformed worktree file", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("not-gitdir\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := inspectRepositoryGitMetadata(root); err == nil || !strings.Contains(err.Error(), "malformed") {
			t.Fatalf("malformed metadata was accepted: %v", err)
		}
	})
}
