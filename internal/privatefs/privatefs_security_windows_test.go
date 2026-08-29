//go:build windows

package privatefs

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

func assertPrivateLockSecurity(t *testing.T, path string) {
	t.Helper()
	file, err := OpenExistingLock(path)
	if err != nil {
		t.Fatalf("lock security: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSecureWindowsDescriptorPersistsProtectedDACL(t *testing.T) {
	path := t.TempDir()
	file, _, err := openDirectory(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureDirectoryDescriptor(file); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := validatePrivateWindowsHandle(file, true); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSecureWindowsHandleRequestsSecurityMutationRights(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(path, []byte("{}\n"), PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var source windows.Handle
	var access, share, flags uint32
	err = secureWindowsHandleWithHooks(file, false, windowsSecurityHooks{
		reopen: func(handle windows.Handle, desiredAccess, shareMode, attributes uint32) (windows.Handle, error) {
			source, access, share, flags = handle, desiredAccess, shareMode, attributes
			return windows.InvalidHandle, windows.ERROR_ACCESS_DENIED
		},
	})
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("security-handle access error = %v, want access denied", err)
	}
	if source != windows.Handle(file.Fd()) {
		t.Fatal("security writer did not reopen the supplied file handle")
	}
	if access != windowsSecurityMutationAccess {
		t.Fatalf("security-handle access = %#x, want READ_CONTROL|WRITE_DAC|WRITE_OWNER", access)
	}
	if share != windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE {
		t.Fatalf("security-handle share mode = %#x", share)
	}
	wantFlags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT | windows.FILE_FLAG_BACKUP_SEMANTICS)
	if flags != wantFlags {
		t.Fatalf("security-handle flags = %#x, want %#x", flags, wantFlags)
	}
}

func TestSecureWindowsHandleTargetsOpenedIdentityAfterReplacement(t *testing.T) {
	directory := t.TempDir()
	openedPath := filepath.Join(directory, "opened.json")
	replacementPath := filepath.Join(directory, "replacement.json")
	movedPath := filepath.Join(directory, "moved.json")
	for _, path := range []string{openedPath, replacementPath} {
		if err := os.WriteFile(path, []byte("{}\n"), PrivateFileMode); err != nil {
			t.Fatal(err)
		}
		setUnsafeWindowsTestDACL(t, path)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := root.OpenFile(filepath.Base(openedPath), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	err = secureWindowsHandleWithHooks(file, false, windowsSecurityHooks{
		reopen:     reopenWindowsSecurityHandle,
		reopenPath: openWindowsSecurityHandleByPath,
		afterReopen: func() error {
			if err := os.Rename(openedPath, movedPath); err != nil {
				return err
			}
			return os.Rename(replacementPath, openedPath)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateWindowsHandle(file, false); err != nil {
		t.Fatalf("opened identity was not secured: %v", err)
	}
	replacement, err := os.Open(openedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if err := validatePrivateWindowsHandle(replacement, false); err == nil {
		t.Fatal("path replacement received the opened identity's private ACL")
	}
}

func TestOpenLockRejectsWindowsReparsePointWithoutMutatingTargetACL(t *testing.T) {
	privateDirectory := filepath.Join(t.TempDir(), "private")
	if err := RepairDirectory(privateDirectory); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.lock")
	if err := os.WriteFile(target, []byte("target"), PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	setUnsafeWindowsTestDACL(t, target)
	before := windowsTestSecurityDescriptor(t, target)
	link := filepath.Join(privateDirectory, "state.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("Windows symbolic links are unavailable: %v", err)
	}
	if file, err := OpenLock(link); err == nil {
		_ = file.Close()
		t.Fatal("private lock accepted a Windows reparse point")
	}
	if after := windowsTestSecurityDescriptor(t, target); after != before {
		t.Fatalf("rejected reparse point changed target security: before=%q after=%q", before, after)
	}
}

func setUnsafeWindowsTestDACL(t *testing.T, path string) {
	t.Helper()
	current, err := currentWindowsUserSID()
	if err != nil {
		t.Fatal(err)
	}
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	var pinner runtime.Pinner
	pinner.Pin(current)
	pinner.Pin(world)
	defer pinner.Unpin()
	entries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.SET_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(current),
			},
		},
		{
			AccessPermissions: windows.GENERIC_READ,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(world),
			},
		},
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(current)
	runtime.KeepAlive(world)
}

func windowsTestSecurityDescriptor(t *testing.T, path string) string {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	return descriptor.String()
}
