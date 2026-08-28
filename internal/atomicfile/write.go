// Package atomicfile publishes small files without exposing partial writes.
package atomicfile

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrCurrentChanged reports that a conditional publication target no longer
// matches the exact filesystem state authorized by its caller.
var ErrCurrentChanged = errors.New("publication target changed after preflight")

// ExpectedCurrent is the exact target state authorized before publication.
// Info must describe the same regular-file identity as Data when Exists is
// true. The zero value authorizes create-only publication.
type ExpectedCurrent struct {
	Data   []byte
	Info   os.FileInfo
	Exists bool
}

// WriteIfChanged atomically replaces path with data only when its current
// bytes differ. It returns true only when a filesystem write was published.
func WriteIfChanged(path string, data []byte, mode os.FileMode) (bool, error) {
	return writeIfChanged(path, data, mode, PublicParentMode)
}

// WritePrivateIfChanged is WriteIfChanged with private permissions for every
// parent directory that must be created.
func WritePrivateIfChanged(path string, data []byte, mode os.FileMode) (bool, error) {
	return writeIfChanged(path, data, mode, PrivateParentMode)
}

// WriteIfCurrent publishes data only while path still has the exact bytes,
// identity, mode, size, and modification time authorized in expected. A
// missing expectation is create-only. It returns true only when a filesystem
// write was published.
func WriteIfCurrent(path string, data []byte, mode os.FileMode, expected ExpectedCurrent) (bool, error) {
	if !expected.Exists {
		if expected.Info != nil || len(expected.Data) != 0 {
			return false, errors.New("missing-file expectation contains existing-file state")
		}
		return writeNew(path, data, mode, PublicParentMode)
	}
	if expected.Info == nil {
		return false, errors.New("existing-file expectation is missing identity")
	}
	return writeExistingIfCurrent(path, data, mode, expected)
}

func writeExistingIfCurrent(path string, data []byte, mode os.FileMode, expected ExpectedCurrent) (changed bool, err error) {
	parent, name, err := bindParent(path, PublicParentMode)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, parent.close()) }()
	directory := parent.directory()
	currentFile, currentInfo, err := openExpectedCurrent(directory, name, path, expected)
	if err != nil {
		return false, err
	}
	if bytes.Equal(data, expected.Data) {
		return reconcileExpectedMode(parent, name, path, currentFile, currentInfo, mode, expected)
	}
	temporary, err := prepareNewFile(directory, name, data, mode)
	if err != nil {
		return false, errors.Join(err, currentFile.Close())
	}
	if err := verifyExpectedCurrent(directory, name, path, currentFile, currentInfo, expected); err != nil {
		return false, errors.Join(err, currentFile.Close(), directory.Remove(temporary))
	}
	if err := errors.Join(parent.validate(), validateCurrent(directory, name, currentInfo), currentFile.Close()); err != nil {
		return false, errors.Join(fmt.Errorf("validate conditional target %s: %w", path, err), directory.Remove(temporary))
	}
	return publishConditionalReplacement(parent, name, path, temporary)
}

func openExpectedCurrent(directory *os.Root, name, path string, expected ExpectedCurrent) (*os.File, os.FileInfo, error) {
	file, info, err := openCurrent(directory, name, path)
	if err != nil {
		return nil, nil, err
	}
	if file == nil {
		return nil, nil, fmt.Errorf("%w: %s is now missing", ErrCurrentChanged, path)
	}
	if err := verifyExpectedCurrent(directory, name, path, file, info, expected); err != nil {
		return nil, nil, errors.Join(err, file.Close())
	}
	return file, info, nil
}

func verifyExpectedCurrent(directory *os.Root, name, path string, file *os.File, info os.FileInfo, expected ExpectedCurrent) error {
	if !sameRegularIdentity(expected.Info, info) || expected.Info.Mode() != info.Mode() ||
		expected.Info.Size() != info.Size() || !expected.Info.ModTime().Equal(info.ModTime()) {
		return fmt.Errorf("%w: %s metadata differs", ErrCurrentChanged, path)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek current %s: %w", path, err)
	}
	matched, err := matchesCurrent(directory, name, path, file, info, expected.Data)
	if err != nil {
		return err
	}
	if !matched {
		return fmt.Errorf("%w: %s bytes differ", ErrCurrentChanged, path)
	}
	return nil
}

