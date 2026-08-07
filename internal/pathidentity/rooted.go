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
	// against that drive's working directory instead of the repository.
	if len(trimmed) >= 2 && trimmed[1] == ':' {
		letter := trimmed[0]
		if letter >= 'a' && letter <= 'z' || letter >= 'A' && letter <= 'Z' {
			return true
		}
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
