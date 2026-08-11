//go:build windows

package actionstate

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

func filesystemObjectIdentity(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(
		name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0,
	)
	if err != nil {
		return "", err
	}
	var information windows.ByHandleFileInformation
	infoErr := windows.GetFileInformationByHandle(handle, &information)
	closeErr := windows.CloseHandle(handle)
	if infoErr != nil || closeErr != nil {
		return "", errors.Join(infoErr, closeErr)
	}
	return fmt.Sprintf(
		"volume:%x:file:%08x%08x",
		information.VolumeSerialNumber, information.FileIndexHigh, information.FileIndexLow,
	), nil
}
