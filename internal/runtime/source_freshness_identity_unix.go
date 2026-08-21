//go:build !windows

package runtime

import (
	"fmt"
	"os"
	"syscall"
)

func freshnessIdentity(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return fmt.Sprintf("%T", info.Sys())
	}
	return fmt.Sprintf("dev=%d;ino=%d", stat.Dev, stat.Ino)
}
