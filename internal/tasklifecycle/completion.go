package tasklifecycle

import (
	"path"
	"slices"
	"strings"
)

// DirtyCompletionPaths returns the Git-dirty paths owned by the configured
// TASK control plane. Both the final completion gate and terminal Stop hook
// use this exact path contract for completion.require_committed.
func DirtyCompletionPaths(cfg Config, dirtyPaths []string) []string {
	overview := canonicalTaskPathSegments(cfg.OverviewPath)
	detail := canonicalTaskPathSegments(cfg.DetailDir)
	owned := make([]string, 0, len(dirtyPaths))
	seen := make(map[string]struct{}, len(dirtyPaths))
	for _, dirtyPath := range dirtyPaths {
		if dirtyPath == "" {
			continue
		}
		if _, dup := seen[dirtyPath]; dup {
			continue
		}
		if !ownsDirtyTaskPath(canonicalTaskPathSegments(dirtyPath), overview, detail) {
			continue
		}
		seen[dirtyPath] = struct{}{}
		owned = append(owned, dirtyPath)
	}
	return owned
}

func ownsDirtyTaskPath(dirty, overview, detail []string) bool {
	if len(dirty) == 0 {
		return false
	}
	if slices.Equal(dirty, overview) {
		return true
	}
	if hasSegmentPrefix(detail, dirty) {
		return true
	}
	return isProperSegmentPrefix(dirty, overview) || isProperSegmentPrefix(dirty, detail)
}

func canonicalTaskPathSegments(raw string) []string {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/")
	if normalized == "" {
		return nil
	}
	normalized = path.Clean(normalized)
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") || path.IsAbs(normalized) {
		return nil
	}
	return strings.Split(normalized, "/")
}

func hasSegmentPrefix(prefix, full []string) bool {
	if len(prefix) == 0 || len(full) < len(prefix) {
		return false
	}
	return slices.Equal(prefix, full[:len(prefix)])
}

func isProperSegmentPrefix(prefix, full []string) bool {
	return len(prefix) > 0 && len(prefix) < len(full) && hasSegmentPrefix(prefix, full)
}
