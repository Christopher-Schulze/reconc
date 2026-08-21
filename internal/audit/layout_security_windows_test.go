//go:build windows

package audit

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"

	"reconc.dev/reconc/internal/privatefs"
)

func assertPrivateDirectorySecurity(t *testing.T, path string, _ os.FileMode) {
	t.Helper()
	if err := privatefs.ValidateDirectory(path); err != nil {
		t.Fatal(err)
	}
}

func assertPrivateFileSecurity(t *testing.T, path string, _ os.FileMode) {
	t.Helper()
	file, err := privatefs.OpenExistingPrivateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func driftPrivateDirectorySecurity(t *testing.T, path string, _ os.FileMode) {
	unprotectPrivateDACL(t, path)
}

func driftPrivateFileSecurity(t *testing.T, path string, _ os.FileMode) {
	unprotectPrivateDACL(t, path)
}

func unprotectPrivateDACL(t *testing.T, path string) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("read private DACL: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		t.Fatal(err)
	}
}
