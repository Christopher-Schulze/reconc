//go:build windows

package atomicfile

import "os"

func syncParentDir(*os.Root) error {
	// Windows exposes no supported directory fsync through os.Root.
	// File.Sync maps to FlushFileBuffers, which requires GENERIC_WRITE while
	// os.Root is deliberately read-only. Payload files are synced before
	// publication; replacement reopens the temporary with FILE_WRITE_THROUGH
	// and renames that handle relative to the bound directory handle.
	return nil
}
