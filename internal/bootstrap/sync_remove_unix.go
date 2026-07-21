//go:build !windows

package bootstrap

import (
	"os"
	"path/filepath"
)

func syncRemovalParent(path string) error {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}
