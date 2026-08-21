//go:build windows

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
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
	if !windowsFileReadOnly(t, path) {
		t.Fatal("file remains writable")
	}
	written, err = WriteIfChanged(path, []byte("private\n"), 0o600)
	if err != nil || !written {
		t.Fatalf("make writable: written=%v err=%v", written, err)
	}
	if windowsFileReadOnly(t, path) {
		t.Fatal("file remains read-only")
	}
}

func windowsFileReadOnly(t *testing.T, path string) bool {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := windows.GetFileAttributes(name)
	if err != nil {
		t.Fatal(err)
	}
	return attributes&windows.FILE_ATTRIBUTE_READONLY != 0
}
