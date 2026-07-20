//go:build windows

package pathidentity

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const (
	maximumWindowsPathCodeUnits = 1 << 16
	windowsVolumeNameDOS        = 0
)

func resolveExistingOS(path string) (resolved string, err error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(
		pathPointer,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return "", err
	}
	defer func() {
		err = errors.Join(err, windows.CloseHandle(handle))
	}()

	bufferSize := uint32(512)
	for bufferSize <= maximumWindowsPathCodeUnits {
		buffer := make([]uint16, bufferSize)
		length, callErr := windows.GetFinalPathNameByHandle(
			handle,
			&buffer[0],
			uint32(len(buffer)),
			windowsVolumeNameDOS,
		)
		if callErr != nil {
			return "", callErr
		}
		if length < uint32(len(buffer)) {
			return normalizeWindowsFinalPath(windows.UTF16ToString(buffer[:length])), nil
		}
		if length >= maximumWindowsPathCodeUnits {
			return "", fmt.Errorf("final path requires %d UTF-16 code units", length)
		}
		bufferSize = length + 1
	}
	return "", fmt.Errorf("final path exceeds %d UTF-16 code units", maximumWindowsPathCodeUnits)
}

func existingAliasesOS(path string) []string {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	bufferSize := uint32(512)
	for bufferSize <= maximumWindowsPathCodeUnits {
		buffer := make([]uint16, bufferSize)
		length, callErr := windows.GetShortPathName(pathPointer, &buffer[0], uint32(len(buffer)))
		if callErr != nil || length == 0 {
			return nil
		}
		if length < uint32(len(buffer)) {
			return []string{filepath.Clean(windows.UTF16ToString(buffer[:length]))}
		}
		if length >= maximumWindowsPathCodeUnits {
			return nil
		}
		bufferSize = length + 1
	}
	return nil
}

func normalizeWindowsFinalPath(path string) string {
	const (
		extendedPrefix = `\\?\`
		extendedUNC    = `\\?\UNC\`
	)
	switch {
	case strings.HasPrefix(path, extendedUNC):
		return `\\` + strings.TrimPrefix(path, extendedUNC)
	case strings.HasPrefix(path, extendedPrefix):
		return strings.TrimPrefix(path, extendedPrefix)
	default:
		return path
	}
}

func aliasComparisonKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}
