// Package jsonl provides cross-process-safe, bounded JSONL publication.
package jsonl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

const (
	appendJournalVersion  = 1
	appendStatePrepared   = "prepared"
	appendStatePublished  = "published"
	appendStateResolved   = "resolved"
	maxAppendJournalBytes = 64 * 1024
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

type appendJournal struct {
	FormatVersion int                   `json:"format_version"`
	State         string                `json:"state"`
	Transactional bool                  `json:"transactional"`
	Rotated       bool                  `json:"rotated"`
	MaxBytes      int64                 `json:"max_bytes"`
	MaxArchives   int                   `json:"max_archives"`
	LiveExisted   bool                  `json:"live_existed"`
	LiveSize      int64                 `json:"live_size"`
	Backups       []appendJournalBackup `json:"backups,omitempty"`
}

type appendJournalBackup struct {
	Index   int    `json:"index"`
	Existed bool   `json:"existed"`
	Mode    uint32 `json:"mode,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

// Inspect reports the cleanup Enforce would perform against the current
// snapshot without creating locks, temp files, or other filesystem state.
func Inspect(path string, policy Policy) (EnforceResult, error) {
	if err := validatePolicy(policy); err != nil {
		return EnforceResult{}, err
	}
	result := EnforceResult{}
	candidates, err := archiveCandidates(path)
	if err != nil {
		return result, err
	}
	for _, candidate := range candidates {
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
	normalized := append(bytes.TrimRight(record, "\r\n"), '\n')
	if int64(len(normalized)) > policy.MaxBytes {
		return fmt.Errorf("jsonl record is %d bytes; maximum is %d", len(normalized), policy.MaxBytes)
	}
	return withLock(path, func() error {
		if err := recoverAppendLocked(path, nil); err != nil {
			return err
		}
		return appendLocked(path, normalized, policy, nil)
	})
}

// AppendTransaction derives and appends one record while holding the same
// cross-process lock used by Append, then runs commit before releasing it.
// It is intended for chained logs whose next record depends on the current
// tail and whose detached head must advance with the append.
func AppendTransaction(path string, policy Policy, prepare func() ([]byte, error), commit func() error) error {
	if err := validatePolicy(policy); err != nil {
		return err
	}
	if prepare == nil {
		return errors.New("jsonl prepare callback is required")
	}
	return withLock(path, func() error {
		if err := recoverAppendLocked(path, commit); err != nil {
			return err
		}
		record, err := prepare()
		if err != nil {
			return err
		}
		return appendLocked(path, record, policy, commit)
	})
}

// Recover resolves a durable append journal left by an interrupted process.
// A transactional published append requires the same idempotent commit
// callback used by AppendTransaction; prepared writes roll back automatically.
func Recover(path string, commit func() error) error {
	if _, err := os.Lstat(appendJournalPath(path)); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return withLock(path, func() error {
		return recoverAppendLocked(path, commit)
	})
}

func appendLocked(path string, record []byte, policy Policy, commit func() error) error {
	record = bytes.TrimRight(record, "\r\n")
	record = append(record, '\n')
	if int64(len(record)) > policy.MaxBytes {
		return fmt.Errorf("jsonl record is %d bytes; maximum is %d", len(record), policy.MaxBytes)
	}
	info, err := os.Stat(path)
	rotateRequired := false
	switch {
	case err == nil && info.Size()+int64(len(record)) > policy.MaxBytes:
		rotateRequired = true
		if err := prepareRotationInputs(path, policy.MaxArchives, policy.MaxBytes); err != nil {
			return err
		}
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return err
	}
	transactional := commit != nil
	var journal *appendJournal
	if rotateRequired || transactional {
		prepared, err := beginAppendJournal(path, policy, rotateRequired, transactional)
		if err != nil {
			return err
		}
		journal = &prepared
	}
	if rotateRequired {
		if err := rotate(path, policy.MaxArchives); err != nil {
			return rollbackAppendError(path, journal, err)
		}
	}
	if err := appendRecord(path, record); err != nil {
		return rollbackAppendError(path, journal, err)
	}
	if journal != nil {
		journal.State = appendStatePublished
		if err := writeAppendJournal(path, *journal); err != nil {
			return rollbackAppendError(path, journal, err)
		}
	}
	if commit != nil {
		if err := commit(); err != nil {
			return err
		}
	}
	if journal != nil {
		return finishAppendJournal(path, *journal)
	}
	return nil
}

func appendRecord(path string, record []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	writeErr := writeFull(file, record)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// Enforce compacts oversized historical files and removes archives outside
// the fixed ring. It is safe to run concurrently with Append.
func Enforce(path string, policy Policy) (EnforceResult, error) {
	if err := validatePolicy(policy); err != nil {
		return EnforceResult{}, err
	}
	result := EnforceResult{}
	err := withLock(path, func() error {
		if err := recoverAppendLocked(path, nil); err != nil {
			return err
		}
		candidates, err := archiveCandidates(path)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
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
func PathsOldestFirst(path string, maxArchives int) ([]string, error) {
	if maxArchives < 0 || maxArchives > 32 {
		return nil, errors.New("jsonl maxArchives must be between 0 and 32")
	}
	paths := make([]string, 0, maxArchives+1)
	for index := maxArchives; index >= 1; index-- {
		candidate := fmt.Sprintf("%s.%d", path, index)
		if _, err := os.Stat(candidate); err == nil {
			paths = append(paths, candidate)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if _, err := os.Stat(path); err == nil {
		paths = append(paths, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return paths, nil
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
	fnErr := fn()
	unlockErr := unlock()
	if fnErr != nil {
		return fnErr
	}
	if unlockErr != nil {
		return fmt.Errorf("unlock JSONL: %w", unlockErr)
	}
	return nil
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(data) {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func prepareRotationInputs(path string, maxArchives int, maxBytes int64) error {
	for index := maxArchives; index >= 0; index-- {
		candidate := archivePath(path, index)
		if _, err := trimTail(candidate, maxBytes); err != nil {
			return err
		}
	}
	return nil
}

func rotate(path string, maxArchives int) error {
	if maxArchives == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	oldest := fmt.Sprintf("%s.%d", path, maxArchives)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for index := maxArchives - 1; index >= 1; index-- {
		source := fmt.Sprintf("%s.%d", path, index)
		destination := fmt.Sprintf("%s.%d", path, index+1)
		if err := os.Rename(source, destination); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(path, path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func archivePath(path string, index int) string {
	if index == 0 {
		return path
	}
	return fmt.Sprintf("%s.%d", path, index)
}

func appendJournalPath(path string) string {
	return path + ".append-transaction.json"
}

func appendBackupPath(path string, index int) string {
	return fmt.Sprintf("%s.append-backup.%d", path, index)
}

func beginAppendJournal(path string, policy Policy, rotated, transactional bool) (appendJournal, error) {
	journal := appendJournal{
		FormatVersion: appendJournalVersion,
		State:         appendStatePrepared,
		Transactional: transactional,
		Rotated:       rotated,
		MaxBytes:      policy.MaxBytes,
		MaxArchives:   policy.MaxArchives,
		Backups:       []appendJournalBackup{},
	}
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return appendJournal{}, fmt.Errorf("jsonl live path is not a regular file: %s", path)
		}
		journal.LiveExisted = true
		journal.LiveSize = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return appendJournal{}, err
	}
	if rotated {
		for index := 0; index <= policy.MaxArchives; index++ {
			backup, err := createAppendBackup(path, index, policy.MaxBytes)
			if err != nil {
				cleanupErr := cleanupAppendBackups(path, journal.Backups)
				return appendJournal{}, errors.Join(err, wrapAppendCleanupError(cleanupErr))
			}
			journal.Backups = append(journal.Backups, backup)
		}
	}
	if err := writeAppendJournal(path, journal); err != nil {
		cleanupErr := cleanupAppendBackups(path, journal.Backups)
		return appendJournal{}, errors.Join(err, wrapAppendCleanupError(cleanupErr))
	}
	return journal, nil
}

func createAppendBackup(path string, index int, maxBytes int64) (appendJournalBackup, error) {
	backup := appendJournalBackup{Index: index}
	source := archivePath(path, index)
	info, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return backup, nil
	}
	if err != nil {
		return appendJournalBackup{}, err
	}
	if !info.Mode().IsRegular() {
		return appendJournalBackup{}, fmt.Errorf("jsonl archive is not a regular file: %s", source)
	}
	backup.Existed = true
	backup.Mode = uint32(info.Mode().Perm())
	backupPath := appendBackupPath(path, index)
	if _, err := os.Lstat(backupPath); err == nil {
		if err := os.Remove(backupPath); err != nil {
			return appendJournalBackup{}, fmt.Errorf("remove stale JSONL append backup: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return appendJournalBackup{}, err
	}
	if err := os.Link(source, backupPath); err != nil {
		data, readErr := readBoundedBackup(source, maxBytes)
		if readErr != nil {
			return appendJournalBackup{}, readErr
		}
		if _, writeErr := atomicfile.WriteIfChanged(backupPath, data, info.Mode().Perm()); writeErr != nil {
			return appendJournalBackup{}, fmt.Errorf("write JSONL append backup: %w", writeErr)
		}
	}
	digest, err := digestBoundedBackup(backupPath, maxBytes)
	if err != nil {
		return appendJournalBackup{}, err
	}
	backup.Digest = digest
	return backup, nil
}

func digestBoundedBackup(path string, maxBytes int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}
	if written > maxBytes {
		return "", fmt.Errorf("JSONL append backup source exceeds %d bytes: %s", maxBytes, path)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readBoundedBackup(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("JSONL append backup source exceeds %d bytes: %s", maxBytes, path)
	}
	return data, nil
}

func writeAppendJournal(path string, journal appendJournal) error {
	body, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSONL append journal: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxAppendJournalBytes {
		return fmt.Errorf("JSONL append journal is %d bytes; maximum is %d", len(body), maxAppendJournalBytes)
	}
	if _, err := atomicfile.WriteIfChanged(appendJournalPath(path), body, 0o600); err != nil {
		return fmt.Errorf("write JSONL append journal: %w", err)
	}
	return nil
}

func readAppendJournal(path string) (*appendJournal, error) {
	file, err := os.Open(appendJournalPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxAppendJournalBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if len(body) > maxAppendJournalBytes {
		return nil, fmt.Errorf("JSONL append journal exceeds %d bytes", maxAppendJournalBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var journal appendJournal
	if err := decoder.Decode(&journal); err != nil {
		return nil, fmt.Errorf("decode JSONL append journal: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("JSONL append journal contains trailing data")
	}
	if err := validateAppendJournal(journal); err != nil {
		return nil, err
	}
	return &journal, nil
}

func validateAppendJournal(journal appendJournal) error {
	if journal.FormatVersion != appendJournalVersion {
		return fmt.Errorf("unsupported JSONL append journal version %d", journal.FormatVersion)
	}
	if journal.State != appendStatePrepared && journal.State != appendStatePublished && journal.State != appendStateResolved {
		return fmt.Errorf("invalid JSONL append journal state %q", journal.State)
	}
	if err := validatePolicy(Policy{MaxBytes: journal.MaxBytes, MaxArchives: journal.MaxArchives}); err != nil {
		return err
	}
	if journal.LiveSize < 0 {
		return errors.New("JSONL append journal live size is negative")
	}
	if !journal.Rotated && len(journal.Backups) != 0 {
		return errors.New("non-rotating JSONL append journal contains backups")
	}
	if journal.Rotated && len(journal.Backups) != journal.MaxArchives+1 {
		return errors.New("rotating JSONL append journal has incomplete backups")
	}
	for index, backup := range journal.Backups {
		if backup.Index != index {
			return errors.New("JSONL append journal backup indexes are not contiguous")
		}
		if backup.Existed && backup.Mode > 0o777 {
			return errors.New("JSONL append journal backup mode is invalid")
		}
		if backup.Existed {
			digest, err := hex.DecodeString(backup.Digest)
			if err != nil || len(digest) != sha256.Size {
				return errors.New("JSONL append journal backup digest is invalid")
			}
		} else if backup.Mode != 0 || backup.Digest != "" {
			return errors.New("absent JSONL append journal backup contains file metadata")
		}
	}
	return nil
}

func recoverAppendLocked(path string, commit func() error) error {
	journal, err := readAppendJournal(path)
	if err != nil {
		return err
	}
	if journal == nil {
		return nil
	}
	if journal.State == appendStatePrepared {
		return rollbackAppendJournal(path, *journal)
	}
	if journal.State == appendStateResolved {
		return finishAppendJournal(path, *journal)
	}
	if journal.Transactional {
		if commit == nil {
			return errors.New("published JSONL transaction requires its commit callback for recovery")
		}
		if err := commit(); err != nil {
			return fmt.Errorf("recover JSONL transaction commit: %w", err)
		}
	}
	return finishAppendJournal(path, *journal)
}

func rollbackAppendError(path string, journal *appendJournal, cause error) error {
	if journal == nil {
		return cause
	}
	if rollbackErr := rollbackAppendJournal(path, *journal); rollbackErr != nil {
		return errors.Join(cause, fmt.Errorf("rollback JSONL append: %w", rollbackErr))
	}
	return cause
}

func rollbackAppendJournal(path string, journal appendJournal) error {
	if journal.Rotated {
		for _, backup := range journal.Backups {
			destination := archivePath(path, backup.Index)
			if !backup.Existed {
				if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				continue
			}
			data, err := readBoundedBackup(appendBackupPath(path, backup.Index), journal.MaxBytes)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(data)
			if hex.EncodeToString(digest[:]) != backup.Digest {
				return fmt.Errorf("JSONL append backup digest mismatch for archive %d", backup.Index)
			}
			if _, err := atomicfile.WriteIfChanged(destination, data, os.FileMode(backup.Mode)); err != nil {
				return fmt.Errorf("restore JSONL append backup: %w", err)
			}
		}
	} else if journal.LiveExisted {
		if err := os.Truncate(path, journal.LiveSize); err != nil {
			return fmt.Errorf("truncate interrupted JSONL append: %w", err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(syncErr, closeErr); err != nil {
			return err
		}
	} else if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	journal.State = appendStateResolved
	if err := writeAppendJournal(path, journal); err != nil {
		return err
	}
	return finishAppendJournal(path, journal)
}

func finishAppendJournal(path string, journal appendJournal) error {
	if err := cleanupAppendBackups(path, journal.Backups); err != nil {
		return err
	}
	if err := os.Remove(appendJournalPath(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func cleanupAppendBackups(path string, backups []appendJournalBackup) error {
	var cleanupErr error
	for _, backup := range backups {
		if err := os.Remove(appendBackupPath(path, backup.Index)); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func wrapAppendCleanupError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("clean up JSONL append backups: %w", err)
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

func archiveCandidates(path string) ([]archiveCandidate, error) {
	directory := filepath.Dir(path)
	entries, err := os.ReadDir(directory)
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
