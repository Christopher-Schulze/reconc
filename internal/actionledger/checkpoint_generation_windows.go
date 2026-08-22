//go:build windows

package actionledger

import (
	"encoding/hex"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type ledgerWindowsFileBasicInfo struct {
	CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
	Attributes                                              uint32
	_                                                       uint32
}

type ledgerWindowsFileIDInfo struct {
	VolumeSerialNumber uint64
	FileID             [16]byte
}

func ledgerFileGeneration(path string) (generation string, resultErr error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, windows.CloseHandle(handle)) }()
	var basic ledgerWindowsFileBasicInfo
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil {
		return "", err
	}
	var identity ledgerWindowsFileIDInfo
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileIdInfo,
		(*byte)(unsafe.Pointer(&identity)), uint32(unsafe.Sizeof(identity))); err != nil {
		return "", err
	}
	return fmt.Sprintf("volume=%d;file=%s;change=%d;write=%d;attributes=%d",
		identity.VolumeSerialNumber, hex.EncodeToString(identity.FileID[:]), basic.ChangeTime,
		basic.LastWriteTime, basic.Attributes), nil
}
