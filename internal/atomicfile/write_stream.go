package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"os"
)

const streamCopyBufferBytes = 128 << 10

// WriteStream atomically replaces path with at most maxBytes read from source.
// It never retains the complete payload in memory.
func WriteStream(path string, source io.Reader, maxBytes int64, mode os.FileMode) (err error) {
	if source == nil {
		return errors.New("atomic stream source is required")
	}
	if maxBytes <= 0 {
		return errors.New("atomic stream byte limit must be positive")
	}
	parent, name, err := bindParent(path, PublicParentMode)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.close()) }()
	directory := parent.directory()
	current, currentInfo, err := openCurrent(directory, name, path)
	if err != nil {
		return err
	}
	if current != nil {
		if err := current.Close(); err != nil {
			return fmt.Errorf("close current %s: %w", path, err)
		}
	}
	temporaryFile, temporary, err := createTemporary(directory, name)
	if err != nil {
		return fmt.Errorf("create stream temporary for %s: %w", path, err)
	}
	closed := false
	cleanup := func(primary error) error {
		var closeErr error
		if !closed {
			closeErr = temporaryFile.Close()
			closed = true
		}
		removeErr := directory.Remove(temporary)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		return errors.Join(primary, closeErr, removeErr)
	}
	if err := temporaryFile.Chmod(mode); err != nil {
		return cleanup(fmt.Errorf("chmod stream temporary for %s: %w", path, err))
	}
	written, err := io.CopyBuffer(
		temporaryFile,
		io.LimitReader(source, maxBytes+1),
		make([]byte, streamCopyBufferBytes),
	)
	if err != nil {
		return cleanup(fmt.Errorf("stream temporary for %s: %w", path, err))
	}
	if written > maxBytes {
		return cleanup(fmt.Errorf("stream for %s exceeds %d bytes", path, maxBytes))
	}
	if err := temporaryFile.Sync(); err != nil {
		return cleanup(fmt.Errorf("sync stream temporary for %s: %w", path, err))
	}
	if err := temporaryFile.Close(); err != nil {
		closed = true
		return errors.Join(fmt.Errorf("close stream temporary for %s: %w", path, err), directory.Remove(temporary))
	}
	closed = true
	if err := parent.validate(); err != nil {
		return cleanup(err)
	}
	if err := validateCurrent(directory, name, currentInfo); err != nil {
		return cleanup(fmt.Errorf("validate stream publication target %s: %w", path, err))
	}
	if err := replaceTemporary(directory, temporary, name); err != nil {
		return fmt.Errorf("publish stream %s: %w", path, err)
	}
	if err := parent.validate(); err != nil {
		return fmt.Errorf("validate parent after publishing %s: %w", path, err)
	}
	if err := syncParentDir(directory); err != nil {
		return fmt.Errorf("sync parent for %s: %w", path, err)
	}
	return parent.validate()
}
