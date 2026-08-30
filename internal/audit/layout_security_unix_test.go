//go:build !windows

package audit

import (
	"os"
	"testing"
)

func assertPrivateDirectorySecurity(t *testing.T, path string, want os.FileMode) {
	assertPrivateMode(t, path, want)
}

func assertAuditRootSecurity(t *testing.T, path string, want os.FileMode) {
	assertPrivateMode(t, path, want)
}

func assertPrivateFileSecurity(t *testing.T, path string, want os.FileMode) {
	assertPrivateMode(t, path, want)
}

func assertPrivateMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want.Perm() {
		t.Fatalf("%s mode = %04o, want %04o", path, info.Mode().Perm(), want.Perm())
	}
}

func driftPrivateDirectorySecurity(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func driftPrivateFileSecurity(t *testing.T, path string, mode os.FileMode) {
	driftPrivateDirectorySecurity(t, path, mode)
}
