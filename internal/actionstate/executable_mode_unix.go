//go:build !windows

package actionstate

import "os"

func executableModeAllowed(mode os.FileMode) bool {
	return mode.IsRegular() && mode.Perm()&0o111 != 0
}
