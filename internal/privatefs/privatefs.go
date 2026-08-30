// Package privatefs provides one identity-checked boundary for Reconc-owned
// private state. Every directory and lock returned by this package is opened,
// checked against its directory entry, secured while that descriptor binds its
// identity, and checked again before the descriptor is returned.
package privatefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/pathidentity"
)

const (
	PrivateDirectoryMode os.FileMode = 0o700
	PrivateFileMode      os.FileMode = 0o600
)

// EnsureDirectory creates missing components with private mode and platform
// security, identity-checks traversed existing directories, and validates the
// final directory's owner, mode, and platform security. It does not repair an
// existing final directory or change existing ancestors.
func EnsureDirectory(path string) error {
	return ensureDirectory(path, false)
}

// RepairDirectory creates missing components privately and repairs only the
// final existing directory while holding its opened descriptor before
// validating owner, mode, identity, and platform security. Existing ancestors are
// identity-checked but never chmodded or given a replacement ACL.
func RepairDirectory(path string) error {
	return ensureDirectory(path, true)
}

func ensureDirectory(path string, repairExisting bool) error {
	absolute, components, err := directoryComponents(path)
	if err != nil {
		return err
	}
	current := filepath.VolumeName(absolute) + string(filepath.Separator)
	for index, component := range components {
		current = filepath.Join(current, component)
		_, statErr := os.Lstat(current)
		created := errors.Is(statErr, os.ErrNotExist)
		final := index == len(components)-1
		secureMode := created || repairExisting && final
		repairFinal := repairExisting && final && !created
		if err := ensureDirectoryComponent(current, secureMode, secureMode || final, repairFinal); err != nil {
			return fmt.Errorf("secure private directory %s: %w", current, err)
		}
	}
	return nil
}

// ValidateDirectory validates one existing private directory and its opened
// descriptor. It rejects symlinks, irregular objects, unsafe permissions,
// unexpected ownership, and platform security descriptors.
func ValidateDirectory(path string) error {
	if _, _, err := directoryComponents(path); err != nil {
		return err
	}
	return validateDirectoryPath(path)
}

// ValidateFile validates an already opened private regular file. The caller
// retains ownership of the descriptor and must close it.
func ValidateFile(file *os.File, info os.FileInfo) error {
	return validatePrivateFile(file, info)
}

// SecureFile applies the platform-native private-file contract to an already
// opened regular file without changing or requiring a private parent directory.
// The path and descriptor identities are checked before and after permission
// changes so callers can safely use this for same-directory transaction files.
func SecureFile(file *os.File) error {
	return secureFile(file, false)
}

// SecureFileAllowLinks is SecureFile for content files whose atomic rotation
// can temporarily retain more than one hard-link name for the same identity.
func SecureFileAllowLinks(file *os.File) error {
	return secureFile(file, true)
}

func secureFile(file *os.File, allowLinks bool) error {
	if file == nil {
		return fmt.Errorf("private file handle is unavailable")
	}
	path := file.Name()
	before, statErr := file.Stat()
	current, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || before == nil || current == nil ||
		before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(before, current) {
		return errors.Join(fmt.Errorf("private file changed identity before securing"), statErr, lstatErr)
	}
	if err := file.Chmod(PrivateFileMode); err != nil {
		return fmt.Errorf("secure private file mode: %w", err)
	}
	if err := secureFileDescriptor(file); err != nil {
		return err
	}
	secured, statErr := file.Stat()
	current, lstatErr = os.Lstat(path)
	if statErr != nil || lstatErr != nil || secured == nil || current == nil ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(before, secured) || !os.SameFile(secured, current) {
		return errors.Join(fmt.Errorf("private file changed identity while securing"), statErr, lstatErr)
	}
	if allowLinks {
		return validatePrivateFileAllowLinks(file, secured)
	}
	return validatePrivateFile(file, secured)
}

// ValidateFileAllowLinks applies the private owner/mode/security contract to
// content files whose atomic rotation intentionally keeps a temporary hard
// link while both names are live.
func ValidateFileAllowLinks(file *os.File, info os.FileInfo) error {
	return validatePrivateFileAllowLinks(file, info)
}

// SecureDirectory runs EnsureDirectory, then reopens and revalidates the final
// private directory boundary. Existing security drift fails without repair.
func SecureDirectory(path string) error {
	if err := EnsureDirectory(path); err != nil {
		return err
	}
	return ValidateDirectory(path)
}

// OpenLock publishes or opens a private single-link lock file. The returned
// descriptor is ready for filelock.Lock/TryLock and remains owned by the
// caller until it is closed.
func OpenLock(path string) (*os.File, error) {
	return openPrivateFile(path, true, true)
}

// OpenExistingLock opens an already published private lock without creating it.
func OpenExistingLock(path string) (*os.File, error) {
	return openPrivateFile(path, false, true)
}

// OpenExistingLockReadOnly opens and validates a published private lock
// without creating, chmodding, repairing, or rewriting filesystem state.
func OpenExistingLockReadOnly(path string) (*os.File, error) {
	directory := filepath.Dir(filepath.Clean(path))
	if _, _, err := directoryComponents(directory); err != nil {
		return nil, fmt.Errorf("validate private lock directory: %w", err)
	}
	parent, err := openPrivateFileParent(path)
	if err != nil {
		return nil, fmt.Errorf("open private lock parent: %w", err)
	}
	file, err := parent.openReadOnly()
	closeErr := parent.close()
	if err != nil {
		return nil, errors.Join(err, closeErr)
	}
	if closeErr != nil {
		return nil, errors.Join(closeErr, file.Close())
	}
	return file, nil
}

