package tasklifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/atomicfile"
)

var syncTransactionDirectory = atomicfile.SyncDirectory

// A linked destination must be durable before removing the source. Directory
// removals and journal cleanup must also reach disk before claiming success.
func syncTransactionParent(path string) error {
	parentPath := filepath.Dir(path)
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return fmt.Errorf("open TASK transaction parent: %w", err)
	}
	before, statErr := parent.Stat(".")
	if statErr != nil {
		return errors.Join(statErr, parent.Close())
	}
	syncErr := syncTransactionDirectory(parent)
	current, pathErr := os.Lstat(parentPath)
	var identityErr error
	if pathErr != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, current) {
		identityErr = errors.Join(errors.New("TASK transaction parent changed during synchronization"), pathErr)
	}
	return errors.Join(syncErr, identityErr, parent.Close())
}
