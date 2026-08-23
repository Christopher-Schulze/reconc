package boundedio

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRegularFileSnapshotRejectsPathReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("replacing an open file is not supported by Windows sharing semantics")
	}
	root := t.TempDir()
	path := filepath.Join(root, "input")
	replacement := filepath.Join(root, "replacement")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("after!"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := WithRegularFileSnapshot(path, 6, func(_ *os.File, _ os.FileInfo) error {
		return os.Rename(replacement, path)
	})
	if err == nil || !strings.Contains(err.Error(), "changed while reading") {
		t.Fatalf("replacement was accepted: %v", err)
	}
}
