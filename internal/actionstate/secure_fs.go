package actionstate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/pathidentity"
)

const StateLockTimeout = 2 * time.Second

func ResolveHome(explicit string) (string, error) {
	home := explicit
	if home == "" {
		home = os.Getenv("RECONC_HOME")
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve Reconc home: %w", err)
		}
		home = filepath.Join(userHome, ".reconc")
	}
	resolved, err := pathidentity.ResolveProspective(home)
	if err != nil {
		return "", fmt.Errorf("resolve Reconc home identity: %w", err)
	}
	if !filepath.IsAbs(resolved) {
		return "", fmt.Errorf("reconc home must resolve to an absolute path")
	}
	resolved = filepath.Clean(resolved)
	if filepath.Dir(resolved) == resolved {
		return "", fmt.Errorf("reconc home must not be a filesystem root")
	}
	return resolved, nil
}

func ensurePrivateDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("private directory path must be absolute")
	}
	info, err := os.Lstat(path)
	created := false
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create private directory %s: %w", path, err)
		}
		created = true
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect private directory %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("private path %s must be a non-symlink directory", path)
	}
	if created {
		if err := secureDirectoryMode(path, info.Mode()); err != nil {
			return fmt.Errorf("secure private directory %s: %w", path, err)
		}
	}
	if err := validatePrivateDirectory(path); err != nil {
		return fmt.Errorf("validate private directory %s: %w", path, err)
	}
	return nil
}

func ensurePrivateSubdirectories(base string, names ...string) (string, error) {
	if err := ensurePrivateDirectory(base); err != nil {
		return "", err
	}
	current := base
	for _, name := range names {
		if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
			return "", fmt.Errorf("private directory component is invalid")
		}
		current = filepath.Join(current, name)
		if err := ensurePrivateDirectory(current); err != nil {
			return "", err
		}
	}
	return current, nil
}

func readPrivateRegularFile(path string, maxBytes int64) ([]byte, error) {
	var body []byte
	err := boundedio.WithRegularFileSnapshot(path, maxBytes, func(file *os.File, info os.FileInfo) error {
		if err := validatePrivateFile(file, info); err != nil {
			return err
		}
		var readErr error
		body, readErr = io.ReadAll(io.LimitReader(file, maxBytes+1))
		if readErr != nil {
			return readErr
		}
		if int64(len(body)) > maxBytes {
			return fmt.Errorf("private file exceeds %d bytes", maxBytes)
		}
		if int64(len(body)) != info.Size() {
			return fmt.Errorf("private file changed while reading")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return body, nil
}

type heldLock struct {
	file   *os.File
	unlock func() error
}

func acquireFileLock(ctx context.Context, path string, timeout time.Duration) (*heldLock, error) {
	return acquirePrivateFileLock(ctx, path, timeout, false)
}

func acquireSharedFileLock(ctx context.Context, path string, timeout time.Duration) (*heldLock, error) {
	return acquirePrivateFileLock(ctx, path, timeout, true)
}

func acquireExistingSharedFileLock(ctx context.Context, path string, timeout time.Duration) (*heldLock, error) {
	if ctx == nil {
		return nil, fmt.Errorf("lock context is required")
	}
	if timeout <= 0 || timeout > StateLockTimeout {
		return nil, fmt.Errorf("lock timeout must be between zero and %s", StateLockTimeout)
	}
	file, err := openExistingPrivateLockFile(path)
	if err != nil {
		return nil, err
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		unlock, lockErr := filelock.TryRLock(file)
		if lockErr == nil {
			return &heldLock{file: file, unlock: unlock}, nil
		}
		if !filelock.IsContended(lockErr) {
			return nil, errors.Join(fmt.Errorf("acquire state lock: %w", lockErr), file.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(fmt.Errorf("acquire state lock: %w", ctx.Err()), file.Close())
		case <-deadline.C:
			return nil, errors.Join(fmt.Errorf("state lock timed out after %s", timeout), file.Close())
		case <-ticker.C:
		}
	}
}

func acquirePrivateFileLock(ctx context.Context, path string, timeout time.Duration, shared bool) (*heldLock, error) {
	if ctx == nil {
		return nil, fmt.Errorf("lock context is required")
	}
	if timeout <= 0 || timeout > StateLockTimeout {
		return nil, fmt.Errorf("lock timeout must be between zero and %s", StateLockTimeout)
	}
	file, err := openPrivateLockFile(path)
	if err != nil {
		return nil, err
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var unlock func() error
		var lockErr error
		if shared {
			unlock, lockErr = filelock.TryRLock(file)
		} else {
			unlock, lockErr = filelock.TryLock(file)
		}
		if lockErr == nil {
			return &heldLock{file: file, unlock: unlock}, nil
		}
		if !filelock.IsContended(lockErr) {
			return nil, errors.Join(fmt.Errorf("acquire state lock: %w", lockErr), file.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(fmt.Errorf("acquire state lock: %w", ctx.Err()), file.Close())
		case <-deadline.C:
			return nil, errors.Join(fmt.Errorf("state lock timed out after %s", timeout), file.Close())
		case <-ticker.C:
		}
	}
}

func openPrivateLockFile(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("lock path %s must be a non-symlink regular file", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect lock path %s: %w", path, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open private lock %s: %w", path, err)
	}
	opened, statErr := file.Stat()
	current, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || !opened.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(opened, current) {
		if statErr == nil && lstatErr == nil {
			statErr = fmt.Errorf("lock path changed identity while opening")
		}
		return nil, errors.Join(statErr, lstatErr, file.Close())
	}
	if err := securePrivateFileMode(path, opened.Mode()); err != nil {
		return nil, errors.Join(fmt.Errorf("secure private lock %s: %w", path, err), file.Close())
	}
	secured, err := file.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("inspect secured private lock %s: %w", path, err), file.Close())
	}
	if err := validatePrivateFile(file, secured); err != nil {
		return nil, errors.Join(fmt.Errorf("validate private lock %s: %w", path, err), file.Close())
	}
	return file, nil
}

func openExistingPrivateLockFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect existing lock path %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("lock path %s must be a non-symlink regular file", path)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open existing private lock %s: %w", path, err)
	}
	opened, statErr := file.Stat()
	current, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || !opened.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(opened, current) {
		if statErr == nil && lstatErr == nil {
			statErr = fmt.Errorf("lock path changed identity while opening")
		}
		return nil, errors.Join(statErr, lstatErr, file.Close())
	}
	if err := validatePrivateFile(file, opened); err != nil {
		return nil, errors.Join(fmt.Errorf("validate existing private lock %s: %w", path, err), file.Close())
	}
	return file, nil
}

func securePublishedPrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("private publication target must be a non-symlink regular file")
	}
	if err := securePrivateFileMode(path, info.Mode()); err != nil {
		return err
	}
	return boundedio.WithRegularFileSnapshot(path, MaxStateTransaction, func(file *os.File, opened os.FileInfo) error {
		return validatePrivateFile(file, opened)
	})
}

func (l *heldLock) close() error {
	if l == nil {
		return nil
	}
	return errors.Join(l.unlock(), l.file.Close())
}
