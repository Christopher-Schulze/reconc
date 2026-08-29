//go:build windows

package privatefs

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var reopenFileProcedure = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReOpenFile")

type windowsSecurityHooks struct {
	reopen      func(windows.Handle, uint32, uint32, uint32) (windows.Handle, error)
	reopenPath  func(string, uint32, uint32, uint32) (windows.Handle, error)
	afterReopen func() error
}

func validatePrivateFile(file *os.File, info os.FileInfo) error {
	if err := validatePrivateFileAllowLinks(file, info); err != nil {
		return err
	}
	return validatePrivateLinkCount(file, info)
}

func validatePrivateFileAllowLinks(file *os.File, info os.FileInfo) error {
	if file == nil || info == nil || !info.Mode().IsRegular() {
		return fmt.Errorf("private file must be regular")
	}
	return validatePrivateWindowsHandle(file, false)
}

func validatePrivateLinkCount(file *os.File, _ os.FileInfo) error {
	if file == nil {
		return fmt.Errorf("private file handle is unavailable")
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return fmt.Errorf("inspect private Windows file links: %w", err)
	}
	if info.NumberOfLinks != 1 {
		return fmt.Errorf("private file must have exactly one directory link")
	}
	return nil
}

func validateDirectorySecurity(file *os.File, info os.FileInfo) error {
	if info == nil || !info.IsDir() {
		return fmt.Errorf("private directory must be a directory")
	}
	return validatePrivateWindowsHandle(file, true)
}

func secureWindowsHandle(file *os.File, directory bool) error {
	return secureWindowsHandleWithHooks(file, directory, windowsSecurityHooks{
		reopen:     reopenWindowsSecurityHandle,
		reopenPath: openWindowsSecurityHandleByPath,
	})
}

func secureWindowsHandleWithHooks(file *os.File, directory bool, hooks windowsSecurityHooks) error {
	if file == nil {
		return fmt.Errorf("private Windows file handle is unavailable")
	}
	if hooks.reopen == nil {
		return fmt.Errorf("private Windows security handle opener is unavailable")
	}
	securityHandle, err := hooks.reopen(
		windows.Handle(file.Fd()),
		windows.WRITE_DAC|windows.WRITE_OWNER,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS,
	)
	if err != nil {
		if securityHandle != windows.InvalidHandle {
			_ = windows.CloseHandle(securityHandle)
			securityHandle = windows.InvalidHandle
		}
		if hooks.reopenPath == nil {
			return fmt.Errorf("reopen private Windows security handle: %w", err)
		}
		securityHandle, err = hooks.reopenPath(
			file.Name(),
			windows.WRITE_DAC|windows.WRITE_OWNER,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS,
		)
		if err != nil {
			if securityHandle != windows.InvalidHandle {
				_ = windows.CloseHandle(securityHandle)
			}
			return fmt.Errorf("reopen private Windows security handle: %w", err)
		}
		if securityHandle == windows.InvalidHandle {
			return fmt.Errorf("reopen private Windows security handle returned an invalid handle")
		}
		if err := validateWindowsSecurityHandleIdentity(file, securityHandle); err != nil {
			return errors.Join(err, windows.CloseHandle(securityHandle))
		}
	}
	if hooks.afterReopen != nil {
		if err := hooks.afterReopen(); err != nil {
			return errors.Join(err, windows.CloseHandle(securityHandle))
		}
	}
	sid, err := currentWindowsUserSID()
	if err != nil {
		return errors.Join(err, windows.CloseHandle(securityHandle))
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
		return errors.Join(fmt.Errorf("build private Windows ACL: %w", err), windows.CloseHandle(securityHandle))
	}
	// Assigning the owner explicitly also covers elevated Windows tokens whose
	// default object owner is the Administrators group rather than the token
	// user. SetSecurityInfo binds the mutation to the reopened object handle;
	// WRITE_DAC and WRITE_OWNER are the only additional rights requested.
	securityInformation := windows.SECURITY_INFORMATION(windows.OWNER_SECURITY_INFORMATION |
		windows.DACL_SECURITY_INFORMATION |
		windows.PROTECTED_DACL_SECURITY_INFORMATION)
	setErr := windows.SetSecurityInfo(
		securityHandle, windows.SE_FILE_OBJECT, securityInformation,
		sid, nil, acl, nil,
	)
	runtime.KeepAlive(sid)
	closeErr := windows.CloseHandle(securityHandle)
	if setErr != nil {
		setErr = fmt.Errorf("set private Windows ACL through handle: %w", setErr)
	}
	return errors.Join(setErr, closeErr)
}

