//go:build !windows

package execfile

import "os"

func executableMode(mode os.FileMode) bool {
	return mode&0o111 != 0
}
