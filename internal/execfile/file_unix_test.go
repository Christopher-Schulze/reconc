//go:build !windows

package execfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsRequiresExecutableRegularFile(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	if Is(plain) {
		t.Fatal("non-executable regular file must not be dispatchable")
	}
	executable := filepath.Join(dir, "executable")
	if err := os.WriteFile(executable, []byte("executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !Is(executable) {
		t.Fatal("executable regular file must be dispatchable")
	}
}

func TestIsRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if Is(link) {
		t.Fatal("symlink must not be dispatchable")
	}
}
