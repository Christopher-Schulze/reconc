//go:build !windows

package bootstrap

import (
	"fmt"
	"os"
)

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
