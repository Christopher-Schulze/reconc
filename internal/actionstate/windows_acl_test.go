//go:build windows

package actionstate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"

	"reconc.dev/reconc/internal/privatefs"
)

func TestWindowsPrivateACLAllowsOnlyTheCurrentOwner(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := privatefs.RepairDirectory(directory); err != nil {
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

func TestWindowsPrivateACLAcceptsSplitOwnerOnlyDirectoryCoverage(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private-split")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	current, err := currentWindowsUserSID()
	if err != nil {
		t.Fatal(err)
	}
	var pinner runtime.Pinner
	pinner.Pin(current)
	defer pinner.Unpin()
	entries := []windows.EXPLICIT_ACCESS{
		windowsTestAccessEntry(current, windows.GRANT_ACCESS, windows.GENERIC_ALL, windows.NO_INHERITANCE),
		windowsTestAccessEntry(
			current,
			windows.GRANT_ACCESS,
			windows.GENERIC_ALL,
			windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE|windows.INHERIT_ONLY_ACE,
		),
	}
	setWindowsTestDACL(t, directory, entries, true)
	if count := windowsTestDACLCount(t, directory); count < 2 {
		t.Fatalf("split owner-only DACL has %d ACEs, want at least 2", count)
	}
	if err := validatePrivateDirectory(directory); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsPrivateACLRejectsUnsafeOrIncompleteDirectoryCoverage(t *testing.T) {
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
	fullInheritance := uint32(windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE)
	tests := []struct {
		name      string
		protected bool
		entries   []windows.EXPLICIT_ACCESS
	}{
		{
			name: "foreign identity", protected: true,
			entries: []windows.EXPLICIT_ACCESS{
				windowsTestAccessEntry(current, windows.GRANT_ACCESS, windows.GENERIC_ALL, fullInheritance),
				windowsTestAccessEntry(world, windows.GRANT_ACCESS, windows.GENERIC_ALL, fullInheritance),
			},
		},
		{
			name: "deny entry", protected: true,
			entries: []windows.EXPLICIT_ACCESS{
				windowsTestAccessEntry(current, windows.DENY_ACCESS, windows.GENERIC_READ, windows.NO_INHERITANCE),
				windowsTestAccessEntry(current, windows.GRANT_ACCESS, windows.GENERIC_ALL, fullInheritance),
			},
		},
		{
			name: "object only", protected: true,
			entries: []windows.EXPLICIT_ACCESS{
				windowsTestAccessEntry(current, windows.SET_ACCESS, windows.GENERIC_ALL, windows.NO_INHERITANCE),
			},
		},
		{
			name: "children only", protected: true,
			entries: []windows.EXPLICIT_ACCESS{
				windowsTestAccessEntry(
					current, windows.SET_ACCESS, windows.GENERIC_ALL,
					fullInheritance|windows.INHERIT_ONLY_ACE,
				),
			},
		},
		{
			name: "file children only", protected: true,
			entries: []windows.EXPLICIT_ACCESS{
				windowsTestAccessEntry(current, windows.GRANT_ACCESS, windows.GENERIC_ALL, windows.NO_INHERITANCE),
				windowsTestAccessEntry(
					current, windows.GRANT_ACCESS, windows.GENERIC_ALL,
					windows.OBJECT_INHERIT_ACE|windows.INHERIT_ONLY_ACE,
				),
			},
		},
		{
			name: "directory children only", protected: true,
			entries: []windows.EXPLICIT_ACCESS{
				windowsTestAccessEntry(current, windows.GRANT_ACCESS, windows.GENERIC_ALL, windows.NO_INHERITANCE),
				windowsTestAccessEntry(
					current, windows.GRANT_ACCESS, windows.GENERIC_ALL,
					windows.CONTAINER_INHERIT_ACE|windows.INHERIT_ONLY_ACE,
				),
			},
		},
		{
			name: "insufficient permissions", protected: true,
			entries: []windows.EXPLICIT_ACCESS{
				windowsTestAccessEntry(current, windows.SET_ACCESS, windows.GENERIC_READ, fullInheritance),
			},
		},
		{
			name: "no propagate", protected: true,
			entries: []windows.EXPLICIT_ACCESS{
				windowsTestAccessEntry(
					current, windows.SET_ACCESS, windows.GENERIC_ALL,
					fullInheritance|windows.NO_PROPAGATE_INHERIT_ACE,
				),
			},
		},
		{
			name: "unprotected", protected: false,
			entries: []windows.EXPLICIT_ACCESS{
				windowsTestAccessEntry(current, windows.SET_ACCESS, windows.GENERIC_ALL, fullInheritance),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "private")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			setWindowsTestDACL(t, directory, test.entries, test.protected)
			if err := validatePrivateDirectory(directory); err == nil {
				t.Fatal("unsafe or incomplete private Windows DACL was accepted")
			}
		})
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

func windowsTestAccessEntry(
	sid *windows.SID,
	mode windows.ACCESS_MODE,
	permissions windows.ACCESS_MASK,
	inheritance uint32,
) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: permissions,
		AccessMode:        mode,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

func setWindowsTestDACL(
	t *testing.T,
	path string,
	entries []windows.EXPLICIT_ACCESS,
	protected bool,
) {
	t.Helper()
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	information := windows.SECURITY_INFORMATION(
		windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
	)
	if protected {
		information = windows.SECURITY_INFORMATION(
			windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION,
		)
	}
	if err := windows.SetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT, information, nil, nil, acl, nil,
	); err != nil {
		t.Fatal(err)
	}
}

func windowsTestDACLCount(t *testing.T, path string) uint16 {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("read split owner-only DACL: %v", err)
	}
	return dacl.AceCount
}
