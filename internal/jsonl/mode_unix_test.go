//go:build !windows

package jsonl

import (
	"os"
	"testing"
)

func assertJSONLFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != want.Perm() {
		t.Fatalf("JSONL mode = %v, want %04o, err = %v", info, want.Perm(), err)
	}
}
