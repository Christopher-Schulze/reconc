//go:build !windows

package repositorycontrol

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"reconc.dev/reconc/internal/privatefs"
)

func TestEnsureRootIsUmaskIndependentAndPreservesExistingAccess(t *testing.T) {
	previous := syscall.Umask(0o077)
	defer syscall.Umask(previous)

	repo := t.TempDir()
	if err := EnsureRoot(repo); err != nil {
		t.Fatal(err)
	}
	assertDirectoryMode(t, filepath.Join(repo, RootName), PublicDirectoryMode)

	for _, mode := range []os.FileMode{0o700, 0o770} {
		repo := t.TempDir()
		root := filepath.Join(repo, RootName)
		if err := os.Mkdir(root, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(root, mode); err != nil {
			t.Fatal(err)
		}
		if err := EnsureRoot(repo); err != nil {
			t.Fatal(err)
		}
		assertDirectoryMode(t, root, mode)
	}
}

func TestEnsureRunDirectoryKeepsSharedRootAndRejectsLegacyExposure(t *testing.T) {
	previous := syscall.Umask(0o077)
	defer syscall.Umask(previous)

	repo := t.TempDir()
	root := filepath.Join(repo, RootName)
	if err := os.Mkdir(root, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRunDirectory(repo); err != nil {
		t.Fatal(err)
	}
	assertDirectoryMode(t, root, 0o770)
	if err := privatefs.ValidateDirectory(filepath.Join(root, RunName)); err != nil {
		t.Fatal(err)
	}

	legacyRepo := t.TempDir()
	legacyRun := filepath.Join(legacyRepo, RootName, RunName)
	if err := os.MkdirAll(legacyRun, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(legacyRun, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(legacyRun, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRunDirectory(legacyRepo); err == nil {
		t.Fatal("legacy public run directory was silently narrowed")
	}
	assertDirectoryMode(t, legacyRun, 0o755)
	if body, err := os.ReadFile(sentinel); err != nil || string(body) != "keep\n" {
		t.Fatalf("legacy run data changed: body=%q err=%v", body, err)
	}
}

func assertDirectoryMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != want.Perm() {
		t.Fatalf("%s mode = %v, want %04o, err=%v", path, info, want.Perm(), err)
	}
}
