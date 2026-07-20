//go:build windows

package atomicfile

import "os"

func reconcileMode(string, os.FileMode, os.FileMode) (bool, error) {
	// Windows does not represent POSIX permission bits. os.Chmod can only
	// toggle the read-only attribute, so treating 0600 and 0644 as different
	// would republish identical files on every write-on-change call.
	return false, nil
}
