// Package jsonl provides cross-process-safe, bounded JSONL publication.
package jsonl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/filelock"
)

const (
	appendJournalVersion  = 1
	appendStatePreparing  = "preparing"
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

// Layout binds the lock, transaction journal, temporary backup prefix, and
// filesystem modes used by one JSONL publication. All paths must be distinct,
// clean siblings of the live file. Callers that need stable public names or
// private modes must pass the same layout to append and recovery.
type Layout struct {
	LockPath      string
	JournalPath   string
	BackupPrefix  string
	DirectoryMode os.FileMode
	FileMode      os.FileMode
	JournalMode   os.FileMode
	LockTimeout   time.Duration
}

func defaultLayout(path string) Layout {
	return Layout{
		LockPath: path + ".lock", JournalPath: path + ".append-transaction.json",
		BackupPrefix: path + ".append-backup", DirectoryMode: 0o755, FileMode: 0o644,
		JournalMode: 0o600,
	}
}

func validateLayout(path string, layout Layout) error {
	base := filepath.Base(path)
	if path == "" || filepath.Clean(path) != path || filepath.Dir(path) == path ||
		base == "." || base == ".." || base == string(filepath.Separator) {
		return errors.New("jsonl live path must be non-empty and clean")
	}
	directory := filepath.Dir(path)
	paths := []string{layout.LockPath, layout.JournalPath, layout.BackupPrefix}
	seen := map[string]bool{path: true}
	for _, candidate := range paths {
		if candidate == "" || filepath.Clean(candidate) != candidate || filepath.Dir(candidate) != directory || seen[candidate] {
			return errors.New("jsonl layout paths must be distinct clean siblings of the live file")
		}
		seen[candidate] = true
	}
	if layout.DirectoryMode.Perm() == 0 || layout.DirectoryMode.Perm() != layout.DirectoryMode ||
		layout.FileMode.Perm() == 0 || layout.FileMode.Perm() != layout.FileMode ||
		layout.JournalMode.Perm() == 0 || layout.JournalMode.Perm() != layout.JournalMode {
		return errors.New("jsonl layout modes must contain only non-zero permission bits")
	}
	if layout.LockTimeout < 0 || layout.LockTimeout > time.Minute {
		return errors.New("jsonl layout lock timeout must be between zero and one minute")
	}
	return nil
}

// EnforceResult reports bytes removed from existing live/archive files.
type EnforceResult struct {
	BytesFreed   int64
	FilesRemoved int
}

type appendJournal struct {
	FormatVersion  int                   `json:"format_version"`
	LayoutIdentity string                `json:"layout_identity,omitempty"`
	State          string                `json:"state"`
	Transactional  bool                  `json:"transactional"`
	Rotated        bool                  `json:"rotated"`
	MaxBytes       int64                 `json:"max_bytes"`
	MaxArchives    int                   `json:"max_archives"`
	LiveExisted    bool                  `json:"live_existed"`
	LiveSize       int64                 `json:"live_size"`
	Backups        []appendJournalBackup `json:"backups,omitempty"`
}

func layoutIdentity(path string, layout Layout) string {
	hash := sha256.New()
	for _, value := range []string{
		filepath.Clean(path), layout.LockPath, layout.JournalPath, layout.BackupPrefix,
		fmt.Sprintf("%04o", layout.DirectoryMode.Perm()), fmt.Sprintf("%04o", layout.FileMode.Perm()),
		fmt.Sprintf("%04o", layout.JournalMode.Perm()), layout.LockTimeout.String(),
	} {
		_, _ = io.WriteString(hash, value)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
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
	return AppendWithLayout(path, record, policy, defaultLayout(path))
}

// AppendWithLayout is Append with caller-owned stable auxiliary paths and
// filesystem modes.
func AppendWithLayout(path string, record []byte, policy Policy, layout Layout) error {
	if err := validatePolicy(policy); err != nil {
		return err
	}
	if err := validateLayout(path, layout); err != nil {
		return err
	}
	normalized := append(bytes.TrimRight(record, "\r\n"), '\n')
	if int64(len(normalized)) > policy.MaxBytes {
		return fmt.Errorf("jsonl record is %d bytes; maximum is %d", len(normalized), policy.MaxBytes)
	}
	return withLayoutLock(path, layout, func() error {
		if err := recoverAppendLockedWithLayout(path, layout, nil); err != nil {
			return err
		}
		return appendLockedWithLayout(path, normalized, policy, layout, nil)
	})
}

// AppendTransaction derives and appends one record while holding the same
// cross-process lock used by Append, then runs commit before releasing it.
// It is intended for chained logs whose next record depends on the current
// tail and whose detached head must advance with the append.
func AppendTransaction(path string, policy Policy, prepare func() ([]byte, error), commit func() error) error {
	return AppendTransactionWithLayout(path, policy, defaultLayout(path), prepare, commit)
}

// AppendTransactionWithLayout is AppendTransaction with caller-owned stable
// auxiliary paths and filesystem modes.
func AppendTransactionWithLayout(
	path string,
	policy Policy,
	layout Layout,
	prepare func() ([]byte, error),
	commit func() error,
) error {
	return AppendTransactionContextWithLayout(context.Background(), path, policy, layout, prepare, commit)
}

// AppendTransactionContextWithLayout bounds lock acquisition by both ctx and
// Layout.LockTimeout while preserving the same atomic append contract.
func AppendTransactionContextWithLayout(
	ctx context.Context,
	path string,
	policy Policy,
	layout Layout,
	prepare func() ([]byte, error),
	commit func() error,
) error {
	if ctx == nil {
		return errors.New("jsonl append context is required")
	}
	if err := validatePolicy(policy); err != nil {
		return err
	}
	if err := validateLayout(path, layout); err != nil {
		return err
	}
	if prepare == nil {
		return errors.New("jsonl prepare callback is required")
	}
	return withLayoutLockContext(ctx, path, layout, func() error {
		if err := recoverAppendLockedWithLayout(path, layout, commit); err != nil {
			return err
		}
		record, err := prepare()
		if err != nil {
			return err
		}
		return appendLockedWithLayout(path, record, policy, layout, commit)
	})
}

// Recover resolves a durable append journal left by an interrupted process.
// A transactional published append requires the same idempotent commit
// callback used by AppendTransaction; prepared writes roll back automatically.
func Recover(path string, commit func() error) error {
	return RecoverWithLayout(path, defaultLayout(path), commit)
}

// RecoverWithLayout resolves one transaction using the exact layout supplied
// to AppendTransactionWithLayout.
func RecoverWithLayout(path string, layout Layout, commit func() error) error {
	return RecoverContextWithLayout(context.Background(), path, layout, commit)
}

// RecoverContextWithLayout bounds recovery lock acquisition by ctx and the
// configured layout timeout.
func RecoverContextWithLayout(ctx context.Context, path string, layout Layout, commit func() error) error {
	if ctx == nil {
		return errors.New("jsonl recovery context is required")
	}
	if err := validateLayout(path, layout); err != nil {
		return err
	}
	if _, err := os.Lstat(layout.JournalPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return withLayoutLockContext(ctx, path, layout, func() error {
		return recoverAppendLockedWithLayout(path, layout, commit)
	})
}

// ReadSnapshotContextWithLayout recovers any interrupted append and executes
// read while holding the writer lock, so a multi-file reader observes one
// stable archive/live snapshot.
func ReadSnapshotContextWithLayout(
	ctx context.Context,
	path string,
	layout Layout,
	commit func() error,
	read func() error,
) error {
	if ctx == nil || read == nil {
		return errors.New("jsonl snapshot context and read callback are required")
	}
	if err := validateLayout(path, layout); err != nil {
		return err
	}
	return withLayoutLockContext(ctx, path, layout, func() error {
		if err := recoverAppendLockedWithLayout(path, layout, commit); err != nil {
			return err
		}
		return read()
	})
}

// ReadExistingSnapshotContextWithLayout is ReadSnapshotContextWithLayout for
// readers that must never create or repair a missing lock file.
func ReadExistingSnapshotContextWithLayout(
	ctx context.Context,
	path string,
	layout Layout,
	commit func() error,
	read func() error,
) error {
	if ctx == nil || read == nil {
		return errors.New("jsonl snapshot context and read callback are required")
	}
	if err := validateLayout(path, layout); err != nil {
		return err
	}
	return withExistingLayoutLockContext(ctx, path, layout, func() error {
		if err := recoverAppendLockedWithLayout(path, layout, commit); err != nil {
			return err
		}
		return read()
	})
}

// WithExistingLayoutLockContext executes inspect while holding an existing
// writer lock. It never creates the lock and never recovers or otherwise
// mutates JSONL state.
func WithExistingLayoutLockContext(
	ctx context.Context,
	path string,
	layout Layout,
	inspect func() error,
) error {
	if ctx == nil || inspect == nil {
		return errors.New("jsonl existing-lock context and inspect callback are required")
	}
	if err := validateLayout(path, layout); err != nil {
		return err
	}
	return withExistingLayoutLockContext(ctx, path, layout, inspect)
}

func appendLockedWithLayout(path string, record []byte, policy Policy, layout Layout, commit func() error) error {
	record = bytes.TrimRight(record, "\r\n")
	record = append(record, '\n')
	if int64(len(record)) > policy.MaxBytes {
		return fmt.Errorf("jsonl record is %d bytes; maximum is %d", len(record), policy.MaxBytes)
	}
	info, err := os.Lstat(path)
	rotateRequired := false
	switch {
	case err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()):
		return fmt.Errorf("jsonl live path must be a non-symlink regular file: %s", path)
	case err == nil && layout != defaultLayout(path) && runtime.GOOS != "windows" &&
		info.Mode().Perm() != layout.FileMode.Perm():
		return fmt.Errorf("jsonl live path has mode %o; want %o", info.Mode().Perm(), layout.FileMode.Perm())
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
		prepared, err := beginAppendJournalWithLayout(path, policy, layout, rotateRequired, transactional)
		if err != nil {
			return err
		}
		journal = &prepared
	}
	if rotateRequired {
		if err := rotate(path, policy.MaxArchives); err != nil {
			return rollbackAppendErrorWithLayout(path, layout, journal, err)
		}
	}
	appendLayout := layout
	if rotateRequired && layout == defaultLayout(path) && journal != nil &&
		len(journal.Backups) > 0 && journal.Backups[0].Existed {
		appendLayout.FileMode = os.FileMode(journal.Backups[0].Mode)
	}
	if err := appendRecordWithLayout(path, record, appendLayout); err != nil {
		return rollbackAppendErrorWithLayout(path, layout, journal, err)
	}
	if journal != nil {
		journal.State = appendStatePublished
		if err := writeAppendJournalWithLayout(path, layout, *journal); err != nil {
			return rollbackAppendErrorWithLayout(path, layout, journal, err)
		}
	}
	if commit != nil {
		if err := commit(); err != nil {
			return err
		}
	}
	if journal != nil {
		return finishAppendJournalWithLayout(path, layout, *journal)
	}
	return nil
}

func appendRecord(path string, record []byte) error {
	return appendRecordWithLayout(path, record, defaultLayout(path))
}

func appendRecordWithLayout(path string, record []byte, layout Layout) error {
	var before os.FileInfo
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("jsonl live path must be a non-symlink regular file: %s", path)
		}
		before = info
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, layout.FileMode)
	if err != nil {
		return err
	}
	opened, statErr := file.Stat()
	current, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || !opened.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(opened, current) || before != nil && !os.SameFile(before, opened) {
		if statErr == nil && lstatErr == nil {
			statErr = fmt.Errorf("jsonl live path changed identity while opening")
		}
		return errors.Join(statErr, lstatErr, file.Close())
	}
	if before == nil || layout != defaultLayout(path) {
		if err := file.Chmod(layout.FileMode); err != nil {
			return errors.Join(err, file.Close())
		}
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
	return EnforceWithLayout(path, policy, defaultLayout(path))
}

// EnforceWithLayout is Enforce with the same auxiliary layout used by the
// writer.
func EnforceWithLayout(path string, policy Policy, layout Layout) (EnforceResult, error) {
	if err := validatePolicy(policy); err != nil {
		return EnforceResult{}, err
	}
	if err := validateLayout(path, layout); err != nil {
		return EnforceResult{}, err
	}
	result := EnforceResult{}
	err := withLayoutLock(path, layout, func() error {
		if err := recoverAppendLockedWithLayout(path, layout, nil); err != nil {
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
		if info, err := os.Lstat(candidate); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return nil, fmt.Errorf("JSONL archive must be a non-symlink regular file: %s", candidate)
			}
			paths = append(paths, candidate)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("JSONL live path must be a non-symlink regular file: %s", path)
		}
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
	return withLayoutLock(path, defaultLayout(path), fn)
}

func withLayoutLock(path string, layout Layout, fn func() error) error {
	return withLayoutLockContext(context.Background(), path, layout, fn)
}

func withLayoutLockContext(ctx context.Context, path string, layout Layout, fn func() error) error {
	return withLayoutLockModeContext(ctx, path, layout, true, fn)
}

func withExistingLayoutLockContext(ctx context.Context, path string, layout Layout, fn func() error) error {
	return withLayoutLockModeContext(ctx, path, layout, false, fn)
}

func withLayoutLockModeContext(
	ctx context.Context,
	path string,
	layout Layout,
	create bool,
	fn func() error,
) error {
	if ctx == nil {
		return errors.New("jsonl lock context is required")
	}
	if fn == nil {
		return errors.New("jsonl lock callback is required")
	}
	if err := validateLayout(path, layout); err != nil {
		return err
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(path), layout.DirectoryMode); err != nil {
			return err
		}
	}
	if err := validateLayoutDirectory(path, layout); err != nil {
		return err
	}
	lockExisted := false
	if info, err := os.Lstat(layout.LockPath); err == nil {
		lockExisted = true
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("jsonl lock path must be a non-symlink regular file: %s", layout.LockPath)
		}
		if layout != defaultLayout(path) && runtime.GOOS != "windows" &&
			info.Mode().Perm() != layout.FileMode.Perm() {
			return fmt.Errorf("jsonl lock path has mode %o; want %o", info.Mode().Perm(), layout.FileMode.Perm())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	lock, err := os.OpenFile(layout.LockPath, flags, layout.FileMode)
	if err != nil {
		return err
	}
	opened, statErr := lock.Stat()
	current, lstatErr := os.Lstat(layout.LockPath)
	if statErr != nil || lstatErr != nil || !opened.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		if statErr == nil && lstatErr == nil {
			statErr = fmt.Errorf("jsonl lock path changed identity while opening")
		}
		return errors.Join(statErr, lstatErr, lock.Close())
	}
	if create && (!lockExisted || layout != defaultLayout(path)) {
		if err := lock.Chmod(layout.FileMode); err != nil {
			return errors.Join(err, lock.Close())
		}
	} else if runtime.GOOS != "windows" && opened.Mode().Perm() != layout.FileMode.Perm() {
		return errors.Join(fmt.Errorf(
			"jsonl lock path has mode %o; want %o", opened.Mode().Perm(), layout.FileMode.Perm(),
		), lock.Close())
	}
	unlock, err := acquireLayoutLock(ctx, lock, layout.LockTimeout)
	if err != nil {
		return errors.Join(err, lock.Close())
	}
	fnErr := fn()
	unlockErr := unlock()
	closeErr := lock.Close()
	if fnErr != nil {
		return errors.Join(fnErr, unlockErr, closeErr)
	}
	if unlockErr != nil {
		return errors.Join(fmt.Errorf("unlock JSONL: %w", unlockErr), closeErr)
	}
	return closeErr
}

func validateLayoutDirectory(path string, layout Layout) error {
	directory := filepath.Dir(path)
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("jsonl parent must be a non-symlink directory: %s", directory)
	}
	if layout != defaultLayout(path) && runtime.GOOS != "windows" &&
		info.Mode().Perm() != layout.DirectoryMode.Perm() {
		return fmt.Errorf(
			"jsonl parent has mode %o; want %o", info.Mode().Perm(), layout.DirectoryMode.Perm(),
		)
	}
	return nil
}

