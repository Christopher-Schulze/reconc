package compiler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/filelock"
)

// CompileLockRelativePath is where the advisory compile lock lives.
// Co-located with the lockfile so it inherits the same parent dir
// permissions and gets cleaned up by `git clean` heuristics alongside
// the lockfile itself.
const CompileLockRelativePath = ".reconc/.compile.lock"

// AcquireCompileLock takes an advisory file-based lock on
// `<repoRoot>/.reconc/.compile.lock`. The returned release() function
// MUST be called (typically via defer) to unlock and close the file. If
// the lock is already held by another process the call returns an
// error immediately. The OS releases the lock after a process crash;
// the durable lock file itself is harmless and can be reused.
//
// The lock is purely advisory: Reconc policy refresh honours it, but nothing
// prevents an external process from writing to the lockfile directly.
// The goal is to prevent two refresh invocations from racing on the same repo
// (e.g. CI running simultaneously with a developer's local refresh) and
// producing a torn lockfile.
func AcquireCompileLock(repoRoot string) (release func() error, err error) {
	lockDir := filepath.Join(repoRoot, ".reconc")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	lockPath := filepath.Join(repoRoot, CompileLockRelativePath)
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open compile lock: %w", err)
	}
	unlock, err := filelock.TryLock(file)
	if err != nil {
		closeErr := file.Close()
		return nil, errors.Join(fmt.Errorf("another reconc refresh is in progress (lock: %s): %w", lockPath, err), closeErr)
	}
	return func() error {
		return errors.Join(unlock(), file.Close())
	}, nil
}
