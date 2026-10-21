//go:build !windows && !darwin

package pathidentity

import (
	"io/fs"
	"os"
	"path/filepath"
)

func resolveExistingOS(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

// resolveExistingOSWithParent resolves path; EvalSymlinks of a non-symlink
// final name always ends in the identity of its parent directory.
func resolveExistingOSWithParent(path string) (string, bool, error) {
	resolved, err := filepath.EvalSymlinks(path)
	return resolved, err == nil, err
}

// composesExistingIdentity reports that a non-symlink name inside a resolved
// directory has that directory's identity plus its own name, which is exactly
// what EvalSymlinks returns for it.
const composesExistingIdentity = true

// storedSpellings needs no state: Unix identities keep the given spelling.
type storedSpellings struct{}

func (storedSpellings) compose(parentIdentity string, _ os.FileInfo, name string, _ os.FileInfo) (string, bool) {
	return filepath.Join(parentIdentity, name), true
}

// listedType reads no listing: an inspected parent saves its own inspection
// per path, and each path is still inspected itself.
func (storedSpellings) listedType(string, os.FileInfo, string) (fs.FileMode, bool) {
	return 0, false
}

func existingAliasesOS(string) []string {
	return nil
}

func aliasComparisonKey(path string) string {
	return path
}

// spellingCurrent has nothing to confirm: a Unix identity keeps the
// requested spelling, and a renamed ancestor fails the inode revalidation.
func spellingCurrent(string, string, os.FileInfo) bool {
	return true
}