func acquireLayoutLock(ctx context.Context, file *os.File, timeout time.Duration) (func() error, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var deadline <-chan time.Time
	var timer *time.Timer
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		deadline = timer.C
		defer timer.Stop()
	}
	for {
		unlock, err := filelock.TryLock(file)
		if err == nil {
			return unlock, nil
		}
		if !filelock.IsContended(err) {
			return nil, fmt.Errorf("acquire JSONL lock: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("acquire JSONL lock: %w", ctx.Err())
		case <-deadline:
			return nil, fmt.Errorf("acquire JSONL lock timed out after %s", timeout)
		case <-ticker.C:
		}
	}
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
	return defaultLayout(path).JournalPath
}

func appendBackupPath(path string, index int) string {
	return appendBackupPathWithLayout(defaultLayout(path), index)
}

func appendBackupPathWithLayout(layout Layout, index int) string {
	return fmt.Sprintf("%s.%d", layout.BackupPrefix, index)
}

func beginAppendJournal(path string, policy Policy, rotated, transactional bool) (appendJournal, error) {
	return beginAppendJournalWithLayout(path, policy, defaultLayout(path), rotated, transactional)
}

func beginAppendJournalWithLayout(
	path string,
	policy Policy,
	layout Layout,
	rotated bool,
	transactional bool,
) (appendJournal, error) {
	state := appendStatePrepared
	if rotated {
		state = appendStatePreparing
	}
	journal := appendJournal{
		FormatVersion:  appendJournalVersion,
		LayoutIdentity: layoutIdentity(path, layout),
		State:          state,
		Transactional:  transactional,
		Rotated:        rotated,
		MaxBytes:       policy.MaxBytes,
		MaxArchives:    policy.MaxArchives,
		Backups:        []appendJournalBackup{},
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
		if err := writeAppendJournalWithLayout(path, layout, journal); err != nil {
			return appendJournal{}, err
		}
		for index := 0; index <= policy.MaxArchives; index++ {
			backup, err := createAppendBackupWithLayout(path, layout, index, policy.MaxBytes)
			if err != nil {
				cleanupErr := abortPreparingAppendWithLayout(layout, policy.MaxArchives)
				return appendJournal{}, errors.Join(err, wrapAppendCleanupError(cleanupErr))
			}
			journal.Backups = append(journal.Backups, backup)
			if err := writeAppendJournalWithLayout(path, layout, journal); err != nil {
				cleanupErr := abortPreparingAppendWithLayout(layout, policy.MaxArchives)
				return appendJournal{}, errors.Join(err, wrapAppendCleanupError(cleanupErr))
			}
		}
		journal.State = appendStatePrepared
	}
	if err := writeAppendJournalWithLayout(path, layout, journal); err != nil {
		cleanupErr := abortPreparingAppendWithLayout(layout, policy.MaxArchives)
		return appendJournal{}, errors.Join(err, wrapAppendCleanupError(cleanupErr))
	}
	return journal, nil
}

