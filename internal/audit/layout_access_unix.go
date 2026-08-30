//go:build !windows

package audit

import "os"

func auditFileAccessReady(info os.FileInfo) bool {
	return info.Mode().Perm() == 0o600
}