func reconcileExpectedMode(parent *boundParent, name, path string, file *os.File, info os.FileInfo, mode os.FileMode, expected ExpectedCurrent) (bool, error) {
	directory := parent.directory()
	if err := errors.Join(parent.validate(), verifyExpectedCurrent(directory, name, path, file, info, expected)); err != nil {
		return false, errors.Join(err, file.Close())
	}
	changed, modeErr := reconcileMode(directory, name, file, info.Mode(), mode)
	validationErr := validateCurrent(directory, name, info)
	parentErr := parent.validate()
	closeErr := file.Close()
	if err := errors.Join(modeErr, validationErr, parentErr, closeErr); err != nil {
		return false, fmt.Errorf("reconcile conditional mode for %s: %w", path, err)
	}
	return changed, nil
}

func publishConditionalReplacement(parent *boundParent, name, path, temporary string) (bool, error) {
	directory := parent.directory()
	if err := replaceTemporary(directory, temporary, name); err != nil {
		return false, fmt.Errorf("publish %s: %w", path, err)
	}
	if err := parent.validate(); err != nil {
		return true, fmt.Errorf("validate parent after publishing %s: %w", path, err)
	}
	if err := syncParentDir(directory); err != nil {
		return true, fmt.Errorf("sync parent for %s: %w", path, err)
	}
	if err := parent.validate(); err != nil {
		return true, fmt.Errorf("validate parent after syncing %s: %w", path, err)
	}
	return true, nil
}

// SecureExistingIfMatches verifies an existing regular file against data and
// reconciles its mode without replacing its content. A missing or different
// target returns false; unsafe identities and I/O failures return an error.
func SecureExistingIfMatches(path string, data []byte, mode os.FileMode) (matched bool, err error) {
	parent, name, err := bindParent(path, PublicParentMode)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, parent.close()) }()
	directory := parent.directory()
	currentFile, currentInfo, err := openCurrent(directory, name, path)
	if err != nil || currentFile == nil {
		return false, err
	}
	identical, err := compareCurrent(directory, name, path, currentFile, currentInfo, data)
	if err != nil {
		return false, err
	}
	if !identical {
		return false, errors.Join(parent.validate(), currentFile.Close())
	}
	if err := parent.validate(); err != nil {
		return false, errors.Join(err, currentFile.Close())
	}
	_, modeErr := reconcileMode(directory, name, currentFile, currentInfo.Mode(), mode)
	validationErr := validateCurrent(directory, name, currentInfo)
	parentErr := parent.validate()
	closeErr := currentFile.Close()
	if err := errors.Join(modeErr, validationErr, parentErr, closeErr); err != nil {
		return false, fmt.Errorf("secure matching current %s: %w", path, err)
	}
	return true, nil
}

func writeIfChanged(path string, data []byte, mode, parentMode os.FileMode) (changed bool, err error) {
	parent, name, err := bindParent(path, parentMode)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, parent.close()) }()
	directory := parent.directory()
	currentFile, currentInfo, err := openCurrent(directory, name, path)
	if err != nil {
		return false, err
	}
	if currentFile != nil {
		identical, compareErr := compareCurrent(directory, name, path, currentFile, currentInfo, data)
		if compareErr != nil {
			return false, compareErr
		}
		if identical {
			if err := parent.validate(); err != nil {
				return false, errors.Join(err, currentFile.Close())
			}
			modeChanged, modeErr := reconcileMode(directory, name, currentFile, currentInfo.Mode(), mode)
			validationErr := validateCurrent(directory, name, currentInfo)
			parentErr := parent.validate()
			closeErr := currentFile.Close()
			if err := errors.Join(modeErr, validationErr, parentErr, closeErr); err != nil {
				return false, fmt.Errorf("reconcile mode for current %s: %w", path, err)
			}
			return modeChanged, nil
		}
		if err := currentFile.Close(); err != nil {
			return false, fmt.Errorf("close current %s: %w", path, err)
		}
	}
	tmpFile, tmp, err := createTemporary(directory, name)
	if err != nil {
		return false, fmt.Errorf("create temp for %s: %w", path, err)
	}
	closed := false
	cleanup := func() error {
		var closeErr error
		if !closed {
			closeErr = tmpFile.Close()
			closed = true
		}
		removeErr := directory.Remove(tmp)
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
		closed = true
		removeErr := directory.Remove(tmp)
		if os.IsNotExist(removeErr) {
			removeErr = nil
		}
		return false, errors.Join(fmt.Errorf("close temp for %s: %w", path, err), removeErr)
	}
	closed = true
	if err := parent.validate(); err != nil {
		return false, errors.Join(err, cleanup())
	}
	if err := validateCurrent(directory, name, currentInfo); err != nil {
		return false, errors.Join(fmt.Errorf("validate publication target %s: %w", path, err), cleanup())
	}
	if err := replaceTemporary(directory, tmp, name); err != nil {
		return false, fmt.Errorf("publish %s: %w", path, err)
	}
	if err := parent.validate(); err != nil {
		return true, fmt.Errorf("validate parent after publishing %s: %w", path, err)
	}
	if err := syncParentDir(directory); err != nil {
		return true, fmt.Errorf("sync parent for %s: %w", path, err)
	}
	if err := parent.validate(); err != nil {
		return true, fmt.Errorf("validate parent after syncing %s: %w", path, err)
	}
	return true, nil
}

