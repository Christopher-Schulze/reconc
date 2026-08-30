//go:build windows

package actionstate

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsFileAllAccess = uint32(windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff)

func validatePrivateWindowsHandle(file *os.File, directory bool) error {
	if file == nil {
		return fmt.Errorf("private Windows file handle is unavailable")
	}
	descriptor, err := windows.GetSecurityInfo(
		windows.Handle(file.Fd()), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION,
	)
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

type windowsAccessCoverage struct {
	object     uint32
	childFiles uint32
	childDirs  uint32
}

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
	if !windowsPermissionsGrantFullAccess(coverage.object) {
		return fmt.Errorf("private Windows DACL lacks full current-user access to the object")
	}
	if directory && (!windowsPermissionsGrantFullAccess(coverage.childFiles) ||
		!windowsPermissionsGrantFullAccess(coverage.childDirs)) {
		return fmt.Errorf("private Windows directory DACL lacks inheritable full current-user access")
	}
	return nil
}

func privateWindowsAccessEntry(
	dacl *windows.ACL,
	index uint32,
	current *windows.SID,
	directory bool,
) (windowsAccessCoverage, error) {
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, index, &ace); err != nil {
		return windowsAccessCoverage{}, fmt.Errorf("read private Windows access entry %d: %w", index, err)
	}
	if ace == nil {
		return windowsAccessCoverage{}, fmt.Errorf("private Windows access entry %d is unavailable", index)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return windowsAccessCoverage{}, fmt.Errorf("private Windows DACL contains a non-allow access entry")
	}
	flags := ace.Header.AceFlags
	allowedFlags := uint8(windows.NO_INHERITANCE)
	if directory {
		allowedFlags = uint8(windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE | windows.INHERIT_ONLY_ACE)
	}
	if flags & ^allowedFlags != 0 || flags&windows.INHERIT_ONLY_ACE != 0 &&
		flags&(windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE) == 0 {
		return windowsAccessCoverage{}, fmt.Errorf("private Windows access entry inheritance is invalid")
	}
	entrySID, err := windowsAccessEntrySID(ace)
	if err != nil {
		return windowsAccessCoverage{}, err
	}
	if !entrySID.Equals(current) {
		return windowsAccessCoverage{}, fmt.Errorf("private Windows DACL grants access to an unexpected identity")
	}
	return windowsCoverageForAccess(uint32(ace.Mask), flags), nil
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
	var coverage windowsAccessCoverage
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
	return permissions&uint32(windows.GENERIC_ALL) != 0 ||
		permissions&windowsFileAllAccess == windowsFileAllAccess
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
