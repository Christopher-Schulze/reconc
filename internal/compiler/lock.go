package compiler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CompileLockRelativePath is where the advisory compile lock lives.
// Co-located with the lockfile so it inherits the same parent dir
// permissions and gets cleaned up by `git clean` heuristics alongside
// the lockfile itself.
const CompileLockRelativePath = ".reconc/.compile.lock"

// StaleCompileLockAfter is how long a compile-lock file may persist
// before being considered stale (e.g. the owning process crashed
// without releasing). 60 seconds is an order of magnitude longer than
// a typical compile (<1 second) so we don't steal live locks.
const StaleCompileLockAfter = 60 * time.Second

// AcquireCompileLock takes an advisory file-based lock on
// `<repoRoot>/.reconc/.compile.lock`. The returned release() function
// MUST be called (typically via defer) to unlink the lock file. If
// the lock is already held by another process the call returns an
// error immediately; stale locks older than StaleCompileLockAfter are
// reaped and the acquisition retried once.
//
// The lock is purely advisory: reconc compile honours it, but nothing
// prevents an external process from writing to the lockfile directly.
// The goal is to prevent two `reconc compile` invocations from racing
// on the same repo (e.g. CI running simultaneously with a dev's
// local compile) and producing a torn lockfile.
func AcquireCompileLock(repoRoot string) (release func(), err error) {
	lockDir := filepath.Join(repoRoot, ".reconc")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	lockPath := filepath.Join(repoRoot, CompileLockRelativePath)
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			// Stamp the lock with pid + timestamp for debugging. We
			// don't depend on this for correctness -- the file's
			// EXISTENCE is the lock; the content is informational.
			_, _ = fmt.Fprintf(f, "pid=%d acquired=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("acquire compile lock: %w", err)
		}
		// Lock exists. Check if it's stale (owner crashed).
		info, statErr := os.Stat(lockPath)
		if statErr == nil && time.Since(info.ModTime()) > StaleCompileLockAfter {
			_ = os.Remove(lockPath)
			continue // retry once after reaping the stale lock
		}
		return nil, fmt.Errorf("another reconc compile is in progress (lock: %s)", lockPath)
	}
	return nil, fmt.Errorf("compile lock contested after stale-reap; retry manually")
}
