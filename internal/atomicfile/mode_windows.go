//go:build windows

package atomicfile

import "os"

func reconcileMode(path string, current os.FileMode, requested os.FileMode) (bool, error) {
	// Windows does not represent POSIX permission bits. os.Chmod can only
	// toggle the read-only attribute, so compare only its owner-write proxy.
	currentWritable := current.Perm()&0o200 != 0
	requestedWritable := requested.Perm()&0o200 != 0
	if currentWritable == requestedWritable {
		return false, nil
	}
	if err := os.Chmod(path, requested); err != nil {
		return false, err
	}
	return true, nil
}