func createAppendBackupWithLayout(path string, layout Layout, index int, maxBytes int64) (appendJournalBackup, error) {
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
	if layout != defaultLayout(path) && runtime.GOOS != "windows" &&
		info.Mode().Perm() != layout.FileMode.Perm() {
		return appendJournalBackup{}, fmt.Errorf(
			"jsonl archive has mode %o; want %o: %s", info.Mode().Perm(), layout.FileMode.Perm(), source,
		)
	}
	backup.Existed = true
	backup.Mode = uint32(info.Mode().Perm())
	backupPath := appendBackupPathWithLayout(layout, index)
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
		backupMode := layout.FileMode
		if layout == defaultLayout(path) {
			backupMode = info.Mode().Perm()
		}
		if _, writeErr := atomicfile.WriteIfChanged(backupPath, data, backupMode); writeErr != nil {
			return appendJournalBackup{}, fmt.Errorf("write JSONL append backup: %w", writeErr)
		}
	}
	expectedMode := info.Mode().Perm()
	if layout != defaultLayout(path) {
		expectedMode = layout.FileMode
	}
	digest, err := digestBoundedBackupWithMode(backupPath, maxBytes, expectedMode)
	if err != nil {
		return appendJournalBackup{}, err
	}
	backup.Digest = digest
	return backup, nil
}

