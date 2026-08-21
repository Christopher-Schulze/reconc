//go:build windows

package atomicfile

import "os"

func reconcileMode(directory *os.Root, name string, _ *os.File, current, requested os.FileMode) (bool, error) {
	// Windows does not represent POSIX permission bits. os.Chmod can only
	// toggle the read-only attribute, so compare only its owner-write proxy.
	currentWritable := current.Perm()&0o200 != 0
	requestedWritable := requested.Perm()&0o200 != 0
	if currentWritable == requestedWritable {
		return false, nil
	}
	// The current descriptor was opened read-only for identity-safe comparison
	// and therefore lacks FILE_WRITE_ATTRIBUTES. Root.Chmod opens the same
	// no-follow name relative to the bound parent with that exact right. The
	// caller revalidates the target identity immediately after this call.
	if err := directory.Chmod(name, requested); err != nil {
		return false, err
	}
	return true, nil
}
