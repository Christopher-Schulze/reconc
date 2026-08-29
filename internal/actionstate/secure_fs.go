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
	"reconc.dev/reconc/internal/privatefs"
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
	return privatefs.SecureDirectory(path)
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
	body, _, err := readPrivateRegularFileSnapshot(path, maxBytes)
	return body, err
}

func readPrivateRegularFileSnapshot(path string, maxBytes int64) ([]byte, os.FileInfo, error) {
	var body []byte
	var identity os.FileInfo
	err := boundedio.WithRegularFileSnapshot(path, maxBytes, func(file *os.File, info os.FileInfo) error {
		if err := validatePrivateFile(file, info); err != nil {
			return err
		}
		identity = info
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
		return nil, nil, err
	}
	return body, identity, nil
}

func validatePrivateRegularFile(path string, maxBytes int64) error {
	return boundedio.WithRegularFileSnapshot(path, maxBytes, func(file *os.File, info os.FileInfo) error {
		return validatePrivateFile(file, info)
	})
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
	return privatefs.OpenLock(path)
}

func openExistingPrivateLockFile(path string) (*os.File, error) {
	return privatefs.OpenExistingLock(path)
}

func securePublishedPrivateFile(path string) error {
	file, err := privatefs.OpenExistingLock(path)
	if err != nil {
		return err
	}
	return file.Close()
}

func (l *heldLock) close() error {
	if l == nil {
		return nil
	}
	return errors.Join(l.unlock(), l.file.Close())
}
