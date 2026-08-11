//go:build !windows

package actionstate

import (
	"fmt"
	"os"
	"syscall"
)

func filesystemObjectIdentity(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return "", fmt.Errorf("filesystem object identity is unavailable")
	}
	return fmt.Sprintf("dev:%x:ino:%x", uint64(stat.Dev), uint64(stat.Ino)), nil
}
