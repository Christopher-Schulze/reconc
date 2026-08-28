package privatefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type privateFileOpenHooks struct {
	afterInspect func(missing bool) error
}

type privateFileParent struct {
	root      *os.Root
	info      os.FileInfo
	directory string
	name      string
}

func openPrivateFileWithHooks(path string, create, singleLink bool, hooks privateFileOpenHooks) (*os.File, error) {
	directory := filepath.Dir(filepath.Clean(path))
	if err := RepairDirectory(directory); err != nil {
		return nil, fmt.Errorf("secure private lock directory: %w", err)
	}
	if err := ValidateDirectory(directory); err != nil {
		return nil, fmt.Errorf("validate private lock directory: %w", err)
	}
	parent, err := openPrivateFileParent(path)
	if err != nil {
		return nil, err
	}
	file, err := parent.openAndSecure(create, singleLink, hooks)
	closeErr := parent.close()
	if err != nil {
		return nil, errors.Join(err, closeErr)
	}
	if closeErr != nil {
		return nil, errors.Join(closeErr, file.Close())
	}
	return file, nil
}

func openPrivateFileParent(path string) (*privateFileParent, error) {
	directory := filepath.Dir(filepath.Clean(path))
	before, err := os.Lstat(directory)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, errors.Join(fmt.Errorf("private lock parent must be a non-symlink directory"), err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	opened, statErr := root.Stat(".")
	after, lstatErr := os.Lstat(directory)
	if statErr != nil || lstatErr != nil || !opened.IsDir() || after.Mode()&os.ModeSymlink != 0 ||
		!after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return nil, errors.Join(fmt.Errorf("private lock parent changed identity while opening"), statErr, lstatErr, root.Close())
	}
	return &privateFileParent{root: root, info: opened, directory: directory, name: filepath.Base(path)}, nil
}

func (parent *privateFileParent) openAndSecure(create, singleLink bool, hooks privateFileOpenHooks) (*os.File, error) {
	file, before, err := parent.openCandidate(create, hooks)
	if err != nil {
		return nil, err
	}
	opened, err := parent.validateOpened(file, before)
	if err != nil {
		if before == nil {
			err = errors.Join(err, parent.removeCreated(file))
		}
		return nil, errors.Join(err, file.Close())
	}
	if singleLink {
		if err := validatePrivateLinkCount(file, opened); err != nil {
			return nil, errors.Join(err, file.Close())
		}
	}
	return parent.secureOpened(file, opened, singleLink)
}

func (parent *privateFileParent) removeCreated(file *os.File) error {
	opened, statErr := file.Stat()
	current, lstatErr := parent.root.Lstat(parent.name)
	if statErr != nil || lstatErr != nil || opened == nil || current == nil ||
		!opened.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 ||
		!current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return errors.Join(fmt.Errorf("refuse to remove changed private lock creation"), statErr, lstatErr)
	}
	if err := parent.root.Remove(parent.name); err != nil {
		return fmt.Errorf("remove rejected private lock creation: %w", err)
	}
	return nil
}

func (parent *privateFileParent) openCandidate(create bool, hooks privateFileOpenHooks) (*os.File, os.FileInfo, error) {
	before, missing, err := parent.inspectCandidate()
	if err != nil {
		return nil, nil, err
	}
	if hooks.afterInspect != nil {
		if err := hooks.afterInspect(missing); err != nil {
			return nil, nil, err
		}
	}
	if missing {
		if !create {
			return nil, nil, os.ErrNotExist
		}
		file, createErr := parent.root.OpenFile(parent.name, os.O_CREATE|os.O_EXCL|os.O_RDWR, PrivateFileMode)
		if createErr == nil {
			return file, nil, nil
		}
		if !errors.Is(createErr, os.ErrExist) {
			return nil, nil, fmt.Errorf("create private lock: %w", createErr)
		}
		before, missing, err = parent.inspectCandidate()
		if err != nil || missing {
			return nil, nil, errors.Join(fmt.Errorf("private lock changed identity during creation"), err)
		}
	}
	file, err := parent.root.OpenFile(parent.name, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open existing private lock: %w", err)
	}
	return file, before, nil
}

func (parent *privateFileParent) inspectCandidate() (os.FileInfo, bool, error) {
	info, err := parent.root.Lstat(parent.name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect private lock: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("private lock must be a non-symlink regular file")
	}
	return info, false, nil
}

func (parent *privateFileParent) validateOpened(file *os.File, before os.FileInfo) (os.FileInfo, error) {
	opened, statErr := file.Stat()
	current, lstatErr := parent.root.Lstat(parent.name)
	if statErr != nil || lstatErr != nil || opened == nil || !opened.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(opened, current) || before != nil && !os.SameFile(before, opened) {
		return nil, errors.Join(fmt.Errorf("private lock changed identity while opening"), statErr, lstatErr)
	}
	if err := parent.validate(); err != nil {
		return nil, err
	}
	return opened, nil
}

func (parent *privateFileParent) secureOpened(file *os.File, opened os.FileInfo, singleLink bool) (*os.File, error) {
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
	validate := validatePrivateFile
	if !singleLink {
		validate = validatePrivateFileAllowLinks
	}
	if err := validate(file, secured); err != nil {
		return nil, errors.Join(fmt.Errorf("validate private lock: %w", err), file.Close())
	}
	current, lstatErr := parent.root.Lstat(parent.name)
	if lstatErr != nil || !os.SameFile(opened, secured) || !os.SameFile(secured, current) {
		return nil, errors.Join(fmt.Errorf("private lock changed identity after securing"), lstatErr, file.Close())
	}
	if err := parent.validate(); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func (parent *privateFileParent) validate() error {
	opened, statErr := parent.root.Stat(".")
	current, lstatErr := os.Lstat(parent.directory)
	if statErr != nil || lstatErr != nil || !opened.IsDir() || current.Mode()&os.ModeSymlink != 0 ||
		!current.IsDir() || !os.SameFile(parent.info, opened) || !os.SameFile(opened, current) {
		return errors.Join(fmt.Errorf("private lock parent changed identity"), statErr, lstatErr)
	}
	return nil
}

func (parent *privateFileParent) close() error {
	if parent == nil || parent.root == nil {
		return nil
	}
	err := parent.root.Close()
	parent.root = nil
	return err
}