// OpenExistingPrivateFile opens a private content file while allowing the
// hard-link aliases used by JSONL rotation.
func OpenExistingPrivateFile(path string) (*os.File, error) {
	return openPrivateFile(path, false, false)
}

// WritePrivateIfChanged uses the descriptor-safe atomic publisher, then
// verifies the resulting private file through an opened descriptor.
func WritePrivateIfChanged(path string, data []byte, mode os.FileMode) (atomicfile.PublicationResult, error) {
	result, err := atomicfile.WritePrivateIfChanged(path, data, mode)
	if err != nil {
		return result, err
	}
	file, err := openPrivateFile(path, false, true)
	if err != nil {
		if result.Outcome == atomicfile.PublicationDurablyPublished {
			result.Outcome = atomicfile.PublicationPublishedUncertain
		}
		return result, fmt.Errorf("validate private publication %s: %w", path, err)
	}
	closeErr := file.Close()
	if closeErr != nil && result.Outcome == atomicfile.PublicationDurablyPublished {
		result.Outcome = atomicfile.PublicationPublishedUncertain
	}
	return result, closeErr
}

func directoryComponents(path string) (string, []string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve private directory: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if info, statErr := os.Lstat(absolute); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("private directory must not be a symlink: %s", path)
	}
	if resolved, resolveErr := pathidentity.ResolveProspective(absolute); resolveErr == nil {
		absolute = filepath.Clean(resolved)
	}
	volume := filepath.VolumeName(absolute)
	root := volume + string(filepath.Separator)
	if absolute == root {
		return "", nil, fmt.Errorf("private directory must not be a filesystem root")
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == "." {
		return "", nil, fmt.Errorf("private directory has no usable components: %s", path)
	}
	components := splitComponents(relative)
	if len(components) == 0 {
		return "", nil, fmt.Errorf("private directory has no usable components: %s", path)
	}
	return absolute, components, nil
}

func splitComponents(path string) []string {
	parts := []string{}
	for path != "." && path != "" {
		directory, name := filepath.Split(path)
		name = filepath.Clean(name)
		if name != "" && name != "." && name != string(filepath.Separator) {
			parts = append(parts, name)
		}
		trimmed := filepath.Clean(directory)
		if trimmed == path {
			break
		}
		path = trimmed
	}
	slices.Reverse(parts)
	return parts
}

func ensureDirectoryComponent(path string, secureMode, validateSecurity, repairExisting bool) error {
	if err := os.Mkdir(path, PrivateDirectoryMode.Perm()); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create directory: %w", err)
	}
	file, info, err := openDirectory(path)
	if err != nil {
		return err
	}
	defer file.Close()
	secured, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect secured directory: %w", err)
	}
	if err := validateDirectoryIdentity(path, info, secured); err != nil {
		return err
	}
	if err := validateDirectoryEntry(path, secured); err != nil {
		return err
	}
	if repairExisting {
		if err := validateDirectorySecurity(file, secured); err == nil {
			return nil
		}
	}
	if secureMode {
		if err := file.Chmod(PrivateDirectoryMode); err != nil {
			return fmt.Errorf("secure mode: %w", err)
		}
		if err := secureDirectoryDescriptor(file); err != nil {
			return err
		}
		secured, err = file.Stat()
		if err != nil {
			return fmt.Errorf("inspect secured directory: %w", err)
		}
	}
	if validateSecurity {
		return validateDirectoryDescriptor(path, file, info, secured)
	}
	return nil
}

func openDirectory(path string) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, nil, fmt.Errorf("private path must be a non-symlink directory")
	}
	file, err := openDirectoryDescriptor(path)
	if err != nil {
		return nil, nil, err
	}
	opened, statErr := file.Stat()
	after, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || !opened.IsDir() || after.Mode()&os.ModeSymlink != 0 ||
		!after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return nil, nil, errors.Join(fmt.Errorf("private directory changed identity while opening"), statErr, lstatErr, file.Close())
	}
	return file, opened, nil
}

func validateDirectoryPath(path string) error {
	file, info, err := openDirectory(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return validateDirectoryDescriptor(path, file, info, info)
}

func validateDirectoryDescriptor(path string, file *os.File, before, after os.FileInfo) error {
	if err := validateDirectoryIdentity(path, before, after); err != nil {
		return err
	}
	if err := validateDirectoryEntry(path, after); err != nil {
		return err
	}
	return validateDirectorySecurity(file, after)
}

func validateDirectoryEntry(path string, opened os.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() ||
		opened == nil || !os.SameFile(opened, current) {
		return errors.Join(fmt.Errorf("private directory %s changed identity", path), err)
	}
	return nil
}

func validateDirectoryIdentity(path string, before, after os.FileInfo) error {
	if before == nil || after == nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() ||
		after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(before, after) {
		return fmt.Errorf("private directory %s changed identity", path)
	}
	return nil
}

func openPrivateFile(path string, create, singleLink bool) (*os.File, error) {
	return openPrivateFileWithHooks(path, create, singleLink, privateFileOpenHooks{})
}
