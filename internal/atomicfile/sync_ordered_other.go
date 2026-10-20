//go:build !darwin

package atomicfile

import "os"

// Outside macOS File.Sync is the only file sync primitive and already
// persists the file, so ordered publication syncs fully.
var syncParentDirOrdered = syncParentDirImpl

func syncFileOrdered(file *os.File) error {
	return file.Sync()
}
