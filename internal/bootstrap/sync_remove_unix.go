//go:build !windows

package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
)

func syncRemovalParent(path string) error {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
