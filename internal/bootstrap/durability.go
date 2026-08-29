package bootstrap

import (
	"errors"
	"os"

	"reconc.dev/reconc/internal/atomicfile"
)

var bootstrapDirectorySync = atomicfile.SyncDirectory

func validateBoundBootstrapParent(parent *os.Root, expected os.FileInfo) error {
	if parent == nil || expected == nil {
		return errors.New("bootstrap parent identity is unavailable")
	}
	current, err := parent.Stat(".")
	if err != nil {
		return err
	}
	if !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(expected, current) {
		return errors.New("bootstrap parent was externally replaced")
	}
	return nil
}

func syncBoundBootstrapParent(parent *os.Root, expected os.FileInfo) error {
	return errors.Join(bootstrapDirectorySync(parent), validateBoundBootstrapParent(parent, expected))
}

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
