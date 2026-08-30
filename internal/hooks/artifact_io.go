package hooks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
)

const maxManagedArtifactBytes = 4 << 20

// readManagedArtifact reads one hook/config artifact without following a
// symlink or accepting unbounded input. Repository hook files are untrusted
// until this identity check succeeds.
func readManagedArtifact(path string) ([]byte, error) {
	snapshot, err := readManagedArtifactSnapshot(path)
	if err != nil {
		return nil, err
	}
	if !snapshot.exists {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	return snapshot.body, nil
}

type managedArtifactSnapshot struct {
	body   []byte
	info   os.FileInfo
	exists bool
}

func readManagedArtifactSnapshot(path string) (managedArtifactSnapshot, error) {
	pathInfo, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return managedArtifactSnapshot{}, nil
	}
	if err != nil {
		return managedArtifactSnapshot{}, err
	}
	if !pathInfo.Mode().IsRegular() {
		return managedArtifactSnapshot{}, fmt.Errorf("%s is not a regular file", path)
	}
	if pathInfo.Size() > maxManagedArtifactBytes {
		return managedArtifactSnapshot{}, fmt.Errorf("%s exceeds the %d-byte managed-artifact limit", path, maxManagedArtifactBytes)
	}
	body, info, err := boundedio.ReadRegularFileSnapshot(path, maxManagedArtifactBytes)
	if err != nil {
		return managedArtifactSnapshot{}, err
	}
	return managedArtifactSnapshot{body: body, info: info, exists: true}, nil
}

func (snapshot managedArtifactSnapshot) expectedCurrent() atomicfile.ExpectedCurrent {
	return atomicfile.ExpectedCurrent{
		Data:   snapshot.body,
		Info:   snapshot.info,
		Exists: snapshot.exists,
	}
}

func managedArtifactPublicationMode(current os.FileMode, exists, executable bool) os.FileMode {
	if !exists {
		if executable {
			return 0o755
		}
		return 0o644
	}
	mode := current.Perm()
	if executable {
		mode |= 0o100
	}
	return mode
}

func publishManagedArtifact(path string, content []byte, mode os.FileMode, snapshot managedArtifactSnapshot) (string, error) {
	action := "created"
	if snapshot.exists {
		action = "updated"
	}
	result, err := atomicfile.WriteIfCurrent(path, content, mode, snapshot.expectedCurrent())
	if err != nil {
		if result.Changed {
			return action, err
		}
		return "", err
	}
	if !result.Changed {
		return "unchanged", nil
	}
	return action, nil
}

func syncManagedArtifactParent(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
