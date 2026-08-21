//go:build windows

package runtime

import (
	"fmt"
	"os"
)

func freshnessIdentity(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	return fmt.Sprintf("%T:%s", info.Sys(), info.Name())
}
