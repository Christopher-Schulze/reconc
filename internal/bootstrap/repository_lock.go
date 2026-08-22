package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/presets"
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
	if err := ensureRepositoryLockDirectory(lockDirectory); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(root))
	lockPath := filepath.Join(lockDirectory, hex.EncodeToString(digest[:])+".lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
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

func ensureRepositoryLockDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create repository transaction lock directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect repository transaction lock directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("repository transaction lock path is not a real directory: %s", path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure repository transaction lock directory: %w", err)
	}
	return nil
}
