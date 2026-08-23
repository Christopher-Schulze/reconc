//go:build !windows

package usercli

import "os"

func releaseCandidateModeMatches(_ string, info os.FileInfo, requested os.FileMode) (bool, error) {
	return info != nil && info.Mode().Perm() == requested.Perm(), nil
}
