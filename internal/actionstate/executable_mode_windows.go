//go:build windows

package actionstate

import "os"

func executableModeAllowed(mode os.FileMode) bool {
	return mode.IsRegular()
}
