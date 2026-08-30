package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/atomicfile"
)

var bootstrapDirectorySync = atomicfile.SyncDirectory
var beforeBoundRemovalSync = func(*os.Root, string) error { return nil }
var beforeExactBootstrapStageRemoval = func(string) error { return nil }

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

func syncMutatedRemovalParent(parent *os.Root, expected os.FileInfo, path string) error {
	var hookErr error
	if beforeBoundRemovalSync != nil {
		hookErr = beforeBoundRemovalSync(parent, path)
	}
	_, lstatErr := parent.Lstat(filepath.Base(path))
	if lstatErr == nil {
		lstatErr = fmt.Errorf("removed bootstrap target reappeared before parent sync: %s", path)
	} else if errors.Is(lstatErr, os.ErrNotExist) {
		lstatErr = nil
	}
	return errors.Join(hookErr, lstatErr, syncMutatedBootstrapParent(parent, expected, path))
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
	return syncMutatedRemovalParent(parent, expected, path)
}

func removeExactBootstrapStage(
	parent *os.Root,
	parentInfo os.FileInfo,
	name string,
	path string,
	expected os.FileInfo,
) error {
	if err := validateCreatedParent(parent, parentInfo, path); err != nil {
		return err
	}
	current, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	if !sameCreatedFile(expected, current) {
		return errors.New("bootstrap staging file changed identity before removal")
	}
	if beforeExactBootstrapStageRemoval != nil {
		if err := beforeExactBootstrapStageRemoval(path); err != nil {
			return err
		}
	}
	if err := validateCreatedParent(parent, parentInfo, path); err != nil {
		return err
	}
	current, err = parent.Lstat(name)
	if err != nil {
		return err
	}
	if !sameCreatedFile(expected, current) {
		return errors.New("bootstrap staging file changed identity during removal")
	}
	if err := parent.Remove(name); err != nil {
		return err
	}
	return syncMutatedRemovalParent(parent, parentInfo, path)
}
