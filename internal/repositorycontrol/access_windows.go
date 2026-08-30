//go:build windows

package repositorycontrol

import "os"

func secureCreatedPublicDirectory(*os.Root, os.FileMode) error {
	// Public repository-control directories intentionally inherit the target
	// repository's ACL. Replacing that ACL would break shared repositories.
	return nil
}

func inheritedDirectoryMode(os.FileInfo) os.FileMode { return PublicDirectoryMode }

func coordinationFileMode(os.FileInfo) os.FileMode { return 0o600 }
