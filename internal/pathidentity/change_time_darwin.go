//go:build darwin

package pathidentity

import (
	"os"
	"syscall"
)

func changeTime(info os.FileInfo) (int64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return 0, false
	}
	return stat.Ctimespec.Nano(), true
}
