//go:build windows

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func assertPublishedFileMode(t *testing.T, path string, _ os.FileInfo, want os.FileMode) {
	t.Helper()
	wantReadOnly := want.Perm()&0o200 == 0
	if got := windowsFileReadOnly(t, path); got != wantReadOnly {
		t.Fatalf("published readonly attribute = %t, want %t", got, wantReadOnly)
	}
}

func TestWriteIfChangedIgnoresUnrepresentablePOSIXModeDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("private\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	written, err := WriteIfChanged(path, []byte("private\n"), 0o600)
	if err != nil || written.Changed {
		t.Fatalf("identical Windows write: written=%v err=%v", written, err)
	}
}

func TestWriteIfChangedReconcilesRepresentableReadOnlyDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("private\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	written, err := WriteIfChanged(path, []byte("private\n"), 0o400)
	if err != nil || !written.Changed {
		t.Fatalf("make read-only: written=%v err=%v", written, err)
	}
	if !windowsFileReadOnly(t, path) {
		t.Fatal("file remains writable")
	}
	written, err = WriteIfChanged(path, []byte("private\n"), 0o600)
	if err != nil || !written.Changed {
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
