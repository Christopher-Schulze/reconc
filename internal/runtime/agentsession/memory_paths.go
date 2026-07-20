package agentsession

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/retention"
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
func agentMemoryWritePath(repoRoot, raw string) bool {
	configRoot, ok := claudeConfigRoot()
	if !ok || strings.TrimSpace(repoRoot) == "" {
		return false
	}
	projectsRoot := filepath.Join(configRoot, "projects")
	return agentMemoryWritePathInProjects(projectsRoot, expectedClaudeProjectKeys(repoRoot), raw)
}

func agentMemoryWritePathInProjects(projectsRoot string, allowedProjectKeys map[string]bool, raw string) bool {
	path := strings.TrimSpace(raw)
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	cleaned := filepath.Clean(path)
	projectKey, ok := agentMemoryProjectKey(projectsRoot, cleaned)
	if !ok || !allowedProjectKeys[projectKey] {
		return false
	}
	if resolvedRoot, err := filepath.EvalSymlinks(projectsRoot); err == nil {
		projectsRoot = resolvedRoot
	}
	resolved := resolveClosestExistingAncestor(cleaned)
	resolvedKey, ok := agentMemoryProjectKey(projectsRoot, resolved)
	return ok && resolvedKey == projectKey
}

func claudeConfigRoot() (string, bool) {
	if configured := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", false
		}
		return filepath.Clean(configured), true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".claude"), true
}

// agentMemoryProjectKey returns the one project-key segment for a file
// strictly below <projectsRoot>/<project>/memory.
func agentMemoryProjectKey(projectsRoot, cleaned string) (string, bool) {
	rel, err := filepath.Rel(projectsRoot, cleaned)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 3 || parts[0] == "" || parts[0] == "." || parts[1] != "memory" {
		return "", false
	}
	return parts[0], true
}

func expectedClaudeProjectKeys(repoRoot string) map[string]bool {
	keys := map[string]bool{}
	if root, err := filepath.Abs(repoRoot); err == nil {
		root = retention.CanonicalizePathCase(root)
		addClaudeProjectKey(keys, root)
		if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
			addClaudeProjectKey(keys, resolved)
		}
		if gitInfo, gitErr := os.Stat(filepath.Join(root, ".git")); gitErr == nil && gitInfo.IsDir() {
			return keys
		}
	}
	// Claude shares project memory across Git worktrees. The common Git
	// directory identifies the primary worktree without allowing any unrelated
	// ~/.claude/projects entry.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-parse", "--git-common-dir")
	if output, err := command.Output(); err == nil {
		common := filepath.Clean(strings.TrimSpace(string(output)))
		if !filepath.IsAbs(common) {
			common = filepath.Join(repoRoot, common)
		}
		if filepath.Base(common) == ".git" {
			keys[claudeProjectKey(filepath.Dir(common))] = true
		}
	}
	return keys
}

func addClaudeProjectKey(keys map[string]bool, root string) {
	keys[claudeProjectKey(root)] = true
	if !strings.HasPrefix(root, string(filepath.Separator)+"private"+string(filepath.Separator)) {
		return
	}
	alias := strings.TrimPrefix(root, string(filepath.Separator)+"private")
	rootInfo, rootErr := os.Stat(root)
	aliasInfo, aliasErr := os.Stat(alias)
	if rootErr == nil && aliasErr == nil && os.SameFile(rootInfo, aliasInfo) {
		keys[claudeProjectKey(alias)] = true
	}
}

func claudeProjectKey(repoRoot string) string {
	var key strings.Builder
	for _, character := range filepath.ToSlash(filepath.Clean(repoRoot)) {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' {
			key.WriteRune(character)
		} else {
			key.WriteByte('-')
		}
	}
	return key.String()
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
func withoutAgentMemoryPaths(repoRoot string, paths []string) []string {
	configRoot, configOK := claudeConfigRoot()
	if !configOK || strings.TrimSpace(repoRoot) == "" {
		return append([]string(nil), paths...)
	}
	projectsRoot := filepath.Join(configRoot, "projects")
	allowedProjectKeys := expectedClaudeProjectKeys(repoRoot)
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if agentMemoryWritePathInProjects(projectsRoot, allowedProjectKeys, path) {
			continue
		}
		out = append(out, path)
	}
	return out
}
