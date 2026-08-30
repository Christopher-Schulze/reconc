//go:build windows

package repositorycontrol

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"reconc.dev/reconc/internal/privatefs"
)

func TestEnsureRootInheritsRepositoryACLAndRunIsPrivate(t *testing.T) {
	repo := t.TempDir()
	if err := EnsureRoot(repo); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(repo, RootName)
	descriptor, err := windows.GetNamedSecurityInfo(
		root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED != 0 {
		t.Fatal("public repository control root replaced inherited ACL with a protected DACL")
	}
	if err := EnsureRunDirectory(repo); err != nil {
		t.Fatal(err)
	}
	if err := privatefs.ValidateDirectory(filepath.Join(root, RunName)); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureRootPreservesExistingPrivateACL(t *testing.T) {
	repo := t.TempDir()
	root := filepath.Join(repo, RootName)
	if err := privatefs.RepairDirectory(root); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRoot(repo); err != nil {
		t.Fatal(err)
	}
	if err := privatefs.ValidateDirectory(root); err != nil {
		t.Fatalf("existing protected root ACL changed: %v", err)
	}
}
