//go:build windows

package atomicfile

import (
	"errors"
	"os"
)

func syncParentDir(directory *os.Root) error {
	// Root.Open keeps the directory identity bound; File.Sync maps to
	// FlushFileBuffers on Windows and provides the durable publication fence.
	dir, err := directory.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
