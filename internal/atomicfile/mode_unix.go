//go:build !windows

package atomicfile

import "os"

func reconcileMode(path string, current, requested os.FileMode) (bool, error) {
	if current.Perm() == requested.Perm() {
		return false, nil
	}
	if err := os.Chmod(path, requested); err != nil {
		return false, err
	}
	return true, nil
}
