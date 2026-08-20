package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func openCreatedParent(path string) (*os.Root, os.FileInfo, string, error) {
	directoryPath := filepath.Dir(path)
	name := filepath.Base(path)
	before, err := os.Lstat(directoryPath)
	if err != nil {
		return nil, nil, "", err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, nil, "", fmt.Errorf("created artifact parent is not a real directory: %s", directoryPath)
	}
	parent, err := os.OpenRoot(directoryPath)
	if err != nil {
		return nil, nil, "", fmt.Errorf("open created artifact parent: %w", err)
	}
	opened, statErr := parent.Stat(".")
	after, lstatErr := os.Lstat(directoryPath)
	if statErr != nil || lstatErr != nil || !opened.IsDir() || after.Mode()&os.ModeSymlink != 0 ||
		!after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return nil, nil, "", errors.Join(
			fmt.Errorf("created artifact parent changed identity while opening"), statErr, lstatErr, parent.Close(),
		)
	}
	return parent, opened, name, nil
}

func validateCreatedParent(parent *os.Root, expected os.FileInfo, path string) error {
	if parent == nil || expected == nil {
		return fmt.Errorf("created artifact parent identity is unavailable: %s", path)
	}
	current, err := parent.Stat(".")
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() ||
		!os.SameFile(expected, current) || !os.SameFile(current, pathInfo) {
		return errors.New("created artifact parent was externally replaced")
	}
	return nil
}

func openCreatedFile(parent *os.Root, name, path string) (*os.File, os.FileInfo, error) {
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("created artifact is not a real regular file: %s", path)
	}
	file, err := parent.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, nil, err
	}
	opened, statErr := file.Stat()
	after, lstatErr := parent.Lstat(name)
	if statErr != nil || lstatErr != nil || !sameCreatedFile(opened, before) || !sameCreatedFile(opened, after) {
		return nil, nil, errors.Join(
			fmt.Errorf("created artifact changed identity while opening: %s", path), statErr, lstatErr, file.Close(),
		)
	}
	return file, opened, nil
}

func sameCreatedFile(left, right os.FileInfo) bool {
	return left != nil && right != nil && left.Mode()&os.ModeSymlink == 0 &&
		right.Mode()&os.ModeSymlink == 0 && left.Mode().IsRegular() && right.Mode().IsRegular() &&
		os.SameFile(left, right)
}

func hashOpenedCreatedFile(file *os.File, path string) (string, os.FileInfo, error) {
	before, err := file.Stat()
	if err != nil {
		return "", nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("created artifact descriptor is not regular: %s", path)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", nil, err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.LimitReader(file, maxBinaryBytes+1))
	after, statErr := file.Stat()
	if copyErr != nil {
		return "", nil, copyErr
	}
	if statErr != nil {
		return "", nil, statErr
	}
	if written > maxBinaryBytes || written != after.Size() || !sameCreatedSnapshot(before, after) {
		return "", nil, fmt.Errorf("created artifact changed or exceeds %d bytes: %s", maxBinaryBytes, path)
	}
	return hex.EncodeToString(hash.Sum(nil)), after, nil
}

func sameCreatedSnapshot(left, right os.FileInfo) bool {
	return sameCreatedFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime())
}

func validateCreatedTarget(record *createdRecord) error {
	if record.parent == nil || record.file == nil || record.info == nil {
		return errors.New("created artifact identity is incomplete")
	}
	if err := validateCreatedParent(record.parent, record.parentInfo, record.path); err != nil {
		return err
	}
	current, err := record.parent.Lstat(record.name)
	if err != nil {
		return err
	}
	opened, err := record.file.Stat()
	if err != nil {
		return err
	}
	if !sameCreatedFile(record.info, opened) || !sameCreatedFile(opened, current) {
		return errors.New("created artifact was externally replaced")
	}
	return nil
}

func (record *createdRecord) close() error {
	if record == nil {
		return nil
	}
	var err error
	if record.file != nil {
		err = errors.Join(err, record.file.Close())
		record.file = nil
	}
	if record.parent != nil {
		err = errors.Join(err, record.parent.Close())
		record.parent = nil
	}
	return err
}

func closeCreatedRecords(records []createdRecord) error {
	var err error
	for index := range records {
		err = errors.Join(err, records[index].close())
	}
	return err
}
