//go:build !windows

package privatefs

import (
	"os"
	"testing"
)

func assertPrivateLockSecurity(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != PrivateFileMode.Perm() {
		t.Fatalf("lock mode = %04o, want 0600", info.Mode().Perm())
	}
}
