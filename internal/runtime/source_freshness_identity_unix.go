//go:build !windows

package runtime

import (
	"fmt"
	"os"
	"strconv"
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
	var storage [64]byte
	identity := append(storage[:0], "dev="...)
	identity = strconv.AppendUint(identity, uint64(stat.Dev), 10)
	identity = append(identity, ";ino="...)
	identity = strconv.AppendUint(identity, uint64(stat.Ino), 10)
	return string(identity)
}
