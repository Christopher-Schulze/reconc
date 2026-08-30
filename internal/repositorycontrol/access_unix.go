//go:build !windows

package repositorycontrol

import (
	"fmt"
	"os"
)

func secureCreatedPublicDirectory(directory *os.Root, mode os.FileMode) error {
	if err := directory.Chmod(".", mode); err != nil {
		return err
	}
	info, err := directory.Stat(".")
	if err != nil {
		return err
	}
	if info.Mode().Perm() != mode.Perm() {
		return fmt.Errorf("created directory mode is %04o, want %04o", info.Mode().Perm(), mode.Perm())
	}
	return nil
}

func inheritedDirectoryMode(parent os.FileInfo) os.FileMode {
	return parent.Mode().Perm() | parent.Mode()&os.ModeSetgid
}

func coordinationFileMode(directory os.FileInfo) os.FileMode {
	mode := os.FileMode(0o600)
	if directory.Mode().Perm()&0o020 != 0 {
		mode |= 0o060
	}
	if directory.Mode().Perm()&0o002 != 0 {
		mode |= 0o006
	}
	return mode
}