func replaceTemporary(directory *os.Root, temporary, target string) error {
	if err := replaceFile(directory, temporary, target); err != nil {
		removeErr := directory.Remove(temporary)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		return errors.Join(err, removeErr)
	}
	return nil
}

func openCurrent(directory *os.Root, name, path string) (*os.File, os.FileInfo, error) {
	before, err := directory.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("inspect current %s: %w", path, err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("current %s is not a non-symlink regular file", path)
	}
	file, err := directory.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open current %s: %w", path, err)
	}
	opened, statErr := file.Stat()
	after, lstatErr := directory.Lstat(name)
	if statErr != nil || lstatErr != nil || !sameRegularIdentity(before, opened) || !sameRegularIdentity(opened, after) {
		return nil, nil, errors.Join(
			fmt.Errorf("current %s changed identity while opening", path), statErr, lstatErr, file.Close(),
		)
	}
	return file, opened, nil
}

// compareCurrent transfers descriptor ownership back to the caller only after
// a successful comparison. A failed comparison closes the descriptor here so
// its primary and close errors cannot be separated or forgotten.
func compareCurrent(directory *os.Root, name, path string, file *os.File, info os.FileInfo, data []byte) (bool, error) {
	identical, err := matchesCurrent(directory, name, path, file, info, data)
	if err != nil {
		return false, errors.Join(err, file.Close())
	}
	return identical, nil
}

func matchesCurrent(directory *os.Root, name, path string, file *os.File, info os.FileInfo, data []byte) (bool, error) {
	if info.Size() != int64(len(data)) {
		return false, nil
	}
	current, readErr := io.ReadAll(io.LimitReader(file, int64(len(data))+1))
	after, statErr := file.Stat()
	pathInfo, lstatErr := directory.Lstat(name)
	if readErr != nil || statErr != nil || lstatErr != nil || int64(len(current)) != info.Size() ||
		!sameRegularIdentity(info, after) || !sameRegularIdentity(after, pathInfo) ||
		info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return false, errors.Join(
			fmt.Errorf("current %s changed while reading", path), readErr, statErr, lstatErr,
		)
	}
	return bytes.Equal(current, data), nil
}

func validateCurrent(directory *os.Root, name string, expected os.FileInfo) error {
	current, err := directory.Lstat(name)
	if expected == nil && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if expected == nil || !sameRegularIdentity(expected, current) {
		return ErrCurrentChanged
	}
	return nil
}

func sameRegularIdentity(left, right os.FileInfo) bool {
	return left != nil && right != nil && left.Mode()&os.ModeSymlink == 0 &&
		right.Mode()&os.ModeSymlink == 0 && left.Mode().IsRegular() && right.Mode().IsRegular() &&
		os.SameFile(left, right)
}

func createTemporary(directory *os.Root, target string) (*os.File, string, error) {
	var random [16]byte
	for range 10 {
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", err
		}
		name := "." + target + "." + hex.EncodeToString(random[:]) + ".tmp"
		file, err := directory.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err == nil {
			return file, name, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("temporary filename collision limit reached")
}
