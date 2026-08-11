//go:build windows

package actionstate

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsFileAllAccess = uint32(windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff)

func securePrivateWindowsPath(path string, directory bool) error {
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
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("build private Windows ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	); err != nil {
		return fmt.Errorf("set private Windows ACL: %w", err)
	}
	runtime.KeepAlive(sid)
	return nil
}

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
	if err != nil || dacl == nil || dacl.AceCount != 1 {
		return fmt.Errorf("private Windows DACL must contain exactly one access entry")
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil || ace == nil {
		return fmt.Errorf("read private Windows access entry: %w", err)
	}
	wantFlags := uint8(windows.NO_INHERITANCE)
	if directory {
		wantFlags = uint8(windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != wantFlags {
		return fmt.Errorf("private Windows access entry type or inheritance is invalid")
	}
	permissions := uint32(ace.Mask)
	if permissions != uint32(windows.GENERIC_ALL) && permissions&windowsFileAllAccess != windowsFileAllAccess {
		return fmt.Errorf("private Windows access entry lacks full current-user access")
	}
	entrySID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !entrySID.IsValid() || !entrySID.Equals(current) {
		return fmt.Errorf("private Windows DACL grants access to an unexpected identity")
	}
	return nil
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
