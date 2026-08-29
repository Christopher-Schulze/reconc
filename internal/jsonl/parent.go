package jsonl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/atomicfile"
)

type jsonlParent struct {
	root           *os.Root
	info           os.FileInfo
	directory      string
	name           string
	lockValidation func() error
}

var jsonlDirectorySync = atomicfile.SyncDirectory

func openJSONLParent(path string) (*jsonlParent, error) {
	directory := filepath.Dir(path)
	before, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, fmt.Errorf("jsonl parent must be a non-symlink directory: %s", directory)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	opened, statErr := root.Stat(".")
	after, lstatErr := os.Lstat(directory)
	if statErr != nil || lstatErr != nil || !opened.IsDir() || after.Mode()&os.ModeSymlink != 0 ||
		!after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return nil, errors.Join(
			fmt.Errorf("jsonl parent changed identity while opening: %s", directory),
			statErr,
			lstatErr,
			root.Close(),
		)
	}
	return &jsonlParent{root: root, info: opened, directory: directory, name: filepath.Base(path)}, nil
}

func openJSONLParentWithLayout(path string, layout Layout) (*jsonlParent, error) {
	parent, err := openJSONLParent(path)
	if err != nil {
		return nil, err
	}
	parent.lockValidation = layout.validateLockLease
	return parent, nil
}

func (parent *jsonlParent) validate() error {
	opened, statErr := parent.root.Stat(".")
	current, lstatErr := os.Lstat(parent.directory)
	if statErr != nil || lstatErr != nil || !opened.IsDir() || current.Mode()&os.ModeSymlink != 0 ||
		!current.IsDir() || !os.SameFile(parent.info, opened) || !os.SameFile(opened, current) {
		return errors.Join(
			fmt.Errorf("jsonl parent changed identity: %s", parent.directory),
			statErr,
			lstatErr,
		)
	}
	return nil
}

func (parent *jsonlParent) validateLockLease() error {
	if parent == nil || parent.lockValidation == nil {
		return nil
	}
	return parent.lockValidation()
}

func (parent *jsonlParent) syncMutation() error {
	if err := parent.validateLockLease(); err != nil {
		return err
	}
	syncErr := jsonlDirectorySync(parent.root)
	return errors.Join(syncErr, parent.validateLockLease(), parent.validate())
}

func (parent *jsonlParent) remove(name string) (bool, error) {
	if err := parent.validateLockLease(); err != nil {
		return false, err
	}
	if err := parent.validate(); err != nil {
		return false, err
	}
	if err := parent.root.Remove(name); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, parent.syncMutation()
}

func (parent *jsonlParent) rename(source, destination string) (bool, error) {
	if err := parent.validateLockLease(); err != nil {
		return false, err
	}
	if err := parent.validate(); err != nil {
		return false, err
	}
	if err := parent.root.Rename(source, destination); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, parent.syncMutation()
}

func (parent *jsonlParent) close() error {
	if parent == nil || parent.root == nil {
		return nil
	}
	err := parent.root.Close()
	parent.root = nil
	return err
}

func removeJSONLPathWithLayout(path string, layout Layout) (resultErr error) {
	parent, err := openJSONLParentWithLayout(path, layout)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, parent.close()) }()
	_, err = parent.remove(parent.name)
	return err
}

func linkJSONLPathWithLayout(source, destination string, layout Layout) (resultErr error) {
	if filepath.Dir(source) != filepath.Dir(destination) {
		return errors.New("jsonl link paths must share one parent")
	}
	parent, err := openJSONLParentWithLayout(destination, layout)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, parent.close()) }()
	if err := parent.validate(); err != nil {
		return err
	}
	if err := parent.validateLockLease(); err != nil {
		return err
	}
	if err := parent.root.Link(filepath.Base(source), parent.name); err != nil {
		return err
	}
	return parent.syncMutation()
}
