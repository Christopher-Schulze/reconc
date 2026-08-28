package bootstrap

import (
	"errors"
	"os"

	"reconc.dev/reconc/internal/atomicfile"
)

var bootstrapDirectorySync = atomicfile.SyncDirectory

func syncMutatedBootstrapParent(parent *os.Root, expected os.FileInfo, path string) error {
	syncErr := bootstrapDirectorySync(parent)
	return errors.Join(syncErr, validateCreatedParent(parent, expected, path))
}

func removeBoundBootstrapEntry(
	parent *os.Root,
	expected os.FileInfo,
	name string,
	path string,
) error {
	if err := validateCreatedParent(parent, expected, path); err != nil {
		return err
	}
	if err := parent.Remove(name); err != nil {
		return err
	}
	return syncMutatedBootstrapParent(parent, expected, path)
}
