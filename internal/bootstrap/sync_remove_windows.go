//go:build windows

package bootstrap

import "os"

func syncRemovalParent(parent *os.Root, expected os.FileInfo, path string) error {
	// Windows exposes neither directory FlushFileBuffers nor delete write-through
	// through os.Root. The shared contract still revalidates the bound parent
	// after handle-relative deletion; rollback replacement is write-through.
	return syncMutatedRemovalParent(parent, expected, path)
}
