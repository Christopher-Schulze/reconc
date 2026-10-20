package atomicfile

import (
	"io/fs"
	"os"
)

var syncParentDir = syncParentDirImpl

// publicationDurability selects how far a publication is synced to storage.
type publicationDurability uint8

const (
	// durabilityFull flushes the payload and the directory entry to media.
	durabilityFull publicationDurability = iota
	// durabilityOrdered persists them in issue order; see
	// WritePrivateOrderedIfChanged.
	durabilityOrdered
	// durabilityNone keeps only atomicity; see WritePrivateCacheIfChanged.
	durabilityNone
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
