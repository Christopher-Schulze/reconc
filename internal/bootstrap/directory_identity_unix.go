//go:build !windows

package bootstrap

import (
	"fmt"
	"os"
)

type directoryIdentity struct {
	handle *os.File
}

func captureDirectoryIdentity(path string) (*directoryIdentity, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("created path is not a real directory: %s", path)
	}
	handle, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := handle.Stat()
	if err != nil {
		handle.Close()
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil {
		handle.Close()
		return nil, err
	}
	if !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		handle.Close()
		return nil, fmt.Errorf("created directory changed identity while inspecting: %s", path)
	}
	return &directoryIdentity{handle: handle}, nil
}

func sameDirectoryIdentity(identity *directoryIdentity, current os.FileInfo) bool {
	if identity == nil || identity.handle == nil || current == nil {
		return false
	}
	expected, err := identity.handle.Stat()
	return err == nil && os.SameFile(current, expected)
}

// closeDirectoryIdentity is idempotent: it closes the handle once and nils it
// so a second call (the rollback defer and the caller defer share the same
// slice backing array) is a harmless no-op instead of a double-close.
func closeDirectoryIdentity(identity *directoryIdentity) {
	if identity != nil && identity.handle != nil {
		_ = identity.handle.Close()
		identity.handle = nil
	}
}
