//go:build windows

package runtime

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func freshnessIdentity(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	return fmt.Sprintf("%T:%s", info.Sys(), info.Name())
}

func freshnessFileIdentity(file *os.File, info os.FileInfo) (string, error) {
	if file == nil || info == nil {
		return "", fmt.Errorf("runtime freshness file identity requires an opened file")
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &information); err != nil {
		return "", fmt.Errorf("inspect runtime freshness file identity: %w", err)
	}
	return fmt.Sprintf(
		"volume:%x:file:%08x%08x",
		information.VolumeSerialNumber, information.FileIndexHigh, information.FileIndexLow,
	), nil
}
