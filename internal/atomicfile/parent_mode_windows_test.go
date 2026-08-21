//go:build windows

package atomicfile

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func assertPublicationParent(t *testing.T, path string, _ os.FileMode) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := windows.GetFileAttributes(name)
	if err != nil {
		t.Fatal(err)
	}
	if attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		t.Fatalf("publication parent attributes = %#x", attributes)
	}
}
