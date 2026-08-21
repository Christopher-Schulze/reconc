// Package boundedio provides exact-size file reads for untrusted or
// repository-controlled inputs.
package boundedio

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
)

// ErrDirectorySnapshotChanged identifies a directory whose metadata changed
// while a bounded snapshot was being read.
var ErrDirectorySnapshotChanged = errors.New("directory snapshot changed")

// ReadFile reads at most maxBytes from a regular file and reports a stable
// error instead of blocking on a special file or allocating oversized input.
// Final symlinks are followed intentionally; strict callers use
// ReadRegularFile.
func ReadFile(path string, maxBytes int64) ([]byte, error) {
	body, _, err := ReadFileSnapshot(path, maxBytes)
	return body, err
}

// ReadFileSnapshot reads a regular file through one bounded open/read window
// and returns the opened file identity alongside the bytes. Final symlinks are
// followed intentionally; callers must validate the resolved target's
// containment separately when repository ownership matters.
func ReadFileSnapshot(path string, maxBytes int64) ([]byte, os.FileInfo, error) {
	if maxBytes <= 0 {
		return nil, nil, errors.New("bounded file read requires a positive byte limit")
	}
	if maxBytes > math.MaxInt64-1 {
		return nil, nil, errors.New("bounded file read byte limit is too large")
	}
	before, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s must resolve to a regular file", path)
	}
	if before.Size() > maxBytes {
		return nil, nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	opened, statErr := file.Stat()
	after, pathStatErr := os.Stat(path)
	if statErr != nil || pathStatErr != nil || !opened.Mode().IsRegular() ||
		opened.Size() > maxBytes || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		if statErr == nil && pathStatErr == nil {
			statErr = fmt.Errorf("%s changed identity or exceeded %d bytes while opening", path, maxBytes)
		}
		return nil, nil, errors.Join(statErr, pathStatErr, file.Close())
	}
	return readOpenedFileSnapshot(path, file, opened, maxBytes, os.Stat)
}

