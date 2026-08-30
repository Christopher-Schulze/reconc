package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const maxWriteNewRecoveryEntries = 4096

var (
	linkWriteNewTemporary = func(directory *os.Root, temporary, target string) error {
		return directory.Link(temporary, target)
	}
	syncWriteNewTemporary = func(file *os.File) error {
		return file.Sync()
	}
	closeWriteNewTemporary = func(file *os.File) error {
		return file.Close()
	}
	removeWriteNewTemporary = func(directory *os.Root, name string) error {
		return directory.Remove(name)
	}
	beforeWriteNewRecoveryRemoval = func(*os.Root, string) error { return nil }
)

// WriteNew atomically publishes a new file and refuses every existing target.
// The temporary file is created in the destination directory so the final
// hard-link publication is same-filesystem and cannot expose partial bytes.
func WriteNew(path string, data []byte, mode os.FileMode) (PublicationResult, error) {
	return writeNew(path, data, mode, PublicParentMode)
}

// WritePrivateNew is WriteNew with private permissions for every parent
// directory that must be created.
func WritePrivateNew(path string, data []byte, mode os.FileMode) (PublicationResult, error) {
	return writeNew(path, data, mode, PrivateParentMode)
}

func writeNew(path string, data []byte, mode, parentMode os.FileMode) (result PublicationResult, err error) {
	parent, name, err := bindParent(path, parentMode)
	if err != nil {
		return PublicationResult{}, err
	}
	defer func() { err = errors.Join(err, result.markUncertainOnClose(parent.close())) }()
	directory := parent.directory()
	if err := recoverWriteNewHardlinks(parent, name, path); err != nil {
		return PublicationResult{}, err
	}
	if err := validateCurrent(directory, name, nil); err != nil {
		return PublicationResult{}, fmt.Errorf("refuse existing target %s: %w", path, err)
	}
	temporary, err := prepareNewFile(directory, name, data, mode)
	if err != nil {
		return PublicationResult{}, err
	}
	if err := parent.validate(); err != nil {
		return PublicationResult{}, errors.Join(err, cleanupWriteNewTemporary(directory, temporary))
	}
	if err := validateCurrent(directory, name, nil); err != nil {
		return PublicationResult{}, errors.Join(
			fmt.Errorf("validate new target %s: %w", path, err),
			cleanupWriteNewTemporary(directory, temporary),
		)
	}
	if err := linkWriteNewTemporary(directory, temporary, name); err != nil {
		publishErr := fmt.Errorf("publish new %s: %w", path, err)
		if errors.Is(err, os.ErrExist) {
			publishErr = errors.Join(ErrCurrentChanged, publishErr)
		}
		return PublicationResult{}, errors.Join(publishErr, cleanupWriteNewTemporary(directory, temporary))
	}
	result.markPublished()
	cleanupPublishedTemporary := func() error {
		removeErr := cleanupWriteNewTemporary(directory, temporary)
		if removeErr != nil {
			return errors.Join(removeErr, parent.validate())
		}
		return errors.Join(syncParentDir(directory), parent.validate())
	}
	if err := syncParentDir(directory); err != nil {
		return result, errors.Join(
			fmt.Errorf("sync parent after publishing %s: %w", path, err),
			cleanupPublishedTemporary(),
		)
	}
	if err := parent.validate(); err != nil {
		return result, errors.Join(
			fmt.Errorf("validate parent after publishing %s: %w", path, err),
			cleanupPublishedTemporary(),
		)
	}
	if err := removeWriteNewTemporary(directory, temporary); err != nil {
		return result, fmt.Errorf("remove publication temporary for %s: %w", path, err)
	}
	if err := syncParentDir(directory); err != nil {
		return result, fmt.Errorf("sync parent for %s: %w", path, err)
	}
	if err := parent.validate(); err != nil {
		return result, fmt.Errorf("validate parent after publishing %s: %w", path, err)
	}
	result.markDurable()
	return result, nil
}

