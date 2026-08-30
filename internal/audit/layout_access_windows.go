//go:build windows

package audit

import "os"

func auditFileAccessReady(os.FileInfo) bool {
	// POSIX mode bits do not prove the protected current-user-only DACL.
	return false
}