func digestBoundedBackupWithMode(path string, maxBytes int64, mode os.FileMode) (string, error) {
	data, err := readBoundedBackupWithMode(path, maxBytes, mode)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func readBoundedBackup(path string, maxBytes int64) ([]byte, error) {
	return boundedio.ReadRegularFile(path, maxBytes)
}

func readBoundedBackupWithMode(path string, maxBytes int64, mode os.FileMode) ([]byte, error) {
	var data []byte
	err := boundedio.WithRegularFileSnapshot(path, maxBytes, func(file *os.File, info os.FileInfo) error {
		if runtime.GOOS != "windows" && info.Mode().Perm() != mode.Perm() {
			return fmt.Errorf("JSONL transaction file %s has mode %o; want %o", path, info.Mode().Perm(), mode.Perm())
		}
		var readErr error
		data, readErr = io.ReadAll(io.LimitReader(file, maxBytes+1))
		if readErr != nil {
			return readErr
		}
		if int64(len(data)) > maxBytes || int64(len(data)) != info.Size() {
			return fmt.Errorf("JSONL transaction file %s changed or exceeds %d bytes", path, maxBytes)
		}
		return nil
	})
	return data, err
}

func writeAppendJournal(path string, journal appendJournal) error {
	return writeAppendJournalWithLayout(path, defaultLayout(path), journal)
}

func writeAppendJournalWithLayout(path string, layout Layout, journal appendJournal) error {
	body, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSONL append journal: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxAppendJournalBytes {
		return fmt.Errorf("JSONL append journal is %d bytes; maximum is %d", len(body), maxAppendJournalBytes)
	}
	if _, err := atomicfile.WriteIfChanged(layout.JournalPath, body, layout.JournalMode); err != nil {
		return fmt.Errorf("write JSONL append journal: %w", err)
	}
	return nil
}

func readAppendJournal(path string) (*appendJournal, error) {
	return readAppendJournalWithLayout(path, defaultLayout(path))
}

func readAppendJournalWithLayout(path string, layout Layout) (*appendJournal, error) {
	body, err := readBoundedBackupWithMode(layout.JournalPath, maxAppendJournalBytes, layout.JournalMode)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
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
	canonical, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode canonical JSONL append journal: %w", err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(body, canonical) {
		return nil, errors.New("JSONL append journal is not canonically encoded")
	}
	wantLayout := layoutIdentity(path, layout)
	if journal.LayoutIdentity == "" {
		if layout != defaultLayout(path) {
			return nil, errors.New("custom JSONL append journal lacks its exact layout identity")
		}
	} else if journal.LayoutIdentity != wantLayout {
		return nil, errors.New("JSONL append journal belongs to a different layout")
	}
	return &journal, nil
}

