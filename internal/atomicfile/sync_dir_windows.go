//go:build windows

package atomicfile

func syncParentDir(string) error {
	// MoveFileExW is called with MOVEFILE_WRITE_THROUGH in replaceFile.
	return nil
}
