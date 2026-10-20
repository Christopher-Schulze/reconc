//go:build !linux && !darwin

package pathidentity

import "os"

// changeTime reports no inode change time where the platform's file
// information does not carry one.
func changeTime(os.FileInfo) (int64, bool) {
	return 0, false
}
