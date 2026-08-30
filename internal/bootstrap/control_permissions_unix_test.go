//go:build !windows

package bootstrap

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestBootstrapFirstCreatesDeterministicControlRoot(t *testing.T) {
	previous := syscall.Umask(0o077)
	defer syscall.Umask(previous)
	repo := t.TempDir()
	rootRef, _, err := openBootstrapRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	finalRef, _, _, err := createSafeParentsWithRoot(repo, rootRef, filepath.Join(repo, ".reconc"))
	if err != nil {
		t.Fatal(err)
	}
	if err := closeBootstrapRootRef(finalRef); err != nil {
		t.Fatal(err)
	}
	if err := closeBootstrapRootRef(rootRef); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(repo, ".reconc"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("bootstrap-first root = %v, want 0755, err=%v", info, err)
	}
}
