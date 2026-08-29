//go:build !windows

package jsonl

import (
	"fmt"
	"os"
	"syscall"
)

func validateAppendSingleLink(_ *os.File, info os.FileInfo) error {
	links, err := appendFileLinkCount(nil, info)
	if err != nil {
		return err
	}
	if links != 1 {
		return fmt.Errorf("new JSONL live file must have exactly one directory link")
	}
	return nil
}

func appendFileLinkCount(_ *os.File, info os.FileInfo) (uint64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return 0, fmt.Errorf("inspect JSONL file link count: unsupported file metadata")
	}
	return uint64(stat.Nlink), nil
}

func appendPathLinkCount(_ string, info os.FileInfo) (uint64, error) {
	return appendFileLinkCount(nil, info)
}
