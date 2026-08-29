//go:build windows

package jsonl

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func validateAppendSingleLink(file *os.File, _ os.FileInfo) error {
	links, err := appendFileLinkCount(file, nil)
	if err != nil {
		return err
	}
	if links != 1 {
		return fmt.Errorf("new JSONL live file must have exactly one directory link")
	}
	return nil
}

func appendFileLinkCount(file *os.File, _ os.FileInfo) (uint64, error) {
	if file == nil {
		return 0, fmt.Errorf("JSONL file handle is unavailable for link-count inspection")
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return 0, fmt.Errorf("inspect JSONL file link count: %w", err)
	}
	return uint64(info.NumberOfLinks), nil
}

func appendPathLinkCount(path string, _ os.FileInfo) (uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	links, linkErr := appendFileLinkCount(file, nil)
	return links, errors.Join(linkErr, file.Close())
}
