//go:build windows

package privatefs

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func validatePrivateFile(file *os.File, info os.FileInfo) error {
	return validatePrivateFileAllowLinks(file, info)
}

func validatePrivateFileAllowLinks(file *os.File, info os.FileInfo) error {
	if file == nil || info == nil || !info.Mode().IsRegular() {
		return fmt.Errorf("private file must be regular")
	}
	return validatePrivateWindowsHandle(file, false)
}

func validatePrivateLinkCount(os.FileInfo) error { return nil }

func validateDirectorySecurity(file *os.File, info os.FileInfo) error {
	if info == nil || !info.IsDir() {
		return fmt.Errorf("private directory must be a directory")
	}
	return validatePrivateWindowsHandle(file, true)
}

func secureWindowsHandle(file *os.File, directory bool) error {
	sid, err := currentWindowsUserSID()
	if err != nil {
		return err
	}
	var pinner runtime.Pinner
	pinner.Pin(sid)
	defer pinner.Unpin()
	inheritance := uint32(windows.NO_INHERITANCE)
	if directory {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.SET_ACCESS, Inheritance: inheritance, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(sid)}}}, nil)
	if err != nil {
		return fmt.Errorf("build private Windows ACL: %w", err)
	}
	if err := windows.SetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return fmt.Errorf("set private Windows ACL: %w", err)
	}
	runtime.KeepAlive(sid)
	return nil
}

func secureDirectoryDescriptor(file *os.File) error { return secureWindowsHandle(file, true) }
func secureFileDescriptor(file *os.File) error      { return secureWindowsHandle(file, false) }

func validatePrivateWindowsHandle(file *os.File, directory bool) error {
	if file == nil {
		return fmt.Errorf("private Windows file handle is unavailable")
	}
	descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read private Windows security descriptor: %w", err)
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("private Windows DACL is not protected")
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("read private Windows owner: %w", err)
	}
	current, err := currentWindowsUserSID()
	if err != nil {
		return err
	}
	if owner == nil || !owner.Equals(current) {
		return fmt.Errorf("private Windows object is not owned by the current user")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read private Windows DACL: %w", err)
	}
	if dacl == nil || dacl.AceCount == 0 {
		return fmt.Errorf("private Windows DACL has no access entries")
	}
	return validatePrivateWindowsDACL(dacl, current, directory)
}

type windowsAccessCoverage struct{ object, childFiles, childDirs uint32 }

func validatePrivateWindowsDACL(dacl *windows.ACL, current *windows.SID, directory bool) error {
	var coverage windowsAccessCoverage
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		entry, err := privateWindowsAccessEntry(dacl, index, current, directory)
		if err != nil {
			return err
		}
		coverage.object |= entry.object
		coverage.childFiles |= entry.childFiles
		coverage.childDirs |= entry.childDirs
	}
	if !windowsPermissionsGrantFullAccess(coverage.object) || directory && (!windowsPermissionsGrantFullAccess(coverage.childFiles) || !windowsPermissionsGrantFullAccess(coverage.childDirs)) {
		return fmt.Errorf("private Windows DACL lacks required current-user access")
	}
	return nil
}

func privateWindowsAccessEntry(dacl *windows.ACL, index uint32, current *windows.SID, directory bool) (windowsAccessCoverage, error) {
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, index, &ace); err != nil || ace == nil {
		return windowsAccessCoverage{}, fmt.Errorf("read private Windows access entry %d: %w", index, err)
	}
	allowedFlags := uint8(windows.NO_INHERITANCE)
	if directory {
		allowedFlags = uint8(windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE | windows.INHERIT_ONLY_ACE)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&^allowedFlags != 0 || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 && ace.Header.AceFlags&(windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE) == 0 {
		return windowsAccessCoverage{}, fmt.Errorf("private Windows DACL entry inheritance is invalid")
	}
	entrySID, err := windowsAccessEntrySID(ace)
	if err != nil {
		return windowsAccessCoverage{}, err
	}
	if !entrySID.Equals(current) {
		return windowsAccessCoverage{}, fmt.Errorf("private Windows DACL grants access to an unexpected identity")
	}
	return windowsCoverageForAccess(uint32(ace.Mask), ace.Header.AceFlags), nil
}

func windowsAccessEntrySID(ace *windows.ACCESS_ALLOWED_ACE) (*windows.SID, error) {
	const minimumSIDBytes = uint16(8)
	sidOffset := uint16(unsafe.Offsetof(ace.SidStart))
	if ace.Header.AceSize < sidOffset+minimumSIDBytes {
		return nil, fmt.Errorf("private Windows access entry is truncated")
	}
	entrySID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !entrySID.IsValid() || entrySID.Len() > int(ace.Header.AceSize-sidOffset) {
		return nil, fmt.Errorf("private Windows access entry identity is invalid")
	}
	return entrySID, nil
}

func windowsCoverageForAccess(permissions uint32, flags uint8) windowsAccessCoverage {
	coverage := windowsAccessCoverage{}
	if flags&windows.INHERIT_ONLY_ACE == 0 {
		coverage.object = permissions
	}
	if flags&windows.OBJECT_INHERIT_ACE != 0 {
		coverage.childFiles = permissions
	}
	if flags&windows.CONTAINER_INHERIT_ACE != 0 {
		coverage.childDirs = permissions
	}
	return coverage
}
func windowsPermissionsGrantFullAccess(permissions uint32) bool {
	const all = uint32(windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff)
	return permissions&uint32(windows.GENERIC_ALL) != 0 || permissions&all == all
}
func currentWindowsUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read current Windows user identity: %w", err)
	}
	if user == nil || user.User.Sid == nil || !user.User.Sid.IsValid() {
		return nil, fmt.Errorf("current Windows user identity is invalid")
	}
	sid, err := user.User.Sid.Copy()
	if err != nil {
		return nil, fmt.Errorf("copy current Windows user identity: %w", err)
	}
	return sid, nil
}
