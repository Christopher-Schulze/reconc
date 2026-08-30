//go:build !windows

package tasklifecycle

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestTaskLockCreatesDeterministicPublicDirectories(t *testing.T) {
	previous := syscall.Umask(0o077)
	defer syscall.Umask(previous)
	repo := t.TempDir()
	if err := withMutationLock(repo, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(repo, ".reconc"),
		filepath.Join(repo, ".reconc", "locks"),
	} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("%s mode = %v, want 0755, err=%v", path, info, err)
		}
	}
}

func TestTaskLockInheritsSharedRepositoryAccess(t *testing.T) {
	repo := t.TempDir()
	root := filepath.Join(repo, ".reconc")
	if err := os.Mkdir(root, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o770|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	if err := withMutationLock(repo, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		root:                         0o770 | os.ModeSetgid,
		filepath.Join(root, "locks"): 0o770 | os.ModeSetgid,
		filepath.Join(root, "locks", "task-lifecycle.lock"): 0o660,
	} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&(os.ModePerm|os.ModeSetgid) != want {
			t.Fatalf("%s mode = %v, want %04o, err=%v", path, info, want, err)
		}
	}
}
