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
	repository, err := os.OpenRoot(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("open compile repository root: %w", err)
	}
	closeRepository := func(cause error) error {
		return errors.Join(cause, repository.Close())
	}
	if err := repository.Mkdir(".reconc", 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, closeRepository(fmt.Errorf("create compile lock directory: %w", err))
	}
	directoryInfo, err := repository.Lstat(".reconc")
	if err != nil || directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return nil, closeRepository(errors.Join(
			fmt.Errorf("compile lock parent must be a non-symlink directory"), err,
		))
	}
	directory, err := repository.OpenRoot(".reconc")
	if err != nil {
		return nil, closeRepository(fmt.Errorf("open compile lock directory: %w", err))
	}
	closeRoots := func(cause error) error {
		return errors.Join(cause, directory.Close(), repository.Close())
	}
	openedDirectory, statErr := directory.Stat(".")
	currentDirectory, lstatErr := repository.Lstat(".reconc")
	if statErr != nil || lstatErr != nil || currentDirectory.Mode()&os.ModeSymlink != 0 ||
		!openedDirectory.IsDir() || !currentDirectory.IsDir() ||
		!os.SameFile(directoryInfo, openedDirectory) || !os.SameFile(openedDirectory, currentDirectory) {
		return nil, closeRoots(errors.Join(
			fmt.Errorf("compile lock parent changed identity while opening"), statErr, lstatErr,
		))
	}
	lockName := filepath.Base(CompileLockRelativePath)
	before, err := directory.Lstat(lockName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, closeRoots(fmt.Errorf("inspect compile lock: %w", err))
	}
	if err == nil && (before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular()) {
		return nil, closeRoots(fmt.Errorf("compile lock must be a non-symlink regular file"))
	}
	if errors.Is(err, os.ErrNotExist) {
		before = nil
	}
	file, err := openCompileLockFile(directory, lockName, before)
	if err != nil {
		return nil, closeRoots(err)
	}
	closeAll := func(cause error) error {
		return errors.Join(cause, file.Close(), directory.Close(), repository.Close())
	}
	currentDirectory, err = repository.Lstat(".reconc")
	if err != nil || currentDirectory.Mode()&os.ModeSymlink != 0 ||
		!currentDirectory.IsDir() || !os.SameFile(openedDirectory, currentDirectory) {
		return nil, closeAll(errors.Join(fmt.Errorf("compile lock parent changed identity before locking"), err))
	}
	lockPath := filepath.Join(repoRoot, CompileLockRelativePath)
	unlock, err := filelock.TryLock(file)
	if err != nil {
		return nil, closeAll(fmt.Errorf("another reconc refresh is in progress (lock: %s): %w", lockPath, err))
	}
	return func() error {
		return errors.Join(unlock(), file.Close(), directory.Close(), repository.Close())
	}, nil
}

func openCompileLockFile(directory *os.Root, name string, before os.FileInfo) (*os.File, error) {
	flags := os.O_RDWR
	if before == nil {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := directory.OpenFile(name, flags, 0o600)
	if before == nil && errors.Is(err, os.ErrExist) {
		before, err = directory.Lstat(name)
		if err == nil && (before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular()) {
			return nil, fmt.Errorf("compile lock must be a non-symlink regular file")
		}
		if err == nil {
			file, err = directory.OpenFile(name, os.O_RDWR, 0)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("open compile lock: %w", err)
	}
	opened, statErr := file.Stat()
	current, lstatErr := directory.Lstat(name)
	if statErr != nil || lstatErr != nil || !opened.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(opened, current) || before != nil && !os.SameFile(before, opened) {
		return nil, errors.Join(
			fmt.Errorf("compile lock changed identity while opening"), statErr, lstatErr, file.Close(),
		)
	}
	return file, nil
}
