//go:build darwin

package pathidentity

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

func resolveExistingOS(path string) (string, error) {
	resolved, _, err := resolveExistingOSWithParent(path)
	return resolved, err
}

// resolveExistingOSWithParent also reports whether the identity is the
// descriptor path. Only then is its directory the canonical identity of the
// parent; an evaluated fallback keeps the caller's spelling.
func resolveExistingOSWithParent(path string) (resolved string, parentProven bool, err error) {
	evaluated, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false, err
	}
	// Opening a FIFO, socket or device for its descriptor path can block or
	// have side effects; only directories and regular files are opened.
	info, err := os.Stat(evaluated)
	if err != nil {
		return "", false, err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return filepath.Clean(evaluated), false, nil
	}
	file, err := os.OpenFile(evaluated, os.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", false, err
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()

	buffer := make([]byte, unix.PathMax)
	_, callErr := unix.FcntlInt(
		file.Fd(),
		unix.F_GETPATH,
		int(uintptr(unsafe.Pointer(&buffer[0]))),
	)
	runtime.KeepAlive(buffer)
	descriptorPath, descriptorOK := darwinDescriptorPath(evaluated, buffer, callErr)
	if descriptorOK {
		return descriptorPath, true, nil
	}
	return filepath.Clean(evaluated), false, nil
}

// composesExistingIdentity reports that a non-symlink name inside a resolved
// directory derives its identity from that directory, with the name's stored
// spelling exactly as resolveExistingOS canonicalizes it.
const composesExistingIdentity = true

const (
	maxListedDirectoryEntries = 1024
	maxListedNames            = 1 << 16
)

// storedSpellings caches the names each resolved directory stores, bound to
// the directory metadata observed before listing it. A name stored exactly as
// requested is already its canonical spelling, so only other spellings open
// the object for its descriptor path. Any entry change alters the directory's
// modification time, which forces a fresh listing.
type storedSpellings struct {
	directories map[string]directoryListing
	names       int
}

type directoryListing struct {
	info  os.FileInfo
	names map[string]struct{}
}

func (s *storedSpellings) compose(parentIdentity string, parentInfo os.FileInfo, name string, info os.FileInfo) (string, bool) {
	evaluated := filepath.Join(parentIdentity, name)
	if !info.IsDir() && !info.Mode().IsRegular() {
		return evaluated, true
	}
	if s.stores(parentIdentity, parentInfo, name) {
		return evaluated, true
	}
	file, err := os.OpenFile(evaluated, os.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", false
	}
	buffer := make([]byte, unix.PathMax)
	_, callErr := unix.FcntlInt(
		file.Fd(),
		unix.F_GETPATH,
		int(uintptr(unsafe.Pointer(&buffer[0]))),
	)
	runtime.KeepAlive(buffer)
	if err := file.Close(); err != nil {
		return "", false
	}
	return selectDarwinIdentity(evaluated, buffer, callErr), true
}

// stores reports whether directory, observed with info just before this call,
// stores name with exactly this spelling. A symlinked parent's metadata does
// not describe its target's entries, so it never answers from a listing.
func (s *storedSpellings) stores(directory string, info os.FileInfo, name string) bool {
	if info == nil || !info.IsDir() {
		return false
	}
	listing, ok := s.directories[directory]
	if !ok || !sameProspectiveAncestor(listing.info, info) {
		listing = directoryListing{info: info, names: readDirectoryNames(directory)}
		if s.directories == nil || s.names+len(listing.names) > maxListedNames {
			s.directories = make(map[string]directoryListing)
			s.names = 0
		}
		s.directories[directory] = listing
		s.names += len(listing.names)
	}
	_, stored := listing.names[name]
	return stored
}

// readDirectoryNames lists at most maxListedDirectoryEntries names; a larger
// or unreadable directory yields no names, so its entries take the
// descriptor path instead.
func readDirectoryNames(directory string) map[string]struct{} {
	handle, err := os.Open(directory)
	if err != nil {
		return nil
	}
	entries, readErr := handle.Readdirnames(maxListedDirectoryEntries + 1)
	closeErr := handle.Close()
	if (readErr != nil && readErr != io.EOF) || closeErr != nil || len(entries) > maxListedDirectoryEntries {
		return nil
	}
	names := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		names[entry] = struct{}{}
	}
	return names
}

func selectDarwinIdentity(evaluated string, buffer []byte, callErr error) string {
	if descriptorPath, ok := darwinDescriptorPath(evaluated, buffer, callErr); ok {
		return descriptorPath
	}
	return filepath.Clean(evaluated)
}

// darwinDescriptorPath returns the descriptor path when it names evaluated in
// a case-insensitive comparison; another hard link's name is never adopted.
func darwinDescriptorPath(evaluated string, buffer []byte, callErr error) (string, bool) {
	if callErr != nil {
		return "", false
	}
	length := bytes.IndexByte(buffer, 0)
	if length <= 0 {
		return "", false
	}
	descriptorPath := filepath.Clean(string(buffer[:length]))
	if strings.EqualFold(filepath.Clean(evaluated), descriptorPath) {
		return descriptorPath, true
	}
	return "", false
}

func existingAliasesOS(string) []string {
	return nil
}

func aliasComparisonKey(path string) string {
	return path
}
