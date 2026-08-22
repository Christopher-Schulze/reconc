package ingest

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// repositorySourceReader anchors all source reads in one operating-system
// directory handle. os.Root prevents lexical and symlink traversal outside the
// repository while stable metadata checks reject path replacement during a
// bounded read.
type repositorySourceReader struct {
	path string
	root *os.Root
}

func newRepositorySourceReader(rootPath string) (*repositorySourceReader, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	return &repositorySourceReader{path: filepath.Clean(rootPath), root: root}, nil
}

func (r *repositorySourceReader) Close() error {
	if r == nil || r.root == nil {
		return nil
	}
	err := r.root.Close()
	r.root = nil
	return err
}

func (r *repositorySourceReader) Read(relative string) ([]byte, error) {
	if r == nil || r.root == nil {
		return nil, errors.New("repository source root is unavailable")
	}
	name, err := repositorySourceName(relative)
	if err != nil {
		return nil, err
	}
	before, err := r.root.Stat(name)
	if err != nil {
		return nil, repositorySourceOpenError(relative, err)
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("source %s must resolve to a regular file", relative)
	}
	if before.Size() > maxPolicySourceBytes {
		return nil, fmt.Errorf("source %s exceeds %d bytes", relative, maxPolicySourceBytes)
	}
	file, err := r.root.Open(name)
	if err != nil {
		return nil, repositorySourceOpenError(relative, err)
	}
	return readRootedSourceSnapshot(r.root, relative, name, file, before)
}

func repositorySourceName(relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("%s must be repository-relative", relative)
	}
	name := filepath.Clean(filepath.FromSlash(relative))
	if name == "." || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("source %s resolves outside the repository root", relative)
	}
	return name, nil
}

func repositorySourceOpenError(relative string, err error) error {
	if strings.Contains(err.Error(), "path escapes from parent") {
		return fmt.Errorf("source %s resolves outside the repository root", relative)
	}
	return fmt.Errorf("open source %s beneath repository root: %w", relative, err)
}

func readRootedSourceSnapshot(
	root *os.Root,
	relative string,
	name string,
	file *os.File,
	before os.FileInfo,
) ([]byte, error) {
	opened, statErr := file.Stat()
	afterOpen, pathErr := root.Stat(name)
	if statErr != nil || pathErr != nil || !sameSourceInfo(before, opened) || !sameSourceInfo(opened, afterOpen) {
		if statErr == nil && pathErr == nil {
			statErr = fmt.Errorf("source %s changed filesystem identity while opening", relative)
		}
		return nil, errors.Join(statErr, pathErr, file.Close())
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxPolicySourceBytes+1))
	afterFile, statErr := file.Stat()
	afterPath, pathErr := root.Stat(name)
	closeErr := file.Close()
	if err := errors.Join(readErr, statErr, pathErr, closeErr); err != nil {
		return nil, err
	}
	if afterFile == nil || afterPath == nil {
		return nil, fmt.Errorf("source %s returned incomplete filesystem metadata", relative)
	}
	if int64(len(body)) > maxPolicySourceBytes {
		return nil, fmt.Errorf("source %s exceeds %d bytes", relative, maxPolicySourceBytes)
	}
	if !sameSourceInfo(before, afterFile) || !sameSourceInfo(afterFile, afterPath) || int64(len(body)) != afterFile.Size() {
		return nil, fmt.Errorf("source %s changed filesystem identity while being read", relative)
	}
	return body, nil
}
