package pathidentity

import (
	"path/filepath"
	"slices"
	"strings"
)

// Rooted reports whether a configured path names a location independently of
// the repository it is written for. It is deliberately platform-independent:
// `filepath.IsAbs` answers for the running operating system only, so a POSIX
// root such as `/etc/passwd` is "not absolute" on Windows and a repository
// policy would be accepted there and refused on Unix.
//
// The same policy file must produce the same decision on every platform, so
// this rejects every rooting convention at once: a leading separator of either
// kind, a Windows drive or UNC volume, and a bare `C:relative` drive prefix.
func Rooted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) {
		return true
	}
	if filepath.IsAbs(trimmed) || filepath.VolumeName(trimmed) != "" {
		return true
	}
	// `C:file` is drive-relative rather than absolute, and Windows resolves it
	// against that drive's working directory instead of the repository. The
	// prefix counts whatever character precedes the colon: Windows treats a
	// non-letter prefix as a volume too, so restricting this to letters would
	// hand the two platforms different answers for the same string, which is
	// exactly what this helper exists to prevent. Nothing repo-relative needs a
	// colon in second position.
	if len(trimmed) >= 2 && trimmed[1] == ':' {
		return true
	}
	return false
}

// EscapesLexically reports whether a cleaned repo-relative path climbs out of
// its root. It accepts either separator so a policy written on one platform is
// read the same way on the other.
func EscapesLexically(value string) bool {
	normalized := strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	if normalized == "" {
		return false
	}
	return slices.Contains(strings.Split(normalized, "/"), "..")
}
