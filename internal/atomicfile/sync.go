package atomicfile

import (
	"io/fs"
	"os"
)

// SyncDirectory commits directory-entry changes through an already bound root.
// Windows keeps the strongest supported os.Root boundary and returns success
// without claiming an unsupported FlushFileBuffers call on a read-only handle.
func SyncDirectory(directory *os.Root) error {
	if directory == nil {
		return fs.ErrInvalid
	}
	return syncParentDir(directory)
}
