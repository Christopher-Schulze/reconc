//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompareCurrentFailuresDoNotLeakDescriptors(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state")
	directory, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := openDescriptorCount(t)
	for range 512 {
		file, info, err := openCurrent(directory, "state", path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := compareCurrent(directory, "state", path, file, info, []byte("data")); err == nil {
			t.Fatal("concurrent truncation did not fail comparison")
		}
		if _, err := file.Stat(); err == nil {
			t.Fatal("failed comparison left current descriptor open")
		}
		if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	after := openDescriptorCount(t)
	if after != before {
		t.Fatalf("open descriptor count grew from %d to %d", before, after)
	}
}

func openDescriptorCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Skipf("descriptor inventory unavailable: %v", err)
	}
	return len(entries)
}
