package atomicfile

import (
	"io/fs"
	"os"
)

var syncParentDir = syncParentDirImpl

var syncFile = (*os.File).Sync

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

// SyncOrdered syncs file with the barrier of the ordered publications: on
// macOS F_BARRIERFSYNC keeps write order without flushing the device cache,
// and elsewhere the file syncs fully. A later full sync on the same file
// system makes the write durable.
func SyncOrdered(file *os.File) error {
	if file == nil {
		return fs.ErrInvalid
	}
	return syncFileOrdered(file)
}

// SyncDirectory commits directory-entry changes through an already bound root.
// Windows keeps the strongest supported os.Root boundary and returns success
// without claiming an unsupported FlushFileBuffers call on a read-only handle.
func SyncDirectory(directory *os.Root) error {
	if directory == nil {
		return fs.ErrInvalid
	}
	return syncParentDir(directory)
}