func recoverWriteNewHardlinks(parent *boundParent, target, path string) (err error) {
	directory := parent.directory()
	targetFile, targetInfo, err := openCurrent(directory, target, path)
	if err != nil {
		return fmt.Errorf("inspect publication target before temporary recovery: %w", err)
	}
	if targetFile == nil {
		return nil
	}
	defer func() { err = errors.Join(err, targetFile.Close()) }()
	opened, err := directory.Open(".")
	if err != nil {
		return fmt.Errorf("open parent while recovering publication temporaries for %s: %w", path, err)
	}
	entries, readErr := opened.ReadDir(maxWriteNewRecoveryEntries + 1)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	closeErr := opened.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read parent while recovering publication temporaries for %s: %w", path, err)
	}
	if len(entries) > maxWriteNewRecoveryEntries {
		return fmt.Errorf(
			"%w: refuse publication temporary recovery for %s: parent exceeds %d entries",
			ErrCurrentChanged,
			path,
			maxWriteNewRecoveryEntries,
		)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !isWriteNewTemporaryName(target, name) {
			continue
		}
		candidate, err := directory.Lstat(name)
		if err != nil {
			return fmt.Errorf("inspect publication temporary for %s: %w", path, err)
		}
		if !sameRegularIdentity(targetInfo, candidate) {
			continue
		}
		if beforeWriteNewRecoveryRemoval != nil {
			if err := beforeWriteNewRecoveryRemoval(directory, name); err != nil {
				return fmt.Errorf("prepare recovered publication temporary removal for %s: %w", path, err)
			}
		}
		currentCandidate, err := directory.Lstat(name)
		if err != nil || !sameRegularIdentity(candidate, currentCandidate) {
			return errors.Join(
				fmt.Errorf("publication temporary changed identity during recovery for %s", path),
				err,
			)
		}
		if err := parent.validate(); err != nil {
			return fmt.Errorf("validate parent before recovering publication temporary for %s: %w", path, err)
		}
		if err := removeWriteNewTemporary(directory, name); err != nil {
			return fmt.Errorf("remove recovered publication temporary for %s: %w", path, err)
		}
		openedTarget, statErr := targetFile.Stat()
		currentTarget, lstatErr := directory.Lstat(target)
		if statErr != nil || lstatErr != nil ||
			!sameRegularIdentity(targetInfo, openedTarget) || !sameRegularIdentity(openedTarget, currentTarget) {
			return errors.Join(
				fmt.Errorf("publication target changed while recovering temporary for %s", path),
				statErr,
				lstatErr,
			)
		}
		if err := syncParentDir(directory); err != nil {
			return fmt.Errorf("sync recovered publication temporary removal for %s: %w", path, err)
		}
		if err := parent.validate(); err != nil {
			return fmt.Errorf("validate parent after recovering publication temporary for %s: %w", path, err)
		}
	}
	return nil
}

func isWriteNewTemporaryName(target, name string) bool {
	prefix := "." + target + "."
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tmp") {
		return false
	}
	randomHex := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".tmp")
	if len(randomHex) != 32 {
		return false
	}
	for _, value := range randomHex {
		if value < '0' || (value > '9' && value < 'a') || value > 'f' {
			return false
		}
	}
	return true
}

func cleanupWriteNewTemporary(directory *os.Root, name string) error {
	err := removeWriteNewTemporary(directory, name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func prepareNewFile(directory *os.Root, name string, data []byte, mode os.FileMode) (string, error) {
	file, path, err := createTemporary(directory, name)
	if err != nil {
		return "", fmt.Errorf("create temporary for %s: %w", name, err)
	}
	closed := false
	cleanup := func(primary error) (string, error) {
		var closeErr error
		if !closed {
			closeErr = closeWriteNewTemporary(file)
			closed = true
		}
		return "", errors.Join(primary, closeErr, cleanupWriteNewTemporary(directory, path))
	}
	if err := file.Chmod(mode); err != nil {
		return cleanup(fmt.Errorf("chmod temporary for %s: %w", name, err))
	}
	if _, err := file.Write(data); err != nil {
		return cleanup(fmt.Errorf("write temporary for %s: %w", name, err))
	}
	if err := syncWriteNewTemporary(file); err != nil {
		return cleanup(fmt.Errorf("sync temporary for %s: %w", name, err))
	}
	if err := closeWriteNewTemporary(file); err != nil {
		closed = true
		return "", errors.Join(
			fmt.Errorf("close temporary for %s: %w", name, err),
			cleanupWriteNewTemporary(directory, path),
		)
	}
	closed = true
	return path, nil
}
