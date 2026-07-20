//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteIfChangedRepairsModeWithoutRewritingBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("private\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixedTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	written, err := WriteIfChanged(path, []byte("private\n"), 0o600)
	if err != nil || !written {
		t.Fatalf("mode repair: written=%v err=%v", written, err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", after.Mode().Perm())
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("mode-only repair rewrote file bytes: before=%s after=%s", before.ModTime(), after.ModTime())
	}
}
