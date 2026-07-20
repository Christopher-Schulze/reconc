package agentsession

import (
	"os"
	"path/filepath"
	"strings"
)

// agentMemoryWritePath reports whether path targets the Claude Code
// persistent-memory tree (~/.claude/projects/<project>/memory/**). Memory is
// agent runtime state owned by the agent harness, not repository content:
// blocking it as a repo-boundary escape breaks the harness memory feature,
// and recording it as repo write evidence would poison policy triggers.
//
// The check is symlink-hardened fail-closed: the path (or, for a not-yet
// created leaf, its closest existing ancestor) must RESOLVE inside the memory
// tree. A memory-looking path that resolves elsewhere (for example a
// symlinked memory directory pointing into a repository) is NOT treated as
// memory, so the normal write policy applies to it.
func agentMemoryWritePath(raw string) bool {
	path := strings.TrimSpace(raw)
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	projectsRoot := filepath.Join(home, ".claude", "projects")
	cleaned := filepath.Clean(path)
	if !insideAgentMemoryTree(projectsRoot, cleaned) {
		return false
	}
	if resolvedRoot, err := filepath.EvalSymlinks(projectsRoot); err == nil {
		projectsRoot = resolvedRoot
	}
	return insideAgentMemoryTree(projectsRoot, resolveClosestExistingAncestor(cleaned))
}

// insideAgentMemoryTree reports whether cleaned names a file strictly inside
// <projectsRoot>/<one-project-segment>/memory (never the memory directory or
// anything above it).
func insideAgentMemoryTree(projectsRoot, cleaned string) bool {
	rel, err := filepath.Rel(projectsRoot, cleaned)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) >= 3 && parts[0] != "" && parts[0] != "." && parts[1] == "memory"
}

// resolveClosestExistingAncestor resolves symlinks on the longest existing
// prefix of path and re-joins the not-yet-existing suffix, so a first Write to
// a fresh memory file still gets an honest resolution.
func resolveClosestExistingAncestor(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	current := path
	suffix := ""
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		base := filepath.Base(current)
		if suffix == "" {
			suffix = base
		} else {
			suffix = filepath.Join(base, suffix)
		}
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(resolved, suffix)
		}
		current = parent
	}
}

// withoutAgentMemoryPaths returns paths minus every agent-memory target.
func withoutAgentMemoryPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if agentMemoryWritePath(path) {
			continue
		}
		out = append(out, path)
	}
	return out
}
