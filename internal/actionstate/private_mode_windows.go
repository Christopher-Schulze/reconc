//go:build windows

package actionstate

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func validatePrivateFileMode(mode os.FileMode) error {
	if !mode.IsRegular() {
		return fmt.Errorf("private file must be regular")
	}
	return nil
}

func validatePrivateFile(file *os.File, info os.FileInfo) error {
	if err := validatePrivateFileMode(info.Mode()); err != nil {
		return err
	}
	return validatePrivateWindowsHandle(file, false)
}

func validatePrivateDirectory(path string) (resultErr error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode private Windows directory path: %w", err)
	}
	attributes, err := windows.GetFileAttributes(pathPointer)
	if err != nil {
		return fmt.Errorf("inspect private Windows directory attributes: %w", err)
	}
	if err := validatePrivateWindowsDirectoryAttributes(attributes); err != nil {
		return err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return fmt.Errorf("private directory must be a non-symlink directory")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	opened, statErr := file.Stat()
	current, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || !opened.IsDir() ||
		current.Mode()&os.ModeSymlink != 0 || !current.IsDir() ||
		!os.SameFile(before, opened) || !os.SameFile(opened, current) {
		if statErr == nil && lstatErr == nil {
			statErr = fmt.Errorf("private Windows directory changed identity while opening")
		}
		return errors.Join(statErr, lstatErr)
	}
	return validatePrivateWindowsHandle(file, true)
}

func validatePrivateWindowsDirectoryAttributes(attributes uint32) error {
	if attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("private directory must be a directory")
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("private directory must not be a Windows reparse point")
	}
	return nil
}
