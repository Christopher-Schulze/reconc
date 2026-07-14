// Package atomicfile publishes small files without exposing partial writes.
package atomicfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// WriteIfChanged atomically replaces path with data only when its current
// bytes differ. It returns true only when a filesystem write was published.
func WriteIfChanged(path string, data []byte, mode os.FileMode) (bool, error) {
	current, err := os.ReadFile(path)
	if err == nil && bytes.Equal(current, data) {
		return false, nil
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
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
	}
	if err := tmpFile.Chmod(mode); err != nil {
		cleanup()
		return false, fmt.Errorf("chmod temp for %s: %w", path, err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		cleanup()
		return false, fmt.Errorf("write temp for %s: %w", path, err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := replaceFile(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("publish %s: %w", path, err)
	}
	return true, nil
}
