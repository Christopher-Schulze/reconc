//go:build !windows

package privatefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenExistingLockReadOnlyDoesNotRepairMode(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenExistingLockReadOnly(path); err == nil {
		t.Fatal("read-only lock open accepted insecure mode")
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("read-only lock open repaired mode to %04o", info.Mode().Perm())
	}
}
