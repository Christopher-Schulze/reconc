package tasklifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskPathGuardRejectsReplacementAfterRead(t *testing.T) {
	root := t.TempDir()
	detailDir := filepath.Join(root, "docs", "tasks")
	path := filepath.Join(detailDir, "001-current.md")
	if err := os.MkdirAll(detailDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guard := newTaskPathGuard(root, 8)
	if err := guard.reject(path); err != nil {
		t.Fatal(err)
	}
	oldPath := path + ".old"
	if err := os.Rename(path, oldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after!\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := guard.revalidate(); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("replacement was accepted: %v", err)
	}
}

func TestTaskPathGuardRejectsSymlinkAndNonDirectoryComponents(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	guard := newTaskPathGuard(root, 8)
	if err := guard.reject(filepath.Join(root, "docs", "tasks", "001.md")); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("non-directory intermediate was accepted: %v", err)
	}

	linkRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(linkRoot, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(linkRoot, "real"), filepath.Join(linkRoot, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	linkGuard := newTaskPathGuard(linkRoot, 8)
	if err := linkGuard.reject(filepath.Join(linkRoot, "alias", "001.md")); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink intermediate was accepted: %v", err)
	}
}
