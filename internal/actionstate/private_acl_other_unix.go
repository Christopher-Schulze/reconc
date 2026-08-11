//go:build !windows && !darwin

package actionstate

import "os"

func validatePrivateFileACL(_ *os.File) error {
	return nil
}

func validatePrivatePathACL(_ string) error {
	return nil
}
