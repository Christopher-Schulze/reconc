package hooks

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const maxManagedArtifactBytes = 4 << 20

// readManagedArtifact reads one hook/config artifact without following a
// symlink or accepting unbounded input. Repository hook files are untrusted
// until this identity check succeeds.
func readManagedArtifact(path string) ([]byte, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !pathInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if pathInfo.Size() > maxManagedArtifactBytes {
		return nil, fmt.Errorf("%s exceeds the %d-byte managed-artifact limit", path, maxManagedArtifactBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return nil, fmt.Errorf("%s changed while opening", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxManagedArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxManagedArtifactBytes {
		return nil, fmt.Errorf("%s exceeds the %d-byte managed-artifact limit", path, maxManagedArtifactBytes)
	}
	return data, nil
}

func syncManagedArtifactParent(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}
