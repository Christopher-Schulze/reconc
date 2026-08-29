package jsonl

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
)

const (
	// Secured lock publication may leave a losing candidate in the parent
	// directory briefly after another writer has acquired the final lock.
	// Keep archive discovery bounded while waiting for that transient churn to
	// settle, then preserve the strict snapshot error.
	archiveDirectoryReadTries  = 400
	archiveDirectoryRetryDelay = 5 * time.Millisecond
)

func trimTailWithLayout(path string, maxBytes int64, layout Layout) (int64, error) {
	original, kept, data, mode, err := tailDataWithLayout(path, maxBytes, layout)
	if err != nil || original == kept {
		return 0, err
	}
	result, err := atomicfile.WriteIfChanged(path, data, mode)
	if err != nil {
		return 0, err
	}
	if result.Changed {
		if err := secureLayoutSecurityFile(layout, path, maxBytes); err != nil {
			return 0, err
		}
	}
	return original - kept, nil
}

func tailData(path string, maxBytes int64) (int64, int64, []byte, os.FileMode, error) {
	return tailDataWithLayout(path, maxBytes, defaultLayout(path))
}

func tailDataWithLayout(path string, maxBytes int64, layout Layout) (int64, int64, []byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil, 0, nil
	}
	if err != nil {
		return 0, 0, nil, 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return 0, 0, nil, 0, fmt.Errorf("JSONL path must be a non-symlink regular file: %s", path)
	}
	if info.Size() <= maxBytes {
		if err := validateLayoutSecurityFile(layout, path, maxBytes); err != nil {
			return 0, 0, nil, 0, err
		}
		return info.Size(), info.Size(), nil, info.Mode().Perm(), nil
	}
	var data []byte
	err = withValidatedLayoutSecurityFileLimits(layout, path, info.Size(), maxBytes, func(file *os.File, opened os.FileInfo) error {
		start := opened.Size() - maxBytes
		if _, seekErr := file.Seek(start, 0); seekErr != nil {
			return seekErr
		}
		var readErr error
		data, readErr = io.ReadAll(io.LimitReader(file, maxBytes))
		return readErr
	})
	if err != nil {
		return 0, 0, nil, 0, err
	}
	if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
		data = data[newline+1:]
	} else {
		data = nil
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		if newline := bytes.LastIndexByte(data, '\n'); newline >= 0 {
			data = data[:newline+1]
		} else {
			data = nil
		}
	}
	return info.Size(), int64(len(data)), data, info.Mode().Perm(), nil
}

type archiveCandidate struct {
	path  string
	index int
}

func archiveCandidates(path string) ([]archiveCandidate, error) {
	directory := filepath.Dir(path)
	entries, err := readArchiveDirectory(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	prefix := filepath.Base(path) + "."
	out := make([]archiveCandidate, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		suffix := strings.TrimPrefix(entry.Name(), prefix)
		index, err := strconv.Atoi(suffix)
		if err == nil && index > 0 {
			out = append(out, archiveCandidate{path: filepath.Join(directory, entry.Name()), index: index})
		}
	}
	return out, nil
}

func readArchiveDirectory(directory string) ([]os.DirEntry, error) {
	return readArchiveDirectoryWith(directory, boundedio.ReadDir)
}

func readArchiveDirectoryWith(
	directory string,
	read func(string, int) ([]os.DirEntry, error),
) ([]os.DirEntry, error) {
	var err error
	for attempt := 0; attempt < archiveDirectoryReadTries; attempt++ {
		var entries []os.DirEntry
		entries, err = read(directory, 4096)
		if err == nil || !errors.Is(err, boundedio.ErrDirectorySnapshotChanged) {
			return entries, err
		}
		if attempt+1 < archiveDirectoryReadTries {
			time.Sleep(archiveDirectoryRetryDelay)
		}
	}
	return nil, err
}
