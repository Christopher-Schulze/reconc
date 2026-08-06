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

// ReadFile reads at most maxBytes from a regular file and reports a stable
// error instead of blocking on a special file or allocating oversized input.
// Final symlinks are followed intentionally; strict callers use
// ReadRegularFile.
func ReadFile(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("bounded file read requires a positive byte limit")
	}
	if maxBytes > math.MaxInt64-1 {
		return nil, errors.New("bounded file read byte limit is too large")
	}
	before, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must resolve to a regular file", path)
	}
	if before.Size() > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	after, pathStatErr := os.Stat(path)
	if statErr != nil || pathStatErr != nil || !opened.Mode().IsRegular() ||
		opened.Size() > maxBytes || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		if statErr == nil && pathStatErr == nil {
			statErr = fmt.Errorf("%s changed identity or exceeded %d bytes while opening", path, maxBytes)
		}
		return nil, errors.Join(statErr, pathStatErr, file.Close())
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	return body, nil
}

// OpenRegularFile opens a non-symlink regular file whose declared size fits
// maxBytes. Identity is checked before and after open so a path swap cannot
// turn the strict read into a symlink or different-file read.
func OpenRegularFile(path string, maxBytes int64) (*os.File, error) {
	if maxBytes <= 0 {
		return nil, errors.New("bounded regular-file open requires a positive byte limit")
	}
	if maxBytes > math.MaxInt64-1 {
		return nil, errors.New("bounded regular-file open byte limit is too large")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a non-symlink regular file", path)
	}
	if before.Size() > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	after, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil {
		return nil, errors.Join(statErr, lstatErr, file.Close())
	}
	if !opened.Mode().IsRegular() || opened.Size() > maxBytes ||
		!os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return nil, errors.Join(fmt.Errorf("%s changed identity or exceeded %d bytes while opening", path, maxBytes), file.Close())
	}
	return file, nil
}

// ReadRegularFile reads one non-symlink regular file within maxBytes. The
// limit reader catches growth after the size and identity checks.
func ReadRegularFile(path string, maxBytes int64) ([]byte, error) {
	file, err := OpenRegularFile(path, maxBytes)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	return body, nil
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
	if rejectSymlink {
		after, afterErr := os.Lstat(path)
		if afterErr != nil || !os.SameFile(opened, after) {
			if afterErr == nil {
				afterErr = fmt.Errorf("%s changed identity during directory read", path)
			}
			return nil, errors.Join(afterErr, directory.Close())
		}
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
