package execfile

import (
	"os"
)

// Is reports whether path is a real regular file that the current platform can
// dispatch. POSIX requires an executable bit; Windows executable intent is
// determined by the selected host command rather than os.FileMode.
func Is(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && ModeIsExecutable(info.Mode())
}

// ModeIsExecutable reports whether an already inspected real-file mode is
// dispatchable on the current platform.
func ModeIsExecutable(mode os.FileMode) bool {
	return mode.IsRegular() && mode&os.ModeSymlink == 0 && executableMode(mode)
}
