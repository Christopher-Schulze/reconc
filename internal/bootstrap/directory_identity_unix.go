//go:build !windows

package bootstrap

import (
	"fmt"
	"os"
)

type directoryIdentity struct {
	handle *os.File
}

func captureDirectoryIdentity(path string) (directoryIdentity, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return directoryIdentity{}, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return directoryIdentity{}, fmt.Errorf("created path is not a real directory: %s", path)
	}
	handle, err := os.Open(path)
	if err != nil {
		return directoryIdentity{}, err
	}
	opened, err := handle.Stat()
	if err != nil {
		handle.Close()
		return directoryIdentity{}, err
	}
	after, err := os.Lstat(path)
	if err != nil {
		handle.Close()
		return directoryIdentity{}, err
	}
	if !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		handle.Close()
		return directoryIdentity{}, fmt.Errorf("created directory changed identity while inspecting: %s", path)
	}
	return directoryIdentity{handle: handle}, nil
}

func sameDirectoryIdentity(identity directoryIdentity, current os.FileInfo) bool {
	if identity.handle == nil || current == nil {
		return false
	}
	expected, err := identity.handle.Stat()
	return err == nil && os.SameFile(current, expected)
}

func closeDirectoryIdentity(identity directoryIdentity) {
	if identity.handle != nil {
		_ = identity.handle.Close()
	}
}
