package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"

	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/presets"
	"reconc.dev/reconc/internal/privatefs"
)

// WithRepositoryTransaction canonicalizes repoRoot and serializes one external
// repository mutation with init, sync, recovery, removal, and acceptance.
func WithRepositoryTransaction(repoRoot string, operation func(root string) error) error {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	if operation == nil {
		return fmt.Errorf("repository transaction operation is nil")
	}
	return withRepositoryTransactionLock(root, func() error { return operation(root) })
}

func withRepositoryTransactionLock(root string, operation func() error) (resultErr error) {
	home, err := presets.ResolveHome()
	if err != nil {
		return err
	}
	lockDirectory := filepath.Join(home, "locks", "repositories")
	if err := privatefs.RepairDirectory(lockDirectory); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(root))
	lockPath := filepath.Join(lockDirectory, hex.EncodeToString(digest[:])+".lock")
	lockFile, err := privatefs.OpenLock(lockPath)
	if err != nil {
		return fmt.Errorf("open repository transaction lock: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, lockFile.Close())
	}()
	unlock, err := filelock.TryLock(lockFile)
	if err != nil {
		return fmt.Errorf("repository transaction is already active for %s: %w", root, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, unlock())
	}()
	return operation()
}
