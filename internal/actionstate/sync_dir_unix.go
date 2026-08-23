//go:build !windows

package actionstate

import (
	"errors"
	"os"
)

type directorySyncCloser interface {
	Sync() error
	Close() error
}

func syncStateDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return syncAndCloseStateDirectory(directory)
}

func syncAndCloseStateDirectory(directory directorySyncCloser) error {
	return errors.Join(directory.Sync(), directory.Close())
}
