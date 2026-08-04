//go:build windows

package bootstrap

import (
	"fmt"
	"os"
)

type directoryIdentity struct {
	info os.FileInfo
}

func captureDirectoryIdentity(path string) (*directoryIdentity, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("created path is not a real directory: %s", path)
	}
	after, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) {
		return nil, fmt.Errorf("created directory changed identity while inspecting: %s", path)
	}
	return &directoryIdentity{info: after}, nil
}

func sameDirectoryIdentity(identity *directoryIdentity, current os.FileInfo) bool {
	return identity != nil && identity.info != nil && current != nil && os.SameFile(current, identity.info)
}

func closeDirectoryIdentity(identity *directoryIdentity) {
	if identity != nil {
		identity.info = nil
	}
}