func validateAppendJournal(journal appendJournal) error {
	if journal.FormatVersion != appendJournalVersion {
		return fmt.Errorf("unsupported JSONL append journal version %d", journal.FormatVersion)
	}
	if journal.LayoutIdentity != "" && !lowerHexDigest(journal.LayoutIdentity) {
		return errors.New("JSONL append journal layout identity is invalid")
	}
	if journal.State != appendStatePreparing && journal.State != appendStatePrepared &&
		journal.State != appendStatePublished && journal.State != appendStateResolved {
		return fmt.Errorf("invalid JSONL append journal state %q", journal.State)
	}
	if err := validatePolicy(Policy{MaxBytes: journal.MaxBytes, MaxArchives: journal.MaxArchives}); err != nil {
		return err
	}
	if journal.LiveSize < 0 || !journal.LiveExisted && journal.LiveSize != 0 ||
		journal.LiveExisted && journal.LiveSize > journal.MaxBytes {
		return errors.New("JSONL append journal live-file metadata is invalid")
	}
	if !journal.Rotated && len(journal.Backups) != 0 {
		return errors.New("non-rotating JSONL append journal contains backups")
	}
	if journal.State == appendStatePreparing && (!journal.Rotated || len(journal.Backups) > journal.MaxArchives+1) {
		return errors.New("preparing JSONL append journal has invalid backups")
	}
	if journal.Rotated && journal.State != appendStatePreparing && len(journal.Backups) != journal.MaxArchives+1 {
		return errors.New("rotating JSONL append journal has incomplete backups")
	}
	for index, backup := range journal.Backups {
		if backup.Index != index {
			return errors.New("JSONL append journal backup indexes are not contiguous")
		}
		if backup.Existed && (backup.Mode == 0 || backup.Mode > 0o777) {
			return errors.New("JSONL append journal backup mode is invalid")
		}
		if backup.Existed {
			if !lowerHexDigest(backup.Digest) {
				return errors.New("JSONL append journal backup digest is invalid")
			}
		} else if backup.Mode != 0 || backup.Digest != "" {
			return errors.New("absent JSONL append journal backup contains file metadata")
		}
	}
	return nil
}

func lowerHexDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

func recoverAppendLockedWithLayout(path string, layout Layout, commit func() error) error {
	journal, err := readAppendJournalWithLayout(path, layout)
	if err != nil {
		return err
	}
	if journal == nil {
		return nil
	}
	if journal.State == appendStatePreparing {
		return abortPreparingAppendWithLayout(layout, journal.MaxArchives)
	}
	if journal.State == appendStatePrepared {
		return rollbackAppendJournalWithLayout(path, layout, *journal)
	}
	if journal.State == appendStateResolved {
		return finishAppendJournalWithLayout(path, layout, *journal)
	}
	if journal.Transactional {
		if commit == nil {
			return errors.New("published JSONL transaction requires its commit callback for recovery")
		}
		if err := commit(); err != nil {
			return fmt.Errorf("recover JSONL transaction commit: %w", err)
		}
	}
	return finishAppendJournalWithLayout(path, layout, *journal)
}

func rollbackAppendError(path string, journal *appendJournal, cause error) error {
	return rollbackAppendErrorWithLayout(path, defaultLayout(path), journal, cause)
}

func rollbackAppendErrorWithLayout(path string, layout Layout, journal *appendJournal, cause error) error {
	if journal == nil {
		return cause
	}
	if rollbackErr := rollbackAppendJournalWithLayout(path, layout, *journal); rollbackErr != nil {
		return errors.Join(cause, fmt.Errorf("rollback JSONL append: %w", rollbackErr))
	}
	return cause
}

