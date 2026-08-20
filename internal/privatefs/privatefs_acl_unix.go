//go:build !windows && !darwin

package privatefs

import "os"

func validatePrivateFileACL(*os.File) error      { return nil }
func validatePrivateDirectoryACL(*os.File) error { return nil }
