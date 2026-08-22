//go:build !windows

package runtime

import (
	"fmt"
	"os"
	"syscall"
)

func evidenceObjectIdentity(_ string, info os.FileInfo) (string, error) {
	if info == nil {
		return "", fmt.Errorf("evidence object identity is unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return "", fmt.Errorf("evidence object identity is unavailable")
	}
	return fmt.Sprintf("dev:%x:ino:%x", uint64(stat.Dev), uint64(stat.Ino)), nil
}
