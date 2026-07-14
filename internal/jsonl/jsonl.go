// Package jsonl provides cross-process-safe, bounded JSONL publication.
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

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/filelock"
)

// Policy bounds one live JSONL file plus a fixed archive ring.
type Policy struct {
	MaxBytes    int64
	MaxArchives int
}

// EnforceResult reports bytes removed from existing live/archive files.
type EnforceResult struct {
	BytesFreed   int64
	FilesRemoved int
}

// Inspect reports the cleanup Enforce would perform against the current
// snapshot without creating locks, temp files, or other filesystem state.
func Inspect(path string, policy Policy) (EnforceResult, error) {
	if err := validatePolicy(policy); err != nil {
		return EnforceResult{}, err
	}
	result := EnforceResult{}
	for _, candidate := range archiveCandidates(path) {
		if candidate.index <= policy.MaxArchives {
			continue
		}
		info, err := os.Stat(candidate.path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, err
		}
		if err == nil {
			result.BytesFreed += info.Size()
			result.FilesRemoved++
		}
	}
	for index := policy.MaxArchives; index >= 0; index-- {
		candidate := path
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d", path, index)
		}
		original, kept, _, _, err := tailData(candidate, policy.MaxBytes)
		if err != nil {
			return result, err
		}
		result.BytesFreed += original - kept
	}
	return result, nil
}

// Append writes exactly one newline-terminated record. Rotation happens
// before the append, so every live/archive file remains within MaxBytes.
func Append(path string, record []byte, policy Policy) error {
	if err := validatePolicy(policy); err != nil {
		return err
	}
	record = bytes.TrimRight(record, "\r\n")
	record = append(record, '\n')
	if int64(len(record)) > policy.MaxBytes {
		return fmt.Errorf("jsonl record is %d bytes; maximum is %d", len(record), policy.MaxBytes)
	}
	return withLock(path, func() error {
		info, err := os.Stat(path)
		switch {
		case err == nil && info.Size()+int64(len(record)) > policy.MaxBytes:
			if err := rotate(path, policy.MaxArchives, policy.MaxBytes); err != nil {
				return err
			}
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(record)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	})
}

// Enforce compacts oversized historical files and removes archives outside
// the fixed ring. It is safe to run concurrently with Append.
func Enforce(path string, policy Policy) (EnforceResult, error) {
	if err := validatePolicy(policy); err != nil {
		return EnforceResult{}, err
	}
	result := EnforceResult{}
	err := withLock(path, func() error {
		for _, candidate := range archiveCandidates(path) {
			if candidate.index > policy.MaxArchives {
				info, err := os.Stat(candidate.path)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if err == nil {
					if err := os.Remove(candidate.path); err != nil {
						return err
					}
					result.BytesFreed += info.Size()
					result.FilesRemoved++
				}
			}
		}
		for index := policy.MaxArchives; index >= 0; index-- {
			candidate := path
			if index > 0 {
				candidate = fmt.Sprintf("%s.%d", path, index)
			}
			freed, err := trimTail(candidate, policy.MaxBytes)
			if err != nil {
				return err
			}
			result.BytesFreed += freed
		}
		return nil
	})
	return result, err
}

// PathsOldestFirst returns existing bounded-ring files in chronological
// order, then the live file. Readers use this to preserve append order.
func PathsOldestFirst(path string, maxArchives int) []string {
	paths := make([]string, 0, maxArchives+1)
	for index := maxArchives; index >= 1; index-- {
		candidate := fmt.Sprintf("%s.%d", path, index)
		if _, err := os.Stat(candidate); err == nil {
			paths = append(paths, candidate)
		}
	}
	if _, err := os.Stat(path); err == nil {
		paths = append(paths, path)
	}
	return paths
}

func validatePolicy(policy Policy) error {
	if policy.MaxBytes <= 0 {
		return errors.New("jsonl MaxBytes must be positive")
	}
	if policy.MaxArchives < 0 || policy.MaxArchives > 32 {
		return errors.New("jsonl MaxArchives must be between 0 and 32")
	}
	return nil
}

func withLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	unlock, err := filelock.Lock(lock)
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	return fn()
}

func rotate(path string, maxArchives int, maxBytes int64) error {
	if maxArchives == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if _, err := trimTail(path, maxBytes); err != nil {
		return err
	}
	oldest := fmt.Sprintf("%s.%d", path, maxArchives)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for index := maxArchives - 1; index >= 1; index-- {
		source := fmt.Sprintf("%s.%d", path, index)
		destination := fmt.Sprintf("%s.%d", path, index+1)
		if _, err := trimTail(source, maxBytes); err != nil {
			return err
		}
		if err := os.Rename(source, destination); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(path, path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func trimTail(path string, maxBytes int64) (int64, error) {
	original, kept, data, mode, err := tailData(path, maxBytes)
	if err != nil || original == kept {
		return 0, err
	}
	if _, err := atomicfile.WriteIfChanged(path, data, mode); err != nil {
		return 0, err
	}
	return original - kept, nil
}

func tailData(path string, maxBytes int64) (int64, int64, []byte, os.FileMode, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil, 0, nil
	}
	if err != nil {
		return 0, 0, nil, 0, err
	}
	if info.Size() <= maxBytes {
		return info.Size(), info.Size(), nil, info.Mode().Perm(), nil
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, nil, 0, err
	}
	defer file.Close()
	start := info.Size() - maxBytes
	if _, err := file.Seek(start, 0); err != nil {
		return 0, 0, nil, 0, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes))
	if err != nil {
		return 0, 0, nil, 0, err
	}
	if start > 0 {
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
			data = data[newline+1:]
		} else {
			data = nil
		}
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

func archiveCandidates(path string) []archiveCandidate {
	matches, _ := filepath.Glob(path + ".*")
	out := make([]archiveCandidate, 0, len(matches))
	for _, match := range matches {
		suffix := strings.TrimPrefix(match, path+".")
		index, err := strconv.Atoi(suffix)
		if err == nil && index > 0 {
			out = append(out, archiveCandidate{path: match, index: index})
		}
	}
	return out
}
