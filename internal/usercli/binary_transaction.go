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
)

const binaryCopyBufferBytes = 128 << 10

type binaryBackup struct {
	exists bool
	path   string
	mode   os.FileMode
	digest string
	size   int64
	retain bool
}

func withBinaryBackup(path string, operation func(*binaryBackup) error) (resultErr error) {
	backup, err := captureBinaryBackup(path)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, backup.cleanup()) }()
	return operation(&backup)
}

func withPrivateTemporaryBinary(directory, pattern string, operation func(string) error) (resultErr error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return fmt.Errorf("create private temporary binary: %w", err)
	}
	path := file.Name()
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
	backup := binaryBackup{exists: true, path: file.Name(), mode: info.Mode().Perm()}
	closed := false
	cleanup := func(primary error) (binaryBackup, error) {
		var closeErr error
		if !closed {
			closeErr = file.Close()
			closed = true
		}
		return binaryBackup{}, errors.Join(primary, closeErr, os.Remove(backup.path))
	}
	if err := file.Chmod(0o600); err != nil {
		return cleanup(fmt.Errorf("make user CLI backup private: %w", err))
	}
	hash := sha256.New()
	err = boundedio.WithRegularFileSnapshot(path, maxBinaryBytes, func(source *os.File, sourceInfo os.FileInfo) error {
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
		return atomicfile.WriteStream(target, file, maxBinaryBytes, mode)
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
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() != expectedSize {
		return errors.New("temporary binary changed identity, type, or size")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("temporary binary permissions are not private: %o", info.Mode().Perm())
	}
	return nil
}
