package atomicfile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

const streamCopyBufferBytes = 128 << 10

// ExpectedStream identifies the existing regular file that a streamed
// publication is allowed to replace. Digest validation keeps the opened
// identity and the authorized bytes from being observed in separate
// generations.
type ExpectedStream struct {
	Info   os.FileInfo
	Digest string
	Exists bool
}

// WriteStream atomically replaces path with at most maxBytes read from source.
// It never retains the complete payload in memory.
func WriteStream(path string, source io.Reader, maxBytes int64, mode os.FileMode) (result PublicationResult, err error) {
	return writeStream(path, source, maxBytes, mode, nil)
}

// WriteStreamIfCurrent atomically replaces path only while an existing target
// still has the exact identity, metadata, size, and digest in expected. A
// missing expectation is create-only. It never retains the streamed payload in
// memory.
func WriteStreamIfCurrent(path string, source io.Reader, maxBytes int64, mode os.FileMode, expected ExpectedStream) (result PublicationResult, err error) {
	if expected.Exists && (expected.Info == nil || expected.Digest == "") {
		return PublicationResult{}, errors.New("existing stream expectation is incomplete")
	}
	if !expected.Exists && (expected.Info != nil || expected.Digest != "") {
		return PublicationResult{}, errors.New("missing stream expectation contains existing-file state")
	}
	return writeStream(path, source, maxBytes, mode, &expected)
}

func writeStream(path string, source io.Reader, maxBytes int64, mode os.FileMode, expected *ExpectedStream) (result PublicationResult, err error) {
	if source == nil {
		return PublicationResult{}, errors.New("atomic stream source is required")
	}
	if maxBytes <= 0 {
		return PublicationResult{}, errors.New("atomic stream byte limit must be positive")
	}
	parent, name, err := bindParent(path, PublicParentMode)
	if err != nil {
		return PublicationResult{}, err
	}
	defer func() { err = errors.Join(err, result.markUncertainOnClose(parent.close())) }()
	directory := parent.directory()
	current, currentInfo, err := openCurrent(directory, name, path)
	if err != nil {
		return PublicationResult{}, err
	}
	if expected != nil {
		if expected.Exists {
			if current == nil {
				return PublicationResult{}, fmt.Errorf("%w: %s is now missing", ErrCurrentChanged, path)
			}
			if err := verifyExpectedStreamFile(directory, name, path, current, currentInfo, *expected); err != nil {
				return PublicationResult{}, errors.Join(err, current.Close())
			}
		} else if current != nil {
			return PublicationResult{}, errors.Join(fmt.Errorf("%w: %s was created", ErrCurrentChanged, path), current.Close())
		}
	}
	if current != nil {
		if err := current.Close(); err != nil {
			return PublicationResult{}, fmt.Errorf("close current %s: %w", path, err)
		}
	}
	temporaryFile, temporary, err := createTemporary(directory, name)
	if err != nil {
		return PublicationResult{}, fmt.Errorf("create stream temporary for %s: %w", path, err)
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
		return PublicationResult{}, cleanup(fmt.Errorf("chmod stream temporary for %s: %w", path, err))
	}
	written, err := io.CopyBuffer(
		temporaryFile,
		io.LimitReader(source, maxBytes+1),
		make([]byte, streamCopyBufferBytes),
	)
	if err != nil {
		return PublicationResult{}, cleanup(fmt.Errorf("stream temporary for %s: %w", path, err))
	}
	if written > maxBytes {
		return PublicationResult{}, cleanup(fmt.Errorf("stream for %s exceeds %d bytes", path, maxBytes))
	}
	if err := temporaryFile.Sync(); err != nil {
		return PublicationResult{}, cleanup(fmt.Errorf("sync stream temporary for %s: %w", path, err))
	}
	if err := temporaryFile.Close(); err != nil {
		closed = true
		return PublicationResult{}, errors.Join(fmt.Errorf("close stream temporary for %s: %w", path, err), directory.Remove(temporary))
	}
	closed = true
	if err := parent.validate(); err != nil {
		return PublicationResult{}, cleanup(err)
	}
	if expected != nil && expected.Exists {
		if err := verifyExpectedStream(directory, name, path, *expected); err != nil {
			return PublicationResult{}, cleanup(fmt.Errorf("validate stream publication target %s: %w", path, err))
		}
	} else if err := validateCurrent(directory, name, currentInfo); err != nil {
		return PublicationResult{}, cleanup(fmt.Errorf("validate stream publication target %s: %w", path, err))
	}
	if err := replaceTemporary(directory, temporary, name); err != nil {
		return PublicationResult{}, fmt.Errorf("publish stream %s: %w", path, err)
	}
	result.markPublished()
	if err := parent.validate(); err != nil {
		return result, fmt.Errorf("validate parent after publishing %s: %w", path, err)
	}
	if err := syncParentDir(directory); err != nil {
		return result, fmt.Errorf("sync parent for %s: %w", path, err)
	}
	if err := parent.validate(); err != nil {
		return result, err
	}
	result.markDurable()
	return result, nil
}

func verifyExpectedStream(directory *os.Root, name, path string, expected ExpectedStream) (resultErr error) {
	current, info, err := openCurrent(directory, name, path)
	if err != nil {
		return err
	}
	if current == nil {
		return fmt.Errorf("%w: %s is now missing", ErrCurrentChanged, path)
	}
	defer func() { resultErr = errors.Join(resultErr, current.Close()) }()
	return verifyExpectedStreamFile(directory, name, path, current, info, expected)
}

func verifyExpectedStreamFile(directory *os.Root, name, path string, file *os.File, info os.FileInfo, expected ExpectedStream) error {
	if !sameRegularIdentity(expected.Info, info) || expected.Info.Mode() != info.Mode() ||
		expected.Info.Size() != info.Size() || !expected.Info.ModTime().Equal(info.ModTime()) {
		return fmt.Errorf("%w: %s metadata differs", ErrCurrentChanged, path)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek current %s: %w", path, err)
	}
	hash := sha256.New()
	written, err := io.CopyBuffer(hash, file, make([]byte, streamCopyBufferBytes))
	if err != nil {
		return fmt.Errorf("hash current %s: %w", path, err)
	}
	if written != info.Size() || hex.EncodeToString(hash.Sum(nil)) != expected.Digest {
		return fmt.Errorf("%w: %s bytes differ", ErrCurrentChanged, path)
	}
	afterFile, statErr := file.Stat()
	afterPath, pathStatErr := directory.Lstat(name)
	if statErr != nil || pathStatErr != nil || !sameRegularIdentity(info, afterFile) ||
		!sameRegularIdentity(afterFile, afterPath) || info.Mode() != afterFile.Mode() ||
		info.Size() != afterFile.Size() || !info.ModTime().Equal(afterFile.ModTime()) {
		return errors.Join(fmt.Errorf("%w: %s changed while reading", ErrCurrentChanged, path), statErr, pathStatErr)
	}
	return nil
}
