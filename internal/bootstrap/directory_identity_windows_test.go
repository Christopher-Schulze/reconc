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
	after, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) {
		return nil, fmt.Errorf("created directory changed identity while inspecting: %s", path)
	}
	return &directoryIdentity{info: after}, nil
}
