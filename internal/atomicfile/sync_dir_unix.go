//go:build !windows

package atomicfile

import (
	"errors"
	"os"
)

func syncParentDir(directory *os.Root) error {
	dir, err := directory.Open(".")
	if err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		return errors.Join(err, dir.Close())
	}
	return dir.Close()
}
