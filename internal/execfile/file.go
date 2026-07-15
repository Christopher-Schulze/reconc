package execfile

import (
	"os"
)

// Is reports whether path is a real regular file that the current platform can
// dispatch. POSIX requires an executable bit; Windows executable intent is
// determined by the selected host command rather than os.FileMode.
func Is(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && executableMode(info.Mode())
}