func reopenWindowsSecurityHandle(
	handle windows.Handle,
	desiredAccess, shareMode, flags uint32,
) (windows.Handle, error) {
	if err := reopenFileProcedure.Find(); err != nil {
		return windows.InvalidHandle, fmt.Errorf("resolve ReOpenFile: %w", err)
	}
	result, _, callErr := reopenFileProcedure.Call(
		uintptr(handle), uintptr(desiredAccess), uintptr(shareMode), uintptr(flags),
	)
	reopened := windows.Handle(result)
	if reopened == windows.InvalidHandle {
		return windows.InvalidHandle, callErr
	}
	return reopened, nil
}

func openWindowsSecurityHandleByPath(
	path string,
	desiredAccess, shareMode, flags uint32,
) (windows.Handle, error) {
	// Go's os.Root handles are opened with NtCreateFile and cannot be passed to
	// ReOpenFile. Open a no-follow security handle by the recorded name as a
	// compatibility fallback; the caller compares its volume/file identity to
	// the source handle before applying any security mutation.
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("encode private Windows security path: %w", err)
	}
	securityHandle, err := windows.CreateFile(
		pathPointer,
		desiredAccess,
		shareMode,
		nil,
		windows.OPEN_EXISTING,
		flags,
		0,
	)
	if err == nil {
		return securityHandle, nil
	}
	if shareMode&windows.FILE_SHARE_DELETE == 0 || !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return windows.InvalidHandle, err
	}
	securityHandle, retryErr := windows.CreateFile(
		pathPointer,
		desiredAccess,
		shareMode&^uint32(windows.FILE_SHARE_DELETE),
		nil,
		windows.OPEN_EXISTING,
		flags,
		0,
	)
	if retryErr != nil {
		return windows.InvalidHandle, errors.Join(err, retryErr)
	}
	return securityHandle, nil
}

func validateWindowsSecurityHandleIdentity(file *os.File, securityHandle windows.Handle) error {
	if file == nil {
		return fmt.Errorf("private Windows security source handle is unavailable")
	}
	var source, reopened windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &source); err != nil {
		return fmt.Errorf("inspect private Windows security source identity: %w", err)
	}
	if err := windows.GetFileInformationByHandle(securityHandle, &reopened); err != nil {
		return fmt.Errorf("inspect private Windows security handle identity: %w", err)
	}
	if source.VolumeSerialNumber != reopened.VolumeSerialNumber ||
		source.FileIndexHigh != reopened.FileIndexHigh || source.FileIndexLow != reopened.FileIndexLow {
		return fmt.Errorf("private Windows security handle changed identity")
	}
	return nil
}

func secureDirectoryDescriptor(file *os.File) error { return secureWindowsHandle(file, true) }
func secureFileDescriptor(file *os.File) error      { return secureWindowsHandle(file, false) }

func openDirectoryDescriptor(path string) (*os.File, error) {
	return openWindowsPrivateDescriptor(
		path,
		windows.GENERIC_READ,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
	)
}

func openExistingPrivateFileDescriptorReadOnly(path string) (*os.File, error) {
	return openWindowsPrivateDescriptor(
		path,
		windows.GENERIC_READ,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
	)
}

func openWindowsPrivateDescriptor(path string, access, disposition, attributes uint32) (*os.File, error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("encode private Windows path: %w", err)
	}
	handle, err := windows.CreateFile(
		pathPointer,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		disposition,
		attributes,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		return nil, errors.Join(
			fmt.Errorf("wrap private Windows descriptor"),
			windows.CloseHandle(handle),
		)
	}
	return file, nil
}

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
