//go:build !windows

package actionstate

import (
	"errors"
)

type directorySyncCloser interface {
	Sync() error
	Close() error
}

func syncAndCloseStateDirectory(directory directorySyncCloser) error {
	return errors.Join(directory.Sync(), directory.Close())
}
