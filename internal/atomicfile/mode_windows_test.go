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

func TestWriteIfChangedReconcilesRepresentableReadOnlyDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("private\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	written, err := WriteIfChanged(path, []byte("private\n"), 0o400)
	if err != nil || !written {
		t.Fatalf("make read-only: written=%v err=%v", written, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o200 != 0 {
		t.Fatalf("file remains writable: mode=%#o", info.Mode().Perm())
	}
	written, err = WriteIfChanged(path, []byte("private\n"), 0o600)
	if err != nil || !written {
		t.Fatalf("make writable: written=%v err=%v", written, err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o200 == 0 {
		t.Fatalf("file remains read-only: mode=%#o", info.Mode().Perm())
	}
}
