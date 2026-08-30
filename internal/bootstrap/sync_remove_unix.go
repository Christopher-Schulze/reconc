//go:build !windows

package bootstrap

import "os"

func syncRemovalParent(parent *os.Root, expected os.FileInfo, path string) error {
	return syncMutatedRemovalParent(parent, expected, path)
}
