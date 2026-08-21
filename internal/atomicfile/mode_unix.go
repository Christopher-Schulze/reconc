//go:build !windows

package atomicfile

import "os"

func reconcileMode(_ *os.Root, _ string, file *os.File, current, requested os.FileMode) (bool, error) {
	if current.Perm() == requested.Perm() {
		return false, nil
	}
	if err := file.Chmod(requested); err != nil {
		return false, err
	}
	return true, nil
}
