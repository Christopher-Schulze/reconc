//go:build !windows

package actionstate

import (
	"fmt"
	"os"
	"syscall"
)

func validatePrivateFileMode(mode os.FileMode) error {
	if !mode.IsRegular() || mode.Perm() != 0o600 {
		return fmt.Errorf("private file must be a regular file with mode 0600")
	}
	return nil
}

func validatePrivateFile(file *os.File, info os.FileInfo) error {
	if err := validatePrivateFileMode(info.Mode()); err != nil {
		return err
	}
	if err := validateCurrentUserOwner(info); err != nil {
		return err
	}
	return validatePrivateFileACL(file)
}

func validatePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("private directory must be a non-symlink directory with mode 0700")
	}
	if err := validateCurrentUserOwner(info); err != nil {
		return err
	}
	return validatePrivatePathACL(path)
}

func validateCurrentUserOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil || uint64(stat.Uid) != uint64(os.Geteuid()) {
		return fmt.Errorf("private filesystem object must be owned by the current user")
	}
	return nil
}
