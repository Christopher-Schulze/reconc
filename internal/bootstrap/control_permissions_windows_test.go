//go:build windows

package bootstrap

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestBootstrapFirstInheritsRepositoryControlACL(t *testing.T) {
	repo := t.TempDir()
	rootRef, _, err := openBootstrapRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	finalRef, _, _, err := createSafeParentsWithRoot(repo, rootRef, filepath.Join(repo, ".reconc"))
	if err != nil {
		t.Fatal(err)
	}
	if err := closeBootstrapRootRef(finalRef); err != nil {
		t.Fatal(err)
	}
	if err := closeBootstrapRootRef(rootRef); err != nil {
		t.Fatal(err)
	}

	descriptor, err := windows.GetNamedSecurityInfo(
		filepath.Join(repo, ".reconc"), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED != 0 {
		t.Fatal("bootstrap-first control root replaced its inherited repository DACL")
	}
}
