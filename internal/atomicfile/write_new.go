package atomicfile

import (
	"errors"
	"fmt"
	"os"
)

// WriteNew atomically publishes a new file and refuses every existing target.
// The temporary file is created in the destination directory so the final
// hard-link publication is same-filesystem and cannot expose partial bytes.
func WriteNew(path string, data []byte, mode os.FileMode) error {
	_, err := writeNew(path, data, mode, PublicParentMode)
	return err
}

// WritePrivateNew is WriteNew with private permissions for every parent
// directory that must be created.
func WritePrivateNew(path string, data []byte, mode os.FileMode) error {
	_, err := writeNew(path, data, mode, PrivateParentMode)
	return err
}

func writeNew(path string, data []byte, mode, parentMode os.FileMode) (published bool, err error) {
	parent, name, err := bindParent(path, parentMode)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, parent.close()) }()
	directory := parent.directory()
	if err := validateCurrent(directory, name, nil); err != nil {
		return false, fmt.Errorf("refuse existing target %s: %w", path, err)
	}
	temporary, err := prepareNewFile(directory, name, data, mode)
	if err != nil {
		return false, err
	}
	if err := parent.validate(); err != nil {
		return false, errors.Join(err, directory.Remove(temporary))
	}
	if err := validateCurrent(directory, name, nil); err != nil {
		return false, errors.Join(fmt.Errorf("validate new target %s: %w", path, err), directory.Remove(temporary))
	}
	if err := directory.Link(temporary, name); err != nil {
		publishErr := fmt.Errorf("publish new %s: %w", path, err)
		if errors.Is(err, os.ErrExist) {
			publishErr = errors.Join(ErrCurrentChanged, publishErr)
		}
		return false, errors.Join(publishErr, directory.Remove(temporary))
	}
	if err := syncParentDir(directory); err != nil {
		return true, fmt.Errorf("sync parent after publishing %s: %w", path, err)
	}
	if err := parent.validate(); err != nil {
		return true, fmt.Errorf("validate parent after publishing %s: %w", path, err)
	}
	if err := directory.Remove(temporary); err != nil {
		return true, fmt.Errorf("remove publication temporary for %s: %w", path, err)
	}
	if err := syncParentDir(directory); err != nil {
		return true, fmt.Errorf("sync parent for %s: %w", path, err)
	}
	if err := parent.validate(); err != nil {
		return true, fmt.Errorf("validate parent after publishing %s: %w", path, err)
	}
	if err := parent.validate(); err != nil {
		return true, fmt.Errorf("validate parent after syncing %s: %w", path, err)
	}
	return true, nil
}

func prepareNewFile(directory *os.Root, name string, data []byte, mode os.FileMode) (string, error) {
	file, path, err := createTemporary(directory, name)
	if err != nil {
		return "", fmt.Errorf("create temporary for %s: %w", name, err)
	}
	closed := false
	cleanup := func(primary error) (string, error) {
		var closeErr error
		if !closed {
			closeErr = file.Close()
			closed = true
		}
		return "", errors.Join(primary, closeErr, directory.Remove(path))
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
		closed = true
		return "", errors.Join(fmt.Errorf("close temporary for %s: %w", name, err), directory.Remove(path))
	}
	closed = true
	return path, nil
}