func rollbackAppendJournalWithLayout(path string, layout Layout, journal appendJournal) error {
	if journal.Rotated {
		for _, backup := range journal.Backups {
			destination := archivePath(path, backup.Index)
			if !backup.Existed {
				if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				continue
			}
			mode := os.FileMode(backup.Mode)
			if layout != defaultLayout(path) {
				mode = layout.FileMode
			}
			data, err := readBoundedBackupWithMode(
				appendBackupPathWithLayout(layout, backup.Index), journal.MaxBytes, mode,
			)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(data)
			if hex.EncodeToString(digest[:]) != backup.Digest {
				return fmt.Errorf("JSONL append backup digest mismatch for archive %d", backup.Index)
			}
			restoreMode := os.FileMode(backup.Mode)
			if layout != defaultLayout(path) {
				restoreMode = layout.FileMode
			}
			if _, err := atomicfile.WriteIfChanged(destination, data, restoreMode); err != nil {
				return fmt.Errorf("restore JSONL append backup: %w", err)
			}
		}
	} else if journal.LiveExisted {
		if err := truncateRegularFile(path, journal.LiveSize); err != nil {
			return fmt.Errorf("truncate interrupted JSONL append: %w", err)
		}
	} else if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	journal.State = appendStateResolved
	if err := writeAppendJournalWithLayout(path, layout, journal); err != nil {
		return err
	}
	return finishAppendJournalWithLayout(path, layout, journal)
}

func truncateRegularFile(path string, size int64) error {
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return fmt.Errorf("JSONL live path must be a non-symlink regular file: %s", path)
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	opened, statErr := file.Stat()
	current, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || !opened.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(before, opened) || !os.SameFile(opened, current) {
		if statErr == nil && lstatErr == nil {
			statErr = fmt.Errorf("JSONL live path changed identity while opening for rollback")
		}
		return errors.Join(statErr, lstatErr, file.Close())
	}
	if opened.Size() < size {
		return errors.Join(fmt.Errorf(
			"JSONL live file is %d bytes; cannot restore prior size %d", opened.Size(), size,
		), file.Close())
	}
	truncateErr := file.Truncate(size)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(truncateErr, syncErr, closeErr)
}

func finishAppendJournalWithLayout(path string, layout Layout, journal appendJournal) error {
	if err := cleanupAppendBackupsWithLayout(layout, journal.Backups); err != nil {
		return err
	}
	if err := os.Remove(layout.JournalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func cleanupAppendBackupsWithLayout(layout Layout, backups []appendJournalBackup) error {
	var cleanupErr error
	for _, backup := range backups {
		if err := os.Remove(appendBackupPathWithLayout(layout, backup.Index)); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func abortPreparingAppendWithLayout(layout Layout, maxArchives int) error {
	var cleanupErr error
	for index := 0; index <= maxArchives; index++ {
		if err := os.Remove(appendBackupPathWithLayout(layout, index)); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	if err := os.Remove(layout.JournalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
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
		return info.Size(), info.Size(), nil, info.Mode().Perm(), nil
	}
	var data []byte
	err = boundedio.WithRegularFileSnapshot(path, info.Size(), func(file *os.File, opened os.FileInfo) error {
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
	entries, err := boundedio.ReadDir(directory, 4096)
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
