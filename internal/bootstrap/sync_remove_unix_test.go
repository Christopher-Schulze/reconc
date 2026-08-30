//go:build !windows

package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncRemovalParentUsesBoundUnixDirectory(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent, parentInfo, name, err := openCreatedParent(target)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	originalSync := bootstrapDirectorySync
	t.Cleanup(func() { bootstrapDirectorySync = originalSync })
	called := false
	bootstrapDirectorySync = func(got *os.Root) error {
		called = true
		if got != parent {
			t.Fatal("removal durability reopened the parent by path")
		}
		return nil
	}
	if err := parent.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := syncRemovalParent(parent, parentInfo, target); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("bound Unix parent was not fsynced")
	}
}
