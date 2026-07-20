//go:build windows

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteIfChangedIgnoresUnrepresentablePOSIXModeDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("private\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	written, err := WriteIfChanged(path, []byte("private\n"), 0o600)
	if err != nil || written {
		t.Fatalf("identical Windows write: written=%v err=%v", written, err)
	}
}
