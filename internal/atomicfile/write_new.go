package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteNew atomically publishes a new file and refuses every existing target.
// The temporary file is created in the destination directory so the final
// hard-link publication is same-filesystem and cannot expose partial bytes.
func WriteNew(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create parent %s: %w", directory, err)
	}
	temporary, err := prepareNewFile(directory, filepath.Base(path), data, mode)
	if err != nil {
		return err
	}
	if err := os.Link(temporary, path); err != nil {
		return errors.Join(fmt.Errorf("publish new %s: %w", path, err), os.Remove(temporary))
	}
	if err := os.Remove(temporary); err != nil {
		return fmt.Errorf("remove publication temporary for %s: %w", path, err)
	}
	if err := syncParentDir(directory); err != nil {
		return fmt.Errorf("sync parent for %s: %w", path, err)
	}
	return nil
}

func prepareNewFile(directory, name string, data []byte, mode os.FileMode) (string, error) {
	file, err := os.CreateTemp(directory, "."+name+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temporary for %s: %w", name, err)
	}
	path := file.Name()
	cleanup := func(primary error) (string, error) {
		return "", errors.Join(primary, file.Close(), os.Remove(path))
	}
	if err := file.Chmod(mode); err != nil {
		return cleanup(fmt.Errorf("chmod temporary for %s: %w", name, err))
	}
	if _, err := file.Write(data); err != nil {
		return cleanup(fmt.Errorf("write temporary for %s: %w", name, err))
	}
	if err := file.Sync(); err != nil {
		return cleanup(fmt.Errorf("sync temporary for %s: %w", name, err))
	}
	if err := file.Close(); err != nil {
		return "", errors.Join(fmt.Errorf("close temporary for %s: %w", name, err), os.Remove(path))
	}
	return path, nil
}
