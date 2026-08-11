//go:build windows

package actionstate

import (
	"errors"
	"fmt"
	"os"
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
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("private directory must be a directory")
	}
	return validatePrivateWindowsHandle(file, true)
}
