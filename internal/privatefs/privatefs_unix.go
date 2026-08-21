//go:build !windows

package privatefs

import (
	"fmt"
	"os"
	"syscall"
)

func validatePrivateFile(file *os.File, info os.FileInfo) error {
	if err := validatePrivateFileAllowLinks(file, info); err != nil {
		return err
	}
	return validatePrivateLinkCount(info)
}

func validatePrivateFileAllowLinks(file *os.File, info os.FileInfo) error {
	if file == nil || info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != PrivateFileMode.Perm() {
		return fmt.Errorf("private file must be a regular file with mode 0600")
	}
	if err := validateCurrentUserOwner(info); err != nil {
		return err
	}
	return validatePrivateFileACL(file)
}

func validatePrivateLinkCount(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil || stat.Nlink != 1 {
		return fmt.Errorf("private file must have exactly one directory link")
	}
	return nil
}

func validateDirectorySecurity(file *os.File, info os.FileInfo) error {
	if info == nil || info.Mode().Perm() != PrivateDirectoryMode.Perm() {
		return fmt.Errorf("private directory must have mode 0700")
	}
	if err := validateCurrentUserOwner(info); err != nil {
		return err
	}
	return validatePrivateDirectoryACL(file)
}

func secureDirectoryDescriptor(*os.File) error { return nil }
func secureFileDescriptor(*os.File) error      { return nil }

func openDirectoryDescriptor(path string) (*os.File, error) {
	return os.Open(path)
}

func openPrivateFileDescriptor(path string, create bool) (*os.File, error) {
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	return os.OpenFile(path, flags, PrivateFileMode)
}

func validateCurrentUserOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil || uint64(stat.Uid) != uint64(os.Geteuid()) {
		return fmt.Errorf("private filesystem object must be owned by the current user")
	}
	return nil
}
