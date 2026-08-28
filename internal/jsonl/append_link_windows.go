//go:build windows

package jsonl

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func validateAppendSingleLink(file *os.File, _ os.FileInfo) error {
	if file == nil {
		return fmt.Errorf("new JSONL live file handle is unavailable")
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return fmt.Errorf("inspect new JSONL live file links: %w", err)
	}
	if info.NumberOfLinks != 1 {
		return fmt.Errorf("new JSONL live file must have exactly one directory link")
	}
	return nil
}
