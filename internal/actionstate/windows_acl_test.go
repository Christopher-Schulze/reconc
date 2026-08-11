//go:build windows

package actionstate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPrivateACLAllowsOnlyTheCurrentOwner(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := securePrivateWindowsPath(directory, true); err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateDirectory(directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "state.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := securePublishedPrivateFile(path); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateRegularFile(path, 64); err != nil {
		t.Fatal(err)
	}

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
		windowsAccessEntry(current, windows.TRUSTEE_IS_USER),
		windowsAccessEntry(world, windows.TRUSTEE_IS_WELL_KNOWN_GROUP),
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
	if _, err := readPrivateRegularFile(path, 64); err == nil {
		t.Fatal("private state accepted a DACL granting another identity access")
	}
}

func windowsAccessEntry(sid *windows.SID, trusteeType windows.TRUSTEE_TYPE) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.SET_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: trusteeType,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}
