//go:build !windows

package actionstate

import "os"

func secureDirectoryMode(path string, current os.FileMode) error {
	if current.Perm() == 0o700 {
		return nil
	}
	return os.Chmod(path, 0o700)
}

func securePrivateFileMode(path string, current os.FileMode) error {
	if current.Perm() == 0o600 {
		return nil
	}
	return os.Chmod(path, 0o600)
}
