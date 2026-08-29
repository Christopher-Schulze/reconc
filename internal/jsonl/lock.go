package jsonl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"reconc.dev/reconc/internal/filelock"
)

func withLock(path string, fn func() error) error {
	return withLayoutLock(path, defaultLayout(path), fn)
}

func withLayoutLock(path string, layout Layout, fn func() error) error {
	return withLayoutLockContext(context.Background(), path, layout, fn)
}

func withLayoutLockContext(ctx context.Context, path string, layout Layout, fn func() error) error {
	return withLayoutLockModeContext(ctx, path, layout, true, fn)
}

func withLayoutLockModeContext(
	ctx context.Context,
	path string,
	layout Layout,
	create bool,
	fn func() error,
) error {
	if fn == nil {
		return errors.New("jsonl lock callback is required")
	}
	return withLayoutLockLeaseContext(ctx, path, layout, create, func(Layout) error {
		return fn()
	})
}

func withLayoutLockLeaseContext(
	ctx context.Context,
	path string,
	layout Layout,
	create bool,
	fn func(Layout) error,
) error {
	if ctx == nil {
		return errors.New("jsonl lock context is required")
	}
	if fn == nil {
		return errors.New("jsonl lock callback is required")
	}
	if err := validateLayout(path, layout); err != nil {
		return err
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(path), layout.DirectoryMode); err != nil {
			return err
		}
	}
	if err := validateLayoutDirectory(path, layout); err != nil {
		return err
	}
	lock, err := openLayoutLockFile(path, layout, create)
	if err != nil {
		return err
	}
	unlock, err := acquireLayoutLock(ctx, lock, layout.LockTimeout)
	if err != nil {
		return errors.Join(err, lock.Close())
	}
	closeLocked := func(cause error) error {
		return errors.Join(cause, unlock(), lock.Close())
	}
	if err := validateOpenedLayoutLock(path, layout, lock); err != nil {
		return closeLocked(err)
	}
	if err := validateLayoutSecurityFile(layout, layout.LockPath, 4<<10); err != nil {
		return closeLocked(err)
	}
	identity, err := lock.Stat()
	if err != nil {
		return closeLocked(err)
	}
	lease := &layoutLockLease{path: layout.LockPath, file: lock, identity: identity}
	if err := lease.validate(); err != nil {
		return closeLocked(err)
	}
	lockedLayout := layout
	lockedLayout.lockLease = lease
	fnErr := fn(lockedLayout)
	leaseErr := lease.validate()
	unlockErr := unlock()
	closeErr := lock.Close()
	if leaseErr != nil {
		fnErr = errors.Join(fnErr, fmt.Errorf("JSONL lock lease changed: %w", leaseErr))
	}
	if fnErr != nil {
		return errors.Join(fnErr, unlockErr, closeErr)
	}
	if unlockErr != nil {
		return errors.Join(fmt.Errorf("unlock JSONL: %w", unlockErr), closeErr)
	}
	return closeErr
}

type layoutLockLease struct {
	path     string
	file     *os.File
	identity os.FileInfo
}

func (lease *layoutLockLease) validate() error {
	if lease == nil || lease.file == nil || lease.identity == nil {
		return errors.New("JSONL lock lease is unavailable")
	}
	return validateOpenedLayoutLockIdentity(lease.path, lease.file, lease.identity)
}

func (layout Layout) validateLockLease() error {
	if layout.lockLease == nil {
		return nil
	}
	return layout.lockLease.validate()
}

func openLayoutLockFile(path string, layout Layout, create bool) (*os.File, error) {
	before, err := os.Lstat(layout.LockPath)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return nil, err
		}
		lock, createErr := createLayoutLockFile(path, layout)
		if createErr == nil {
			return lock, nil
		}
		if !errors.Is(createErr, os.ErrExist) {
			return nil, createErr
		}
		before, err = os.Lstat(layout.LockPath)
	}
	if err != nil {
		return nil, err
	}
	if err := validateLayoutLockInfo(path, layout, before); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(layout.LockPath, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if err := validateOpenedLayoutLockIdentity(layout.LockPath, lock, before); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	return lock, nil
}

func createLayoutLockFile(path string, layout Layout) (*os.File, error) {
	if layout.Security == nil {
		return createDirectLayoutLockFile(layout)
	}
	return createSecuredLayoutLockFile(path, layout)
}

