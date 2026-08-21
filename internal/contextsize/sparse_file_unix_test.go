//go:build !windows

package contextsize

import "os"

func truncateSparseFile(file *os.File, size int64) error {
	return file.Truncate(size)
}
