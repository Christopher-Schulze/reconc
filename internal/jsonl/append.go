package jsonl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
)

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
	normalized, err := normalizeRecord(record, policy.MaxBytes)
	if err != nil {
		return err
	}
	return withLayoutLockLeaseContext(context.Background(), path, layout, true, func(lockedLayout Layout) error {
		if err := recoverAppendLockedWithLayout(path, lockedLayout, nil); err != nil {
			return err
		}
		return appendNormalizedLockedWithLayout(path, normalized, policy, lockedLayout, nil)
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
	return withLayoutLockLeaseContext(ctx, path, layout, true, func(lockedLayout Layout) error {
		if err := recoverAppendLockedWithLayout(path, lockedLayout, commit); err != nil {
			return err
		}
		record, err := prepare()
		if err != nil {
			return err
		}
		return appendLockedWithLayout(path, record, policy, lockedLayout, commit)
	})
}

// Recover resolves a durable append journal left by an interrupted process.
// Prepared writes and version-2 publications whose commit did not start roll
// back without a callback. Once commit may have started, recovery requires the
// same idempotent callback used by AppendTransaction. A resolved journal only
// needs artifact cleanup and never invokes commit again.
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
	return withLayoutLockLeaseContext(ctx, path, layout, true, func(lockedLayout Layout) error {
		return recoverAppendLockedWithLayout(path, lockedLayout, commit)
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
	return withLayoutLockLeaseContext(ctx, path, layout, true, func(lockedLayout Layout) error {
		if err := recoverAppendLockedWithLayout(path, lockedLayout, commit); err != nil {
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
	return withLayoutLockLeaseContext(ctx, path, layout, false, func(lockedLayout Layout) error {
		if err := recoverAppendLockedWithLayout(path, lockedLayout, commit); err != nil {
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
	return withLayoutLockLeaseContext(ctx, path, layout, false, func(lockedLayout Layout) error {
		if err := lockedLayout.validateLockLease(); err != nil {
			return err
		}
		return inspect()
	})
}

func appendLockedWithLayout(path string, record []byte, policy Policy, layout Layout, commit func() error) error {
	normalized, err := normalizeRecord(record, policy.MaxBytes)
	if err != nil {
		return err
	}
	return appendNormalizedLockedWithLayout(path, normalized, policy, layout, commit)
}

func normalizeRecord(record []byte, maximum int64) ([]byte, error) {
	trimmed := bytes.TrimRight(record, "\r\n")
	normalizedBytes := len(trimmed) + 1
	if int64(normalizedBytes) > maximum {
		return nil, fmt.Errorf("jsonl record is %d bytes; maximum is %d", normalizedBytes, maximum)
	}
	normalized := make([]byte, normalizedBytes)
	copy(normalized, trimmed)
	normalized[len(normalized)-1] = '\n'
	return normalized, nil
}

func appendNormalizedLockedWithLayout(
	path string,
	record []byte,
	policy Policy,
	layout Layout,
	commit func() error,
) error {
	if err := layout.validateLockLease(); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err == nil {
		if securityErr := validateLayoutSecurityFile(layout, path, policy.MaxBytes); securityErr != nil {
			return securityErr
		}
	}
	rotateRequired := false
	switch {
	case err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()):
		return fmt.Errorf("jsonl live path must be a non-symlink regular file: %s", path)
	case err == nil && !layoutIsDefault(path, layout) && runtime.GOOS != "windows" &&
		info.Mode().Perm() != layout.FileMode.Perm():
		return fmt.Errorf("jsonl live path has mode %o; want %o", info.Mode().Perm(), layout.FileMode.Perm())
	case err == nil && info.Size()+int64(len(record)) > policy.MaxBytes:
		rotateRequired = true
		if err := layout.validateLockLease(); err != nil {
			return err
		}
		if err := prepareRotationInputsWithLayout(path, policy.MaxArchives, policy.MaxBytes, layout); err != nil {
			return err
		}
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return err
	}
	transactional := commit != nil
	var journal *appendJournal
	if rotateRequired || transactional {
		if err := layout.validateLockLease(); err != nil {
			return err
		}
		prepared, err := beginAppendJournalWithLayout(path, policy, layout, rotateRequired, transactional)
		if err != nil {
			return err
		}
		journal = &prepared
	}
	if rotateRequired {
		if err := rotateWithLayout(path, policy.MaxArchives, layout); err != nil {
			return rollbackAppendErrorWithLayout(path, layout, journal, err)
		}
	}
	appendLayout := layout
	if rotateRequired && layoutIsDefault(path, layout) && journal != nil &&
		len(journal.Backups) > 0 && journal.Backups[0].Existed {
		appendLayout.FileMode = os.FileMode(journal.Backups[0].Mode)
	}
	if err := appendRecordWithLayout(path, record, appendLayout, policy.MaxBytes); err != nil {
		return rollbackAppendErrorWithLayout(path, layout, journal, err)
	}
	if journal != nil {
		if err := layout.validateLockLease(); err != nil {
			return rollbackAppendErrorWithLayout(path, layout, journal, err)
		}
		journal.State = appendStatePublished
		if err := writeAppendJournalWithLayout(path, layout, *journal); err != nil {
			return rollbackAppendErrorWithLayout(path, layout, journal, err)
		}
	}
	if commit != nil {
		if err := layout.validateLockLease(); err != nil {
			return err
		}
		journal.State = appendStateCommitting
		if err := writeAppendJournalWithLayout(path, layout, *journal); err != nil {
			return err
		}
		if err := commit(); err != nil {
			return err
		}
		if err := layout.validateLockLease(); err != nil {
			return err
		}
		journal.State = appendStateResolved
		if err := writeAppendJournalWithLayout(path, layout, *journal); err != nil {
			return err
		}
	}
	if journal != nil {
		if err := layout.validateLockLease(); err != nil {
			return err
		}
		return finishAppendJournalWithLayout(path, layout, *journal)
	}
	return nil
}

func appendRecord(path string, record []byte) error {
	return appendRecordWithLayout(path, record, defaultLayout(path), int64(len(record))+1)
}

func appendRecordWithLayout(path string, record []byte, layout Layout, maximum int64) (resultErr error) {
	return appendRecordWithLayoutHooks(path, record, layout, maximum, appendOpenHooks{})
}

type appendOpenHooks struct {
	afterInspect func(missing bool) error
}

func appendRecordWithLayoutHooks(path string, record []byte, layout Layout, maximum int64, hooks appendOpenHooks) (resultErr error) {
	parent, err := openJSONLParentWithLayout(path, layout)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, parent.close()) }()
	file, before, created, strictLink, err := openAppendRecordFile(parent, path, layout, maximum, hooks)
	if err != nil {
		return err
	}
	opened, statErr := file.Stat()
	current, lstatErr := parent.root.Lstat(parent.name)
	if statErr != nil || lstatErr != nil || !opened.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(opened, current) || before != nil && !os.SameFile(before, opened) {
		if statErr == nil && lstatErr == nil {
			statErr = fmt.Errorf("jsonl live path changed identity while opening")
		}
		return errors.Join(statErr, lstatErr, file.Close())
	}
	if err := parent.validate(); err != nil {
		if created {
			err = errors.Join(err, removeCreatedAppendFile(parent, file, opened))
		}
		return errors.Join(err, file.Close())
	}
	if strictLink {
		if err := validateAppendSingleLink(file, opened); err != nil {
			return errors.Join(err, file.Close())
		}
	}
	if err := parent.validateLockLease(); err != nil {
		return errors.Join(err, file.Close())
	}
	if layout.Security != nil && created {
		if err := secureLayoutSecurityFile(layout, path, maximum); err != nil {
			return errors.Join(err, file.Close())
		}
	} else if layout.Security == nil && (created || !layoutIsDefault(path, layout)) {
		if err := parent.validateLockLease(); err != nil {
			return errors.Join(err, file.Close())
		}
		if err := file.Chmod(layout.FileMode); err != nil {
			return errors.Join(err, file.Close())
		}
	}
	if err := parent.validateLockLease(); err != nil {
		return errors.Join(err, file.Close())
	}
	writeErr := writeFull(file, record)
	syncErr := file.Sync()
	closeErr := file.Close()
	var parentSyncErr error
	if created {
		parentSyncErr = parent.syncMutation()
	}
	if err := errors.Join(writeErr, syncErr, closeErr, parentSyncErr); err != nil {
		return err
	}
	return validateLayoutSecurityFile(layout, path, maximum)
}

func removeCreatedAppendFile(parent *jsonlParent, file *os.File, opened os.FileInfo) error {
	if err := parent.validateLockLease(); err != nil {
		return err
	}
	current, lstatErr := parent.root.Lstat(parent.name)
	if lstatErr != nil || opened == nil || current == nil || !opened.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return errors.Join(fmt.Errorf("refuse to remove changed JSONL creation"), lstatErr)
	}
	if err := parent.root.Remove(parent.name); err != nil {
		return fmt.Errorf("remove rejected JSONL creation: %w", err)
	}
	return jsonlDirectorySync(parent.root)
}

func openAppendRecordFile(parent *jsonlParent, path string, layout Layout, maximum int64, hooks appendOpenHooks) (*os.File, os.FileInfo, bool, bool, error) {
	before, missing, err := inspectAppendRecordFile(parent, path, layout, maximum)
	if err != nil {
		return nil, nil, false, false, err
	}
	if hooks.afterInspect != nil {
		if err := hooks.afterInspect(missing); err != nil {
			return nil, nil, false, false, err
		}
	}
	if err := parent.validateLockLease(); err != nil {
		return nil, nil, false, false, err
	}
	if !missing {
		file, err := parent.root.OpenFile(parent.name, os.O_APPEND|os.O_WRONLY, 0)
		return file, before, false, false, err
	}
	if err := parent.validateLockLease(); err != nil {
		return nil, nil, false, false, err
	}
	file, err := parent.root.OpenFile(parent.name, os.O_CREATE|os.O_EXCL|os.O_APPEND|os.O_WRONLY, layout.FileMode)
	if err == nil {
		return file, nil, true, true, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, nil, false, false, err
	}
	before, missing, err = inspectAppendRecordFile(parent, path, layout, maximum)
	if err != nil || missing {
		return nil, nil, false, false, errors.Join(fmt.Errorf("jsonl live path changed identity during creation"), err)
	}
	if err := parent.validateLockLease(); err != nil {
		return nil, nil, false, false, err
	}
	file, err = parent.root.OpenFile(parent.name, os.O_APPEND|os.O_WRONLY, 0)
	return file, before, false, true, err
}

func inspectAppendRecordFile(parent *jsonlParent, path string, layout Layout, maximum int64) (os.FileInfo, bool, error) {
	info, err := parent.root.Lstat(parent.name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("jsonl live path must be a non-symlink regular file: %s", path)
	}
	if !layoutIsDefault(path, layout) && runtime.GOOS != "windows" && info.Mode().Perm() != layout.FileMode.Perm() {
		return nil, false, fmt.Errorf("jsonl live path has mode %o; want %o", info.Mode().Perm(), layout.FileMode.Perm())
	}
	if err := validateLayoutSecurityFile(layout, path, maximum); err != nil {
		return nil, false, err
	}
	return info, false, nil
}

// Enforce compacts oversized historical files and removes archives outside
// the fixed ring. It is safe to run concurrently with Append.
