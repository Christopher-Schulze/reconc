//go:build !windows

package atomicfile

import "os"

func replaceFile(directory *os.Root, source, destination string) error {
	return directory.Rename(source, destination)
}
