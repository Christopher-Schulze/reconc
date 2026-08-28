//go:build !windows

package atomicfile

import (
	"errors"
	"os"
)

type directorySyncCloser interface {
	Sync() error
	Close() error
}

func syncParentDir(directory *os.Root) error {
	dir, err := directory.Open(".")
	if err != nil {
		return err
	}
	return syncAndCloseDirectory(dir)
}

func syncAndCloseDirectory(directory directorySyncCloser) error {
	return errors.Join(directory.Sync(), directory.Close())
}
