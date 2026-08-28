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
	"runtime"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
)

func prepareRotationInputs(path string, maxArchives int, maxBytes int64) error {
	return prepareRotationInputsWithLayout(path, maxArchives, maxBytes, defaultLayout(path))
}

func prepareRotationInputsWithLayout(path string, maxArchives int, maxBytes int64, layout Layout) error {
	for index := maxArchives; index >= 0; index-- {
		candidate := archivePath(path, index)
		if _, err := trimTailWithLayout(candidate, maxBytes, layout); err != nil {
			return err
		}
	}
	return nil
}

func rotate(path string, maxArchives int) error {
	return rotateWithHooks(path, maxArchives, rotationHooks{})
}

type rotationHooks struct {
	afterMutation func(int) error
}

func rotateWithHooks(path string, maxArchives int, hooks rotationHooks) (resultErr error) {
	parent, err := openJSONLParent(path)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, parent.close()) }()
	mutation := 0
	commit := func(changed bool, err error) error {
		if err != nil || !changed {
			return err
		}
		mutation++
		if hooks.afterMutation != nil {
			return hooks.afterMutation(mutation)
		}
		return nil
	}
	if maxArchives == 0 {
		return commit(parent.remove(parent.name))
	}
	oldest := fmt.Sprintf("%s.%d", parent.name, maxArchives)
	if err := commit(parent.remove(oldest)); err != nil {
		return err
	}
	for index := maxArchives - 1; index >= 1; index-- {
		source := fmt.Sprintf("%s.%d", parent.name, index)
		destination := fmt.Sprintf("%s.%d", parent.name, index+1)
		if err := commit(parent.rename(source, destination)); err != nil {
			return err
		}
	}
	return commit(parent.rename(parent.name, parent.name+".1"))
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
	if err := validateLayoutSecurityFile(layout, source, maxBytes); err != nil {
		return appendJournalBackup{}, err
	}
	if !layoutIsDefault(path, layout) && runtime.GOOS != "windows" &&
		info.Mode().Perm() != layout.FileMode.Perm() {
		return appendJournalBackup{}, fmt.Errorf(
			"jsonl archive has mode %o; want %o: %s", info.Mode().Perm(), layout.FileMode.Perm(), source,
		)
	}
	backup.Existed = true
	backup.Mode = uint32(info.Mode().Perm())
	backupPath := appendBackupPathWithLayout(layout, index)
	if _, err := os.Lstat(backupPath); err == nil {
		if err := validateLayoutSecurityFile(layout, backupPath, maxBytes); err != nil {
			return appendJournalBackup{}, err
		}
		if err := removeJSONLPath(backupPath); err != nil {
			return appendJournalBackup{}, fmt.Errorf("remove stale JSONL append backup: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return appendJournalBackup{}, err
	}
	if err := linkJSONLPath(source, backupPath); err != nil {
		data, readErr := readBoundedBackup(source, maxBytes)
		if readErr != nil {
			return appendJournalBackup{}, readErr
		}
		backupMode := layout.FileMode
		if layoutIsDefault(path, layout) {
			backupMode = info.Mode().Perm()
		}
		changed, writeErr := atomicfile.WriteIfChanged(backupPath, data, backupMode)
		if writeErr != nil {
			return appendJournalBackup{}, fmt.Errorf("write JSONL append backup: %w", writeErr)
		}
		if changed {
			if secureErr := secureLayoutSecurityFile(layout, backupPath, maxBytes); secureErr != nil {
				return appendJournalBackup{}, secureErr
			}
		}
	}
	expectedMode := info.Mode().Perm()
	if !layoutIsDefault(path, layout) {
		expectedMode = layout.FileMode
	}
	digest, err := digestBoundedBackupWithLayout(backupPath, maxBytes, expectedMode, layout)
	if err != nil {
		return appendJournalBackup{}, err
	}
	backup.Digest = digest
	return backup, nil
}

func digestBoundedBackupWithLayout(path string, maxBytes int64, mode os.FileMode, layout Layout) (string, error) {
	data, err := readBoundedBackupWithLayout(path, maxBytes, mode, layout)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func readBoundedBackup(path string, maxBytes int64) ([]byte, error) {
	return boundedio.ReadRegularFile(path, maxBytes)
}

func readBoundedBackupWithLayout(path string, maxBytes int64, mode os.FileMode, layout Layout) ([]byte, error) {
	if err := validateLayoutSecurityFile(layout, path, maxBytes); err != nil {
		return nil, err
	}
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
	if _, err := os.Lstat(layout.JournalPath); err == nil {
		if err := validateLayoutSecurityFile(layout, layout.JournalPath, maxAppendJournalBytes); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	changed, err := atomicfile.WriteIfChanged(layout.JournalPath, body, layout.JournalMode)
	if err != nil {
		return fmt.Errorf("write JSONL append journal: %w", err)
	}
	if changed {
		if err := secureLayoutSecurityFile(layout, layout.JournalPath, maxAppendJournalBytes); err != nil {
			return err
		}
	}
	return nil
}

func readAppendJournal(path string) (*appendJournal, error) {
	return readAppendJournalWithLayout(path, defaultLayout(path))
}

func readAppendJournalWithLayout(path string, layout Layout) (*appendJournal, error) {
	body, err := readBoundedBackupWithLayout(layout.JournalPath, maxAppendJournalBytes, layout.JournalMode, layout)
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
		if !layoutIsDefault(path, layout) {
			return nil, errors.New("custom JSONL append journal lacks its exact layout identity")
		}
	} else if journal.LayoutIdentity != wantLayout {
		legacyIdentity := layoutIdentityWithSecurity(path, layout, false)
		if layout.Security == nil || journal.LayoutIdentity != legacyIdentity {
			return nil, errors.New("JSONL append journal belongs to a different layout")
		}
	}
	return &journal, nil
}

func validateAppendJournal(journal appendJournal) error {
	if journal.FormatVersion != legacyJournalVersion && journal.FormatVersion != appendJournalVersion {
		return fmt.Errorf("unsupported JSONL append journal version %d", journal.FormatVersion)
	}
	if journal.LayoutIdentity != "" && !lowerHexDigest(journal.LayoutIdentity) {
		return errors.New("JSONL append journal layout identity is invalid")
	}
	if journal.State != appendStatePreparing && journal.State != appendStatePrepared &&
		journal.State != appendStatePublished && journal.State != appendStateCommitting &&
		journal.State != appendStateResolved {
		return fmt.Errorf("invalid JSONL append journal state %q", journal.State)
	}
	if journal.FormatVersion == legacyJournalVersion && journal.State == appendStateCommitting {
		return errors.New("legacy JSONL append journal has an unsupported committing state")
	}
	if journal.State == appendStateCommitting && !journal.Transactional {
		return errors.New("non-transactional JSONL append journal cannot be committing")
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
		if journal.State == appendStatePublished && journal.FormatVersion == appendJournalVersion && commit == nil {
			return rollbackAppendJournalWithLayout(path, layout, *journal)
		}
		if commit == nil {
			return ErrTransactionCommitRequired
		}
		if journal.State == appendStatePublished {
			journal.State = appendStateCommitting
			if err := writeAppendJournalWithLayout(path, layout, *journal); err != nil {
				return err
			}
		}
		if err := commit(); err != nil {
			return fmt.Errorf("recover JSONL transaction commit: %w", err)
		}
		journal.State = appendStateResolved
		if err := writeAppendJournalWithLayout(path, layout, *journal); err != nil {
			return err
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
				if err := removeJSONLPath(destination); err != nil {
					return err
				}
				continue
			}
			mode := os.FileMode(backup.Mode)
			if !layoutIsDefault(path, layout) {
				mode = layout.FileMode
			}
			data, err := readBoundedBackupWithLayout(
				appendBackupPathWithLayout(layout, backup.Index), journal.MaxBytes, mode, layout,
			)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(data)
			if hex.EncodeToString(digest[:]) != backup.Digest {
				return fmt.Errorf("JSONL append backup digest mismatch for archive %d", backup.Index)
			}
			restoreMode := os.FileMode(backup.Mode)
			if !layoutIsDefault(path, layout) {
				restoreMode = layout.FileMode
			}
			if _, err := os.Lstat(destination); err == nil {
				if err := validateLayoutSecurityFile(layout, destination, journal.MaxBytes); err != nil {
					return err
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			changed, err := atomicfile.WriteIfChanged(destination, data, restoreMode)
			if err != nil {
				return fmt.Errorf("restore JSONL append backup: %w", err)
			}
			if changed {
				if err := secureLayoutSecurityFile(layout, destination, journal.MaxBytes); err != nil {
					return err
				}
			}
		}
	} else if journal.LiveExisted {
		if err := truncateRegularFileWithLayout(path, journal.LiveSize, journal.MaxBytes, layout); err != nil {
			return fmt.Errorf("truncate interrupted JSONL append: %w", err)
		}
	} else if err := removeJSONLPath(path); err != nil {
		return err
	}
	journal.State = appendStateResolved
	if err := writeAppendJournalWithLayout(path, layout, journal); err != nil {
		return err
	}
	return finishAppendJournalWithLayout(path, layout, journal)
}

func truncateRegularFileWithLayout(path string, size, maximum int64, layout Layout) error {
	if err := validateLayoutSecurityFile(layout, path, maximum); err != nil {
		return err
	}
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
	if err := errors.Join(truncateErr, syncErr, closeErr); err != nil {
		return err
	}
	return validateLayoutSecurityFile(layout, path, maximum)
}

func finishAppendJournalWithLayout(path string, layout Layout, journal appendJournal) error {
	if err := cleanupAppendBackupsWithLayout(layout, journal.Backups); err != nil {
		return err
	}
	if err := removeJSONLPath(layout.JournalPath); err != nil {
		return err
	}
	return nil
}

func cleanupAppendBackupsWithLayout(layout Layout, backups []appendJournalBackup) error {
	var cleanupErr error
	for _, backup := range backups {
		if err := removeJSONLPath(appendBackupPathWithLayout(layout, backup.Index)); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func abortPreparingAppendWithLayout(layout Layout, maxArchives int) error {
	var cleanupErr error
	for index := 0; index <= maxArchives; index++ {
		if err := removeJSONLPath(appendBackupPathWithLayout(layout, index)); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	if err := removeJSONLPath(layout.JournalPath); err != nil {
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
