//go:build windows

package execfile

import "os"

func executableMode(os.FileMode) bool {
	return true
}
