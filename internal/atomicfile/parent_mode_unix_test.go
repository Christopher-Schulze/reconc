//go:build !windows

package atomicfile

import (
	"os"
	"testing"
)

func assertPublicationParent(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != want {
		t.Fatalf("publication parent mode = %v, want %04o, err=%v", info, want, err)
	}
}
