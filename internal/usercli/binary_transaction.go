package usercli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/privatefs"
)

const binaryCopyBufferBytes = 128 << 10

var beforeBinaryBackupSnapshot = func(string) error { return nil }

type binaryBackup struct {
	exists   bool
	path     string
	mode     os.FileMode
	digest   string
	size     int64
	identity os.FileInfo
	retain   bool
}

func withBinaryBackup(path string, operation func(*binaryBackup) error) (resultErr error) {
	backup, err := captureBinaryBackup(path)
	if err != nil {
		return err
	}
	return withCapturedBinaryBackup(backup, operation)
}

func withCapturedBinaryBackup(backup binaryBackup, operation func(*binaryBackup) error) (resultErr error) {
	defer func() { resultErr = errors.Join(resultErr, backup.cleanup()) }()
	if operation == nil {
		return errors.New("binary backup operation is required")
	}
	return operation(&backup)
}

func withPrivateTemporaryBinary(directory, pattern string, operation func(string) error) (resultErr error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return fmt.Errorf("create private temporary binary: %w", err)
	}
	path := file.Name()
	if err := privatefs.SecureFile(file); err != nil {
		return errors.Join(fmt.Errorf("secure private temporary binary: %w", err), file.Close(), os.Remove(path))
	}
	if err := file.Close(); err != nil {
		return errors.Join(fmt.Errorf("close private temporary binary: %w", err), os.Remove(path))
	}
	defer func() {
		removeErr := os.Remove(path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		resultErr = errors.Join(resultErr, removeErr)
	}()
	if err := validatePrivateTemporary(path, 0); err != nil {
		return fmt.Errorf("validate private temporary binary: %w", err)
	}
	return operation(path)
}

func captureBinaryBackup(path string) (binaryBackup, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return binaryBackup{}, nil
	}
	if err != nil {
		return binaryBackup{}, fmt.Errorf("inspect previous user CLI: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return binaryBackup{}, fmt.Errorf("previous user CLI is not a regular file or is a symlink: %s", path)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".reconc-backup-*.private")
	if err != nil {
		return binaryBackup{}, fmt.Errorf("create private user CLI backup: %w", err)
	}
	backup := binaryBackup{exists: true, path: file.Name()}
	closed := false
	cleanup := func(primary error) (binaryBackup, error) {
		var closeErr error
		if !closed {
			closeErr = file.Close()
			closed = true
		}
		return binaryBackup{}, errors.Join(primary, closeErr, os.Remove(backup.path))
	}
	if err := privatefs.SecureFile(file); err != nil {
		return cleanup(fmt.Errorf("make user CLI backup private: %w", err))
	}
	if err := beforeBinaryBackupSnapshot(path); err != nil {
		return cleanup(err)
	}
	hash := sha256.New()
	err = boundedio.WithRegularFileSnapshot(path, maxBinaryBytes, func(source *os.File, sourceInfo os.FileInfo) error {
		backup.identity = sourceInfo
		backup.mode = sourceInfo.Mode().Perm()
		written, copyErr := io.CopyBuffer(io.MultiWriter(file, hash), source, make([]byte, binaryCopyBufferBytes))
		if copyErr != nil {
			return fmt.Errorf("stream previous user CLI: %w", copyErr)
		}
		if written != sourceInfo.Size() {
			return fmt.Errorf("stream previous user CLI: copied %d of %d bytes", written, sourceInfo.Size())
		}
		backup.size = written
		return nil
	})
	if err != nil {
		return cleanup(fmt.Errorf("capture previous user CLI: %w", err))
	}
	if err := file.Sync(); err != nil {
		return cleanup(fmt.Errorf("sync previous user CLI backup: %w", err))
	}
	if err := file.Close(); err != nil {
		closed = true
		return cleanup(fmt.Errorf("close previous user CLI backup: %w", err))
	}
	closed = true
	backup.digest = hex.EncodeToString(hash.Sum(nil))
	if err := validatePrivateTemporary(backup.path, backup.size); err != nil {
		return cleanup(fmt.Errorf("validate previous user CLI backup: %w", err))
	}
	return backup, nil
}

func validateBinaryBackupSnapshot(path string, expected *binaryBackup) error {
	if expected == nil || !expected.exists || expected.identity == nil {
		return errors.New("owned binary snapshot is unavailable")
	}
	hash := sha256.New()
	var current os.FileInfo
	err := boundedio.WithRegularFileSnapshot(path, maxBinaryBytes, func(file *os.File, info os.FileInfo) error {
		current = info
		written, copyErr := io.CopyBuffer(hash, file, make([]byte, binaryCopyBufferBytes))
		if copyErr != nil {
			return copyErr
		}
		if written != info.Size() {
			return fmt.Errorf("hashed %d of %d bytes", written, info.Size())
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("validate owned binary snapshot: %w", err)
	}
	if !os.SameFile(expected.identity, current) || expected.mode != current.Mode().Perm() ||
		expected.size != current.Size() || expected.digest != hex.EncodeToString(hash.Sum(nil)) {
		return errors.New("owned binary changed during uninstall")
	}
	return nil
}

func (backup *binaryBackup) cleanup() error {
	if backup == nil || backup.path == "" || backup.retain {
		return nil
	}
	err := os.Remove(backup.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func publishBinaryFromFile(target, source string, mode os.FileMode) error {
	return boundedio.WithRegularFileSnapshot(source, maxBinaryBytes, func(file *os.File, _ os.FileInfo) error {
		_, err := atomicfile.WriteStream(target, file, maxBinaryBytes, mode)
		return err
	})
}

func rollbackInstall(path string, backup *binaryBackup, changed bool, cause error) error {
	if !changed {
		return cause
	}
	if backup != nil && backup.exists {
		if err := publishBinaryFromFile(path, backup.path, backup.mode); err != nil {
			backup.retain = true
			return errors.Join(cause, fmt.Errorf("restore previous user CLI from %s: %w", backup.path, err))
		}
		digest, err := fileSHA256(path)
		if err != nil {
			backup.retain = true
			return errors.Join(cause, fmt.Errorf("verify restored user CLI from %s: %w", backup.path, err))
		}
		if digest != backup.digest {
			backup.retain = true
			return errors.Join(cause, fmt.Errorf("verify restored user CLI from %s: checksum mismatch", backup.path))
		}
		if err := os.Chmod(path, backup.mode); err != nil {
			backup.retain = true
			return errors.Join(cause, fmt.Errorf("restore previous user CLI mode from %s: %w", backup.path, err))
		}
		return cause
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(cause, fmt.Errorf("remove failed user CLI publication: %w", err))
	}
	return cause
}

func validatePrivateTemporary(path string, expectedSize int64) error {
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() || before.Size() != expectedSize {
		return errors.New("temporary binary changed identity, type, or size")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	opened, statErr := file.Stat()
	current, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || opened == nil || current == nil ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		opened.Size() != expectedSize || !os.SameFile(before, opened) || !os.SameFile(opened, current) {
		return errors.Join(errors.New("temporary binary changed identity, type, or size"), statErr, lstatErr, file.Close())
	}
	validateErr := privatefs.ValidateFile(file, opened)
	current, lstatErr = os.Lstat(path)
	if lstatErr != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		validateErr = errors.Join(validateErr, errors.New("temporary binary changed identity after validation"), lstatErr)
	}
	return errors.Join(validateErr, file.Close())
}
