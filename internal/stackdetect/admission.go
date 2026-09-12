package stackdetect

import (
	"path"
	"strings"
)

// CanonicalDiscoveryPath returns the repository-relative slash path if a
// file at that location can participate in stack detection. It applies
// separator canonicalization, ignored-directory, and depth rules without
// touching the filesystem.
func CanonicalDiscoveryPath(relative string) (string, bool) {
	normalized, ok := normalizeDiscoveryPath(relative)
	if !ok || !admitsNormalizedDiscoveryFile(normalized) {
		return "", false
	}
	return normalized, true
}

func normalizeDiscoveryPath(relative string) (string, bool) {
	slash := strings.ReplaceAll(strings.TrimSpace(relative), `\`, "/")
	if slash == "" {
		return "", false
	}
	cleaned := path.Clean(slash)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) {
		return "", false
	}
	if cleaned != slash {
		return "", false
	}
	return cleaned, true
}

func admitsNormalizedDiscoveryFile(relative string) bool {
	return pathDepth(relative) <= maxDepth && !discoveryPathHasIgnoredAncestor(relative)
}

func ignoredDiscoveryDirectory(name string) bool {
	return ignoredDirectories[strings.ToLower(name)]
}

func discoveryPathHasIgnoredAncestor(relative string) bool {
	parts := strings.Split(relative, "/")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts[:len(parts)-1] {
		if ignoredDiscoveryDirectory(part) {
			return true
		}
	}
	return false
}
