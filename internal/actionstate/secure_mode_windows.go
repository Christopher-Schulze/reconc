//go:build windows

package actionstate

import "os"

func secureDirectoryMode(path string, _ os.FileMode) error {
	return securePrivateWindowsPath(path, true)
}

func securePrivateFileMode(path string, _ os.FileMode) error {
	return securePrivateWindowsPath(path, false)
}
