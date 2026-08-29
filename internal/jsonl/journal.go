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
)

func prepareRotationInputs(path string, maxArchives int, maxBytes int64) error {
	return prepareRotationInputsWithLayout(path, maxArchives, maxBytes, defaultLayout(path))
}

func prepareRotationInputsWithLayout(path string, maxArchives int, maxBytes int64, layout Layout) error {
	if err := layout.validateLockLease(); err != nil {
		return err
	}
	for index := maxArchives; index >= 0; index-- {
		candidate := archivePath(path, index)
		if _, err := trimTailWithLayout(candidate, maxBytes, layout); err != nil {
			return err
		}
	}
	return nil
}

func rotate(path string, maxArchives int) error {
	return rotateWithLayout(path, maxArchives, defaultLayout(path))
}

type rotationHooks struct {
	afterMutation func(int) error
}

func rotateWithHooks(path string, maxArchives int, hooks rotationHooks) (resultErr error) {
	return rotateWithLayoutHooks(path, maxArchives, defaultLayout(path), hooks)
}

func rotateWithLayout(path string, maxArchives int, layout Layout) error {
	return rotateWithLayoutHooks(path, maxArchives, layout, rotationHooks{})
}

func rotateWithLayoutHooks(path string, maxArchives int, layout Layout, hooks rotationHooks) (resultErr error) {
	parent, err := openJSONLParentWithLayout(path, layout)
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
	if err := layout.validateLockLease(); err != nil {
		return appendJournal{}, err
	}
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
		if err := layout.validateLockLease(); err != nil {
			return appendJournal{}, err
		}
		for index := 0; index <= policy.MaxArchives; index++ {
			backupPath := appendBackupPathWithLayout(layout, index)
			if _, err := os.Lstat(backupPath); err == nil {
				return appendJournal{}, newAppendBackupCollisionError(backupPath)
			} else if !errors.Is(err, os.ErrNotExist) {
				return appendJournal{}, err
			}
		}
		if err := writeAppendJournalWithLayout(path, layout, journal); err != nil {
			return appendJournal{}, err
		}
		for index := 0; index <= policy.MaxArchives; index++ {
			if err := layout.validateLockLease(); err != nil {
				cleanupErr := abortPreparingAppendWithLayout(layout, policy.MaxArchives)
				return appendJournal{}, errors.Join(err, wrapAppendCleanupError(cleanupErr))
			}
			backup, err := createAppendBackupWithLayout(path, layout, index, policy.MaxBytes)
			if err != nil {
				cleanupErr := abortPreparingAppendWithLayoutPreserving(layout, policy.MaxArchives, appendBackupCollisionPath(err))
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
	if err := layout.validateLockLease(); err != nil {
		cleanupErr := abortPreparingAppendWithLayout(layout, policy.MaxArchives)
		return appendJournal{}, errors.Join(err, wrapAppendCleanupError(cleanupErr))
	}
	if err := writeAppendJournalWithLayout(path, layout, journal); err != nil {
		cleanupErr := abortPreparingAppendWithLayout(layout, policy.MaxArchives)
		return appendJournal{}, errors.Join(err, wrapAppendCleanupError(cleanupErr))
	}
	return journal, nil
}

type appendBackupHooks struct {
	beforePublish func() error
}

type appendBackupCollisionError struct {
	path string
}

func newAppendBackupCollisionError(path string) error {
	return &appendBackupCollisionError{path: path}
}

func (e *appendBackupCollisionError) Error() string {
	return fmt.Sprintf("JSONL append backup target already exists: %s", e.path)
}

func appendBackupCollisionPath(err error) string {
	var collision *appendBackupCollisionError
	if errors.As(err, &collision) {
		return collision.path
	}
	return ""
}

func createAppendBackupWithLayout(path string, layout Layout, index int, maxBytes int64) (appendJournalBackup, error) {
	return createAppendBackupWithLayoutHooks(path, layout, index, maxBytes, appendBackupHooks{})
}

func createAppendBackupWithLayoutHooks(
	path string,
	layout Layout,
	index int,
	maxBytes int64,
	hooks appendBackupHooks,
) (backup appendJournalBackup, resultErr error) {
	if err := layout.validateLockLease(); err != nil {
		return appendJournalBackup{}, err
	}
	backup = appendJournalBackup{Index: index}
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
	if info.Size() > maxBytes {
		return appendJournalBackup{}, fmt.Errorf("jsonl archive %s exceeds %d bytes", source, maxBytes)
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
		return appendJournalBackup{}, newAppendBackupCollisionError(backupPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return appendJournalBackup{}, err
	}
	sourceFile, sourceInfo, sourceData, sourceLinks, err := openAppendBackupSource(source, info, layout, maxBytes)
	if err != nil {
		return appendJournalBackup{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, sourceFile.Close()) }()
	sourceDigest := sha256.Sum256(sourceData)
	wantDigest := hex.EncodeToString(sourceDigest[:])
	if hooks.beforePublish != nil {
		if err := hooks.beforePublish(); err != nil {
			return appendJournalBackup{}, err
		}
	}
	if err := layout.validateLockLease(); err != nil {
		return appendJournalBackup{}, err
	}
	if err := validateAppendBackupSource(sourceFile, sourceInfo, source, sourceData, sourceLinks, 0, maxBytes); err != nil {
		return appendJournalBackup{}, err
	}
	linked := false
	if err := linkJSONLPathWithLayout(source, backupPath, layout); err == nil {
		linked = true
	} else {
		if _, collisionErr := os.Lstat(backupPath); collisionErr == nil {
			return appendJournalBackup{}, newAppendBackupCollisionError(backupPath)
		} else if !errors.Is(collisionErr, os.ErrNotExist) {
			return appendJournalBackup{}, errors.Join(err, collisionErr)
		}
		if sourceErr := validateAppendBackupSource(sourceFile, sourceInfo, source, sourceData, sourceLinks, 0, maxBytes); sourceErr != nil {
			return appendJournalBackup{}, errors.Join(fmt.Errorf("link JSONL append backup: %w", err), sourceErr)
		}
		backupMode := layout.FileMode
		if layoutIsDefault(path, layout) {
			backupMode = info.Mode().Perm()
		}
		if err := layout.validateLockLease(); err != nil {
			return appendJournalBackup{}, err
		}
		if _, writeErr := atomicfile.WriteNew(backupPath, sourceData, backupMode); writeErr != nil {
			return appendJournalBackup{}, fmt.Errorf("write JSONL append backup: %w", writeErr)
		}
		if secureErr := secureLayoutSecurityFile(layout, backupPath, maxBytes); secureErr != nil {
			return appendJournalBackup{}, secureErr
		}
	}
	backupInfo, err := os.Lstat(backupPath)
	if err != nil {
		return appendJournalBackup{}, fmt.Errorf("inspect JSONL append backup: %w", err)
	}
	if linked && (!backupInfo.Mode().IsRegular() || !os.SameFile(sourceInfo, backupInfo)) {
		cleanupErr := removeAppendBackupIfSameWithLayout(backupPath, backupInfo, layout)
		return appendJournalBackup{}, errors.Join(
			fmt.Errorf("JSONL append backup is not linked to the validated archive: %s", backupPath), cleanupErr,
		)
	}
	linkDelta := uint64(0)
	if linked {
		linkDelta = 1
	}
	if err := validateAppendBackupSource(sourceFile, sourceInfo, source, sourceData, sourceLinks, linkDelta, maxBytes); err != nil {
		cleanupErr := removeAppendBackupIfSameWithLayout(backupPath, backupInfo, layout)
		return appendJournalBackup{}, errors.Join(err, cleanupErr)
	}
	expectedMode := info.Mode().Perm()
	if !layoutIsDefault(path, layout) {
		expectedMode = layout.FileMode
	}
	digest, err := digestBoundedBackupWithLayout(backupPath, maxBytes, expectedMode, layout)
	if err != nil {
		cleanupErr := removeAppendBackupIfSameWithLayout(backupPath, backupInfo, layout)
		return appendJournalBackup{}, errors.Join(err, cleanupErr)
	}
	if digest != wantDigest {
		cleanupErr := removeAppendBackupIfSameWithLayout(backupPath, backupInfo, layout)
		return appendJournalBackup{}, errors.Join(
			fmt.Errorf("JSONL append backup content changed during publication: %s", backupPath), cleanupErr,
		)
	}
	backup.Digest = digest
	return backup, nil
}

func openAppendBackupSource(
	source string,
	before os.FileInfo,
	layout Layout,
	maxBytes int64,
) (*os.File, os.FileInfo, []byte, uint64, error) {
	file, err := os.Open(source)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	closeOnError := func(cause error) (*os.File, os.FileInfo, []byte, uint64, error) {
		return nil, nil, nil, 0, errors.Join(cause, file.Close())
	}
	opened, statErr := file.Stat()
	current, lstatErr := os.Lstat(source)
	if statErr != nil || lstatErr != nil {
		return closeOnError(errors.Join(statErr, lstatErr))
	}
	if !sameAppendBackupSnapshot(before, opened) || !sameAppendBackupSnapshot(opened, current) {
		return closeOnError(fmt.Errorf("JSONL archive changed identity while opening: %s", source))
	}
	beforeLinks, err := appendPathLinkCount(source, before)
	if err != nil {
		return closeOnError(err)
	}
	openedLinks, err := appendFileLinkCount(file, opened)
	if err != nil {
		return closeOnError(err)
	}
	if beforeLinks != openedLinks {
		return closeOnError(fmt.Errorf("JSONL archive hard-link count changed while opening: %s", source))
	}
	if err := validateOpenedLayoutSecurityFile(layout, file, opened, maxBytes); err != nil {
		return closeOnError(err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return closeOnError(fmt.Errorf("read JSONL archive: %w", err))
	}
	if int64(len(data)) > maxBytes || int64(len(data)) != opened.Size() {
		return closeOnError(fmt.Errorf("JSONL archive changed or exceeds %d bytes: %s", maxBytes, source))
	}
	if err := validateAppendBackupSource(file, opened, source, data, openedLinks, 0, maxBytes); err != nil {
		return closeOnError(err)
	}
	return file, opened, data, openedLinks, nil
}

func validateAppendBackupSource(
	file *os.File,
	expected os.FileInfo,
	source string,
	expectedData []byte,
	expectedLinks uint64,
	linkDelta uint64,
	maxBytes int64,
) error {
	if expectedLinks > ^uint64(0)-linkDelta {
		return fmt.Errorf("JSONL archive hard-link count overflow: %s", source)
	}
	opened, statErr := file.Stat()
	current, lstatErr := os.Lstat(source)
	if statErr != nil || lstatErr != nil {
		return errors.Join(statErr, lstatErr)
	}
	wantLinks := expectedLinks + linkDelta
	currentLinks, linkErr := appendFileLinkCount(file, opened)
	pathLinks, pathLinkErr := appendPathLinkCount(source, current)
	if linkErr != nil || pathLinkErr != nil {
		return errors.Join(linkErr, pathLinkErr)
	}
	if !sameAppendBackupSnapshot(expected, opened) || !sameAppendBackupSnapshot(opened, current) ||
		currentLinks != wantLinks || pathLinks != wantLinks {
		return fmt.Errorf("JSONL archive identity, metadata, or hard-link count changed: %s", source)
	}
	if opened.Size() > maxBytes {
		return fmt.Errorf("JSONL archive exceeds %d bytes: %s", maxBytes, source)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind JSONL archive: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return fmt.Errorf("read JSONL archive identity: %w", err)
	}
	if int64(len(data)) != opened.Size() || !bytes.Equal(data, expectedData) {
		return fmt.Errorf("JSONL archive content changed during publication: %s", source)
	}
	return nil
}

func sameAppendBackupSnapshot(left, right os.FileInfo) bool {
	return left != nil && right != nil && left.Mode().IsRegular() && right.Mode().IsRegular() &&
		left.Mode()&os.ModeSymlink == 0 && right.Mode()&os.ModeSymlink == 0 &&
		os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime())
}

func removeAppendBackupIfSameWithLayout(path string, expected os.FileInfo, layout Layout) error {
	if err := layout.validateLockLease(); err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if expected == nil || !os.SameFile(expected, current) {
		return fmt.Errorf("refuse to remove changed JSONL append backup: %s", path)
	}
	return removeJSONLPathWithLayout(path, layout)
}

func digestBoundedBackupWithLayout(path string, maxBytes int64, mode os.FileMode, layout Layout) (string, error) {
	data, err := readBoundedBackupWithLayout(path, maxBytes, mode, layout)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func readBoundedBackupWithLayout(path string, maxBytes int64, mode os.FileMode, layout Layout) ([]byte, error) {
	var data []byte
	err := withValidatedLayoutSecurityFile(layout, path, maxBytes, func(file *os.File, info os.FileInfo) error {
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
	if err != nil {
		return nil, err
	}
	return data, err
}

func writeAppendJournal(path string, journal appendJournal) error {
	return writeAppendJournalWithLayout(path, defaultLayout(path), journal)
}

func writeAppendJournalWithLayout(path string, layout Layout, journal appendJournal) error {
	if err := layout.validateLockLease(); err != nil {
		return err
	}
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
	if err := layout.validateLockLease(); err != nil {
		return err
	}
	result, err := atomicfile.WriteIfChanged(layout.JournalPath, body, layout.JournalMode)
	if err != nil {
		return fmt.Errorf("write JSONL append journal: %w", err)
	}
	if result.Changed {
		if err := layout.validateLockLease(); err != nil {
			return err
		}
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
			return nil, fmt.Errorf("JSONL append journal %s: %w", layout.JournalPath, ErrLayoutMismatch)
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
	if err := layout.validateLockLease(); err != nil {
		return err
	}
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
			if err := layout.validateLockLease(); err != nil {
				return err
			}
			journal.State = appendStateCommitting
			if err := writeAppendJournalWithLayout(path, layout, *journal); err != nil {
				return err
			}
		}
		if err := layout.validateLockLease(); err != nil {
			return err
		}
		if err := commit(); err != nil {
			return fmt.Errorf("recover JSONL transaction commit: %w", err)
		}
		if err := layout.validateLockLease(); err != nil {
			return err
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
	if err := layout.validateLockLease(); err != nil {
		return err
	}
	if journal.Rotated {
		for _, backup := range journal.Backups {
			destination := archivePath(path, backup.Index)
			if !backup.Existed {
				if err := removeJSONLPathWithLayout(destination, layout); err != nil {
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
			if err := layout.validateLockLease(); err != nil {
				return err
			}
			result, err := atomicfile.WriteIfChanged(destination, data, restoreMode)
			if err != nil {
				return fmt.Errorf("restore JSONL append backup: %w", err)
			}
			if result.Changed {
				if err := secureLayoutSecurityFile(layout, destination, journal.MaxBytes); err != nil {
					return err
				}
			}
		}
	} else if journal.LiveExisted {
		if err := truncateRegularFileWithLayout(path, journal.LiveSize, journal.MaxBytes, layout); err != nil {
			return fmt.Errorf("truncate interrupted JSONL append: %w", err)
		}
	} else if err := removeJSONLPathWithLayout(path, layout); err != nil {
		return err
	}
	journal.State = appendStateResolved
	if err := writeAppendJournalWithLayout(path, layout, journal); err != nil {
		return err
	}
	return finishAppendJournalWithLayout(path, layout, journal)
}

func truncateRegularFileWithLayout(path string, size, maximum int64, layout Layout) error {
	if err := layout.validateLockLease(); err != nil {
		return err
	}
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
	if err := layout.validateLockLease(); err != nil {
		return errors.Join(err, file.Close())
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
	if err := layout.validateLockLease(); err != nil {
		return err
	}
	if err := cleanupAppendBackupsWithLayout(layout, journal.Backups); err != nil {
		return err
	}
	if err := removeJSONLPathWithLayout(layout.JournalPath, layout); err != nil {
		return err
	}
	return nil
}

func cleanupAppendBackupsWithLayout(layout Layout, backups []appendJournalBackup) error {
	var cleanupErr error
	for _, backup := range backups {
		if err := removeJSONLPathWithLayout(appendBackupPathWithLayout(layout, backup.Index), layout); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func abortPreparingAppendWithLayout(layout Layout, maxArchives int) error {
	return abortPreparingAppendWithLayoutPreserving(layout, maxArchives, "")
}

func abortPreparingAppendWithLayoutPreserving(layout Layout, maxArchives int, preservePath string) error {
	var cleanupErr error
	for index := 0; index <= maxArchives; index++ {
		if err := layout.validateLockLease(); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			break
		}
		backupPath := appendBackupPathWithLayout(layout, index)
		if backupPath == preservePath {
			continue
		}
		if err := removeJSONLPathWithLayout(backupPath, layout); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	if err := removeJSONLPathWithLayout(layout.JournalPath, layout); err != nil {
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