func createDirectLayoutLockFile(layout Layout) (*os.File, error) {
	lock, err := os.OpenFile(
		layout.LockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, layout.FileMode,
	)
	if err != nil {
		return nil, err
	}
	if err := lock.Chmod(layout.FileMode); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	if err := validateOpenedLayoutLockIdentity(layout.LockPath, lock, nil); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	return lock, nil
}

func createSecuredLayoutLockFile(path string, layout Layout) (*os.File, error) {
	candidate, err := os.CreateTemp(filepath.Dir(layout.LockPath), ".reconc-jsonl-lock-*")
	if err != nil {
		return nil, err
	}
	candidatePath := candidate.Name()
	closeCandidate := func(cause error) error {
		return errors.Join(cause, candidate.Close(), os.Remove(candidatePath))
	}
	if err := secureLayoutSecurityFile(layout, candidatePath, 4<<10); err != nil {
		return nil, closeCandidate(err)
	}
	if err := validateOpenedLayoutLockIdentity(candidatePath, candidate, nil); err != nil {
		return nil, closeCandidate(err)
	}
	if err := candidate.Close(); err != nil {
		return nil, errors.Join(err, os.Remove(candidatePath))
	}
	if err := os.Link(candidatePath, layout.LockPath); err != nil {
		return nil, errors.Join(fmt.Errorf("atomically publish secured JSONL lock: %w", err), os.Remove(candidatePath))
	}
	if err := os.Remove(candidatePath); err != nil {
		return nil, err
	}
	before, err := os.Lstat(layout.LockPath)
	if err != nil {
		return nil, err
	}
	if err := validateLayoutLockInfo(path, layout, before); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(layout.LockPath, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if err := validateOpenedLayoutLockIdentity(layout.LockPath, lock, before); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	if err := validateLayoutSecurityFile(layout, layout.LockPath, 4<<10); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	return lock, nil
}

func validateOpenedLayoutLock(path string, layout Layout, lock *os.File) error {
	if err := validateOpenedLayoutLockIdentity(layout.LockPath, lock, nil); err != nil {
		return err
	}
	opened, err := lock.Stat()
	if err != nil {
		return err
	}
	return validateLayoutLockInfo(path, layout, opened)
}

func validateOpenedLayoutLockIdentity(lockPath string, lock *os.File, before os.FileInfo) error {
	opened, statErr := lock.Stat()
	current, lstatErr := os.Lstat(lockPath)
	if statErr != nil || lstatErr != nil || !opened.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(opened, current) || before != nil && !os.SameFile(before, opened) {
		if statErr == nil && lstatErr == nil {
			statErr = fmt.Errorf("jsonl lock path changed identity while opening")
		}
		return errors.Join(statErr, lstatErr)
	}
	return nil
}

func validateLayoutLockInfo(path string, layout Layout, info os.FileInfo) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("jsonl lock path must be a non-symlink regular file: %s", layout.LockPath)
	}
	if !layoutIsDefault(path, layout) && runtime.GOOS != "windows" &&
		info.Mode().Perm() != layout.FileMode.Perm() {
		return fmt.Errorf("jsonl lock path has mode %o; want %o", info.Mode().Perm(), layout.FileMode.Perm())
	}
	return nil
}

func validateLayoutDirectory(path string, layout Layout) error {
	directory := filepath.Dir(path)
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("jsonl parent must be a non-symlink directory: %s", directory)
	}
	if !layoutIsDefault(path, layout) && runtime.GOOS != "windows" &&
		info.Mode().Perm() != layout.DirectoryMode.Perm() {
		return fmt.Errorf(
			"jsonl parent has mode %o; want %o", info.Mode().Perm(), layout.DirectoryMode.Perm(),
		)
	}
	if layout.Security != nil {
		if err := layout.Security.ValidateJSONLDirectory(directory); err != nil {
			return fmt.Errorf("validate JSONL directory security: %w", err)
		}
	}
	return nil
}

func acquireLayoutLock(ctx context.Context, file *os.File, timeout time.Duration) (func() error, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var deadline <-chan time.Time
	var timer *time.Timer
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		deadline = timer.C
		defer timer.Stop()
	}
	for {
		unlock, err := filelock.TryLock(file)
		if err == nil {
			return unlock, nil
		}
		if !filelock.IsContended(err) {
			return nil, fmt.Errorf("acquire JSONL lock: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("acquire JSONL lock: %w", ctx.Err())
		case <-deadline:
			return nil, fmt.Errorf("acquire JSONL lock timed out after %s", timeout)
		case <-ticker.C:
		}
	}
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(data) {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}
