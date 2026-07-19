// Package atomicfile publishes small files without exposing partial writes.
package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteIfChanged atomically replaces path with data only when its current
// bytes differ. It returns true only when a filesystem write was published.
func WriteIfChanged(path string, data []byte, mode os.FileMode) (bool, error) {
	info, lstatErr := os.Lstat(path)
	if lstatErr == nil && !info.Mode().IsRegular() {
		return false, fmt.Errorf("current %s is not a regular file", path)
	}
	if lstatErr != nil && !os.IsNotExist(lstatErr) {
		return false, fmt.Errorf("inspect current %s: %w", path, lstatErr)
	}
	current, err := os.ReadFile(path)
	if err == nil && bytes.Equal(current, data) {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return false, fmt.Errorf("stat current %s: %w", path, statErr)
		}
		if info.Mode().Perm() == mode.Perm() {
			return false, nil
		}
		if err := os.Chmod(path, mode); err != nil {
			return false, fmt.Errorf("chmod current %s: %w", path, err)
		}
		return true, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read current %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("create parent %s: %w", dir, err)
	}
	tmpFile, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return false, fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmp := tmpFile.Name()
	cleanup := func() error {
		closeErr := tmpFile.Close()
		removeErr := os.Remove(tmp)
		if os.IsNotExist(removeErr) {
			removeErr = nil
		}
		return errors.Join(closeErr, removeErr)
	}
	if err := tmpFile.Chmod(mode); err != nil {
		return false, errors.Join(fmt.Errorf("chmod temp for %s: %w", path, err), cleanup())
	}
	if _, err := tmpFile.Write(data); err != nil {
		return false, errors.Join(fmt.Errorf("write temp for %s: %w", path, err), cleanup())
	}
	if err := tmpFile.Sync(); err != nil {
		return false, errors.Join(fmt.Errorf("sync temp for %s: %w", path, err), cleanup())
	}
	if err := tmpFile.Close(); err != nil {
		removeErr := os.Remove(tmp)
		if os.IsNotExist(removeErr) {
			removeErr = nil
		}
		return false, errors.Join(fmt.Errorf("close temp for %s: %w", path, err), removeErr)
	}
	if err := replaceFile(tmp, path); err != nil {
		removeErr := os.Remove(tmp)
		if os.IsNotExist(removeErr) {
			removeErr = nil
		}
		return false, errors.Join(fmt.Errorf("publish %s: %w", path, err), removeErr)
	}
	if err := syncParentDir(dir); err != nil {
		return true, fmt.Errorf("sync parent for %s: %w", path, err)
	}
	return true, nil
}
