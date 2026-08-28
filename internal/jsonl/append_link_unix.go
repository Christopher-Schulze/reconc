//go:build !windows

package jsonl

import (
	"fmt"
	"os"
	"syscall"
)

func validateAppendSingleLink(_ *os.File, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil || stat.Nlink != 1 {
		return fmt.Errorf("new JSONL live file must have exactly one directory link")
	}
	return nil
}
