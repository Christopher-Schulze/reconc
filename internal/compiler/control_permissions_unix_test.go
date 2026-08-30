//go:build !windows

package compiler

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCompileFirstCreatesDeterministicControlRoot(t *testing.T) {
	previous := syscall.Umask(0o077)
	defer syscall.Umask(previous)
	repo := t.TempDir()
	release, err := AcquireCompileLock(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(repo, ".reconc"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("compiler-first root = %v, want 0755, err=%v", info, err)
	}
}

func TestCompileLockPreservesSharedRepositoryAccess(t *testing.T) {
	repo := t.TempDir()
	root := filepath.Join(repo, ".reconc")
	if err := os.Mkdir(root, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o770); err != nil {
		t.Fatal(err)
	}
	release, err := AcquireCompileLock(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		root: 0o770,
		filepath.Join(repo, CompileLockRelativePath): 0o660,
	} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s mode = %v, want %04o, err=%v", path, info, want, err)
		}
	}
}