func openRegularFile(path string, maxBytes int64) (*os.File, os.FileInfo, error) {
	if maxBytes <= 0 {
		return nil, nil, errors.New("bounded regular-file open requires a positive byte limit")
	}
	if maxBytes > math.MaxInt64-1 {
		return nil, nil, errors.New("bounded regular-file open byte limit is too large")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s must be a non-symlink regular file", path)
	}
	if before.Size() > maxBytes {
		return nil, nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	opened, statErr := file.Stat()
	after, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil {
		return nil, nil, errors.Join(statErr, lstatErr, file.Close())
	}
	if !sameRegularSnapshot(before, opened) || !sameRegularSnapshot(opened, after) || opened.Size() > maxBytes {
		return nil, nil, errors.Join(fmt.Errorf("%s changed identity, metadata, or exceeded %d bytes while opening", path, maxBytes), file.Close())
	}
	return file, opened, nil
}

// WithRegularFileSnapshot exposes a bounded non-symlink regular file to use,
// then rejects identity, mode, size, or modification-time drift before the
// result can be accepted. The callback must not close the file.
func WithRegularFileSnapshot(path string, maxBytes int64, use func(*os.File, os.FileInfo) error) error {
	if use == nil {
		return errors.New("bounded regular-file snapshot requires a callback")
	}
	file, before, err := openRegularFile(path, maxBytes)
	if err != nil {
		return err
	}
	useErr := use(file, before)
	afterFile, statErr := file.Stat()
	afterPath, pathStatErr := os.Lstat(path)
	closeErr := file.Close()
	var stableErr error
	if statErr == nil && pathStatErr == nil &&
		(!sameRegularSnapshot(before, afterFile) || !sameRegularSnapshot(afterFile, afterPath) || afterFile.Size() > maxBytes) {
		stableErr = fmt.Errorf("%s changed while reading", path)
	}
	return errors.Join(useErr, statErr, pathStatErr, closeErr, stableErr)
}

// ReadRegularFileSnapshot reads one non-symlink regular file within maxBytes
// and returns the metadata of the opened identity together with its bytes. The
// limit reader catches growth after the size and identity checks, and the
// callback's post-read validation rejects replacement or metadata drift.
func ReadRegularFileSnapshot(path string, maxBytes int64) ([]byte, os.FileInfo, error) {
	var body []byte
	var opened os.FileInfo
	err := WithRegularFileSnapshot(path, maxBytes, func(file *os.File, openedInfo os.FileInfo) error {
		opened = openedInfo
		var readErr error
		body, readErr = io.ReadAll(io.LimitReader(file, maxBytes+1))
		if readErr != nil {
			return readErr
		}
		if int64(len(body)) > maxBytes {
			return fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
		}
		if int64(len(body)) != openedInfo.Size() {
			return fmt.Errorf("%s changed while reading", path)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return body, opened, nil
}

// ReadRegularFile reads one non-symlink regular file within maxBytes. The
// strict snapshot primitive owns all identity and growth checks.
func ReadRegularFile(path string, maxBytes int64) ([]byte, error) {
	body, _, err := ReadRegularFileSnapshot(path, maxBytes)
	return body, err
}

func sameRegularSnapshot(left, right os.FileInfo) bool {
	return left != nil && right != nil && left.Mode().IsRegular() && right.Mode().IsRegular() &&
		left.Mode()&os.ModeSymlink == 0 && right.Mode()&os.ModeSymlink == 0 &&
		os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime())
}

func readOpenedFileSnapshot(path string, file *os.File, before os.FileInfo, maxBytes int64, pathStat func(string) (os.FileInfo, error)) ([]byte, os.FileInfo, error) {
	body, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	afterFile, statErr := file.Stat()
	afterPath, pathStatErr := pathStat(path)
	closeErr := file.Close()
	if err := errors.Join(readErr, statErr, pathStatErr, closeErr); err != nil {
		return nil, nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	if !afterFile.Mode().IsRegular() || !afterPath.Mode().IsRegular() ||
		!os.SameFile(before, afterFile) || !os.SameFile(afterFile, afterPath) ||
		before.Mode() != afterFile.Mode() || before.Size() != afterFile.Size() ||
		int64(len(body)) != afterFile.Size() || !before.ModTime().Equal(afterFile.ModTime()) {
		return nil, nil, fmt.Errorf("%s changed while reading", path)
	}
	return body, afterFile, nil
}

// ReadDir returns at most maxEntries entries from a directory. Directory
// symlinks are followed intentionally; callers that require repository-owned
// directory identity use ReadDirNoSymlink.
func ReadDir(path string, maxEntries int) ([]os.DirEntry, error) {
	return readDir(path, maxEntries, false)
}

// ReadDirNoSymlink returns a bounded, sorted snapshot of one real directory.
func ReadDirNoSymlink(path string, maxEntries int) ([]os.DirEntry, error) {
	return readDir(path, maxEntries, true)
}

func readDir(path string, maxEntries int, rejectSymlink bool) ([]os.DirEntry, error) {
	if maxEntries <= 0 {
		return nil, errors.New("bounded directory read requires a positive entry limit")
	}
	if maxEntries > math.MaxInt-1 {
		return nil, errors.New("bounded directory read entry limit is too large")
	}
	var before os.FileInfo
	var err error
	if rejectSymlink {
		before, err = os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
			return nil, fmt.Errorf("%s must be a non-symlink directory", path)
		}
	} else {
		before, err = os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !before.IsDir() {
			return nil, fmt.Errorf("%s must resolve to a directory", path)
		}
	}
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, statErr := directory.Stat()
	if statErr != nil || !opened.IsDir() || rejectSymlink && !os.SameFile(before, opened) {
		if statErr == nil {
			statErr = fmt.Errorf("%s changed identity or is not a directory", path)
		}
		return nil, errors.Join(statErr, directory.Close())
	}
	entries, readErr := directory.ReadDir(maxEntries + 1)
	var after os.FileInfo
	var afterErr error
	if rejectSymlink {
		after, afterErr = os.Lstat(path)
	} else {
		after, afterErr = os.Stat(path)
	}
	if afterErr != nil || !after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) ||
		before.Mode() != opened.Mode() || before.Size() != opened.Size() ||
		!before.ModTime().Equal(opened.ModTime()) || opened.Mode() != after.Mode() ||
		opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		if afterErr == nil {
			afterErr = fmt.Errorf(
				"%s changed identity or metadata during directory read: %w",
				path, ErrDirectorySnapshotChanged,
			)
		}
		return nil, errors.Join(afterErr, directory.Close())
	}
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, errors.Join(readErr, closeErr)
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(entries) > maxEntries {
		return nil, fmt.Errorf("%s exceeds %d directory entries", path, maxEntries)
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
	return entries, nil
}
