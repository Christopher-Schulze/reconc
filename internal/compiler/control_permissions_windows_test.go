//go:build windows

package compiler

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCompileFirstInheritsRepositoryControlACL(t *testing.T) {
	repo := t.TempDir()
	release, err := AcquireCompileLock(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	assertInheritedControlACL(t, filepath.Join(repo, ".reconc"))
}

func assertInheritedControlACL(t *testing.T, path string) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED != 0 {
		t.Fatal("compiler-first control root replaced its inherited repository DACL")
	}
}
