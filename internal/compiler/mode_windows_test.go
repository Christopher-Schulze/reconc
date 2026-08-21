//go:build windows

package compiler

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func assertRepresentableFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := windows.GetFileAttributes(name)
	if err != nil {
		t.Fatal(err)
	}
	wantReadOnly := want.Perm()&0o200 == 0
	gotReadOnly := attributes&windows.FILE_ATTRIBUTE_READONLY != 0
	if gotReadOnly != wantReadOnly {
		t.Fatalf("readonly attribute = %t, want %t", gotReadOnly, wantReadOnly)
	}
}
