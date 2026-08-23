//go:build windows

package usercli

import (
	"os"

	"golang.org/x/sys/windows"
)

func releaseCandidateModeMatches(path string, info os.FileInfo, requested os.FileMode) (bool, error) {
	if info == nil {
		return false, nil
	}
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attributes, err := windows.GetFileAttributes(pathPointer)
	if err != nil {
		return false, err
	}
	wantReadOnly := requested.Perm()&0o200 == 0
	return (attributes&windows.FILE_ATTRIBUTE_READONLY != 0) == wantReadOnly, nil
}
