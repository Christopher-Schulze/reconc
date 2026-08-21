//go:build !windows

package privatefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryCreationValidationAndRepairBoundaries(t *testing.T) {
	root := t.TempDir()
	ancestor := filepath.Join(root, "existing")
	target := filepath.Join(ancestor, "private")
	if err := os.Mkdir(ancestor, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ancestor, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := EnsureDirectory(target); err != nil {
		t.Fatalf("create private target: %v", err)
	}
	assertDirectoryMode(t, target, PrivateDirectoryMode)
	assertDirectoryMode(t, ancestor, 0o755)

	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDirectory(target); err == nil {
		t.Fatal("EnsureDirectory repaired existing final-directory mode drift")
	}
	assertDirectoryMode(t, target, 0o755)

	if err := RepairDirectory(target); err != nil {
		t.Fatalf("repair final private target: %v", err)
	}
	assertDirectoryMode(t, target, PrivateDirectoryMode)
	assertDirectoryMode(t, ancestor, 0o755)
	if err := SecureDirectory(target); err != nil {
		t.Fatalf("revalidate repaired private target: %v", err)
	}
}

func TestDirectoryDescriptorRejectsReplacedPathAfterOpen(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private")
	if err := RepairDirectory(path); err != nil {
		t.Fatal(err)
	}
	file, opened, err := openDirectory(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	moved := filepath.Join(root, "moved")
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, PrivateDirectoryMode); err != nil {
		t.Fatal(err)
	}
	after, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDirectoryDescriptor(path, file, opened, after); err == nil {
		t.Fatal("replaced private directory path was accepted")
	}
}

func assertDirectoryMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want.Perm() {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want.Perm())
	}
}
