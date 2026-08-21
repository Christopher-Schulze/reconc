//go:build !windows

package compiler

import (
	"os"
	"testing"
)

func assertRepresentableFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != want.Perm() {
		t.Fatalf("file mode = %v, want %04o, err=%v", info, want.Perm(), err)
	}
}
