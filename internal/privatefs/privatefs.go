// Package privatefs provides one identity-checked boundary for Reconc-owned
// private state. Every directory and lock returned by this package is opened,
// checked against its directory entry, secured through the descriptor, and
// checked again before the descriptor is returned.
package privatefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/pathidentity"
)

const (
	PrivateDirectoryMode os.FileMode = 0o700
	PrivateFileMode      os.FileMode = 0o600
)

// EnsureDirectory creates every missing component and validates the complete
// directory boundary. Existing components are reconciled to private mode.
func EnsureDirectory(path string) error {
	return ensureDirectory(path, false)
}

// RepairDirectory creates the private directory boundary and reconciles the
// final existing directory to private mode through its opened descriptor. It
// is for legacy state roots that predate the private-mode contract.
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
		secureMode := created || repairExisting && index == len(components)-1
		if err := ensureDirectoryComponent(current, secureMode, secureMode || index == len(components)-1); err != nil {
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

// SecureDirectory creates or secures a private directory and validates it.
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
	return openPrivateFile(path, true)
}

// OpenExistingLock opens an already published private lock without creating it.
func OpenExistingLock(path string) (*os.File, error) {
	return openPrivateFile(path, false)
}

// WritePrivateIfChanged uses the descriptor-safe atomic publisher, then
// verifies the resulting private file through an opened descriptor.
func WritePrivateIfChanged(path string, data []byte, mode os.FileMode) (bool, error) {
	changed, err := atomicfile.WritePrivateIfChanged(path, data, mode)
	if err != nil {
		return false, err
	}
	file, err := openPrivateFile(path, false)
	if err != nil {
		return false, fmt.Errorf("validate private publication %s: %w", path, err)
	}
	closeErr := file.Close()
	return changed, closeErr
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
	for _, part := range filepath.SplitList(path) {
		_ = part
	}
	for path != "." && path != "" {
		directory, name := filepath.Split(path)
		name = filepath.Clean(name)
		if name != "" && name != "." && name != string(filepath.Separator) {
			parts = append([]string{name}, parts...)
		}
		trimmed := filepath.Clean(directory)
		if trimmed == path {
			break
		}
		path = trimmed
	}
	return parts
}

func ensureDirectoryComponent(path string, secureMode, validateSecurity bool) error {
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
	if err := validateDirectoryIdentity(path, info, secured); err != nil {
		return err
	}
	if validateSecurity {
		if err := validateDirectorySecurity(file, secured); err != nil {
			return err
		}
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
	file, err := os.Open(path)
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
	return validateDirectorySecurity(file, after)
}

func validateDirectoryIdentity(path string, before, after os.FileInfo) error {
	if before == nil || after == nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() ||
		after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(before, after) {
		return fmt.Errorf("private directory %s changed identity", path)
	}
	return nil
}

func openPrivateFile(path string, create bool) (*os.File, error) {
	parent := filepath.Dir(filepath.Clean(path))
	if err := RepairDirectory(parent); err != nil {
		return nil, fmt.Errorf("secure private lock directory: %w", err)
	}
	if err := ValidateDirectory(parent); err != nil {
		return nil, fmt.Errorf("validate private lock directory: %w", err)
	}
	before, lstatErr := os.Lstat(path)
	if lstatErr != nil && !errors.Is(lstatErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect private lock: %w", lstatErr)
	}
	if !create && errors.Is(lstatErr, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	file, err := os.OpenFile(path, flags, PrivateFileMode)
	if err != nil {
		return nil, fmt.Errorf("open private lock: %w", err)
	}
	opened, statErr := file.Stat()
	current, currentErr := os.Lstat(path)
	if statErr != nil || currentErr != nil || !opened.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(opened, current) || before != nil && !os.SameFile(before, current) && !create {
		return nil, errors.Join(fmt.Errorf("private lock changed identity while opening"), statErr, currentErr, file.Close())
	}
	if err := validatePrivateLinkCount(opened); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if err := file.Chmod(PrivateFileMode); err != nil {
		return nil, errors.Join(fmt.Errorf("secure private lock mode: %w", err), file.Close())
	}
	if err := secureFileDescriptor(file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	secured, err := file.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("inspect secured private lock: %w", err), file.Close())
	}
	if err := validatePrivateFile(file, secured); err != nil {
		return nil, errors.Join(fmt.Errorf("validate private lock: %w", err), file.Close())
	}
	current, err = os.Lstat(path)
	if err != nil || !os.SameFile(secured, current) {
		return nil, errors.Join(fmt.Errorf("private lock changed identity after securing"), err, file.Close())
	}
	return file, nil
}
