package agentsession

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/gitexec"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/retention"
)

// agentMemoryWritePath reports whether path targets the Claude Code
// persistent-memory tree (~/.claude/projects/<project>/memory/**). Memory is
// agent runtime state owned by the agent harness, not repository content:
// blocking it as a repo-boundary escape breaks the harness memory feature,
// and recording it as repo write evidence would poison policy triggers.
//
// The check is filesystem-identity-hardened and fail-closed: the path (or, for
// a not-yet-created leaf, its closest existing ancestor) must resolve inside
// the memory tree. A memory-looking path redirected elsewhere by a Unix
// symlink or Windows reparse point is not treated as memory, so the normal
// write policy applies to it.
func agentMemoryWritePath(repoRoot, raw string) bool {
	configRoot, ok := claudeConfigRoot()
	if !ok || strings.TrimSpace(repoRoot) == "" {
		return false
	}
	projectsRoot := filepath.Join(configRoot, "projects")
	projectKey, ok := agentMemoryProjectKeyInProjects(projectsRoot, raw)
	return ok && expectedClaudeProjectKeys(repoRoot).allows(projectKey)
}

func agentMemoryWritePathInProjects(projectsRoot string, allowedProjectKeys claudeProjectKeyMatcher, raw string) bool {
	projectKey, ok := agentMemoryProjectKeyInProjects(projectsRoot, raw)
	return ok && allowedProjectKeys.allows(projectKey)
}

func agentMemoryProjectKeyInProjects(projectsRoot, raw string) (string, bool) {
	if raw == "" || !filepath.IsAbs(raw) {
		return "", false
	}
	resolvedRoot, err := pathidentity.ResolveExisting(projectsRoot)
	if err != nil {
		return "", false
	}
	resolved, err := pathidentity.ResolveProspective(filepath.Clean(raw))
	if err != nil {
		return "", false
	}
	return agentMemoryProjectKey(resolvedRoot, resolved)
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

type claudeProjectKeyMatcher struct {
	exact map[string]bool
	roots []string
}

func (matcher claudeProjectKeyMatcher) allows(projectKey string) bool {
	if matcher.exact[projectKey] {
		return true
	}
	for _, root := range matcher.roots {
		if claudeProjectKeyMatchesFilesystemAliases(root, projectKey) {
			return true
		}
	}
	return false
}

func (matcher *claudeProjectKeyMatcher) addRoot(root string) {
	if matcher.exact == nil {
		matcher.exact = map[string]bool{}
	}
	root = retention.CanonicalizePathCase(root)
	aliases, err := pathidentity.ExistingAliases(root)
	if err != nil {
		addClaudeProjectKey(matcher.exact, root)
		return
	}
	for _, alias := range aliases {
		addClaudeProjectKey(matcher.exact, alias)
	}
	resolved, err := pathidentity.ResolveExisting(root)
	if err == nil {
		for _, existing := range matcher.roots {
			if existing == resolved {
				return
			}
		}
		matcher.roots = append(matcher.roots, resolved)
	}
}

func expectedClaudeProjectKeys(repoRoot string) claudeProjectKeyMatcher {
	return expectedClaudeProjectKeysWithCommonDir(repoRoot, resolveGitCommonDir)
}

func expectedClaudeProjectKeysWithCommonDir(repoRoot string, commonDirFor func(string) (string, bool)) claudeProjectKeyMatcher {
	matcher := claudeProjectKeyMatcher{exact: map[string]bool{}}
	if root, err := filepath.Abs(repoRoot); err == nil {
		matcher.addRoot(root)
		if gitInfo, gitErr := os.Stat(filepath.Join(root, ".git")); gitErr == nil && gitInfo.IsDir() {
			return matcher
		}
	}
	// Claude shares project memory across Git worktrees. The common Git
	// directory identifies the primary worktree without allowing any unrelated
	// ~/.claude/projects entry.
	if common, ok := commonDirFor(repoRoot); ok {
		common = filepath.Clean(common)
		if !filepath.IsAbs(common) {
			common = filepath.Join(repoRoot, common)
		}
		if filepath.Base(common) == ".git" {
			matcher.addRoot(filepath.Dir(common))
		}
	}
	return matcher
}

func resolveGitCommonDir(repoRoot string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := gitexec.CommandContext(ctx, repoRoot, nil, "rev-parse", "--git-common-dir")
	output, err := boundedexec.Output(command, maxGitControlFileBytes)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(output)), true
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

// withoutAgentMemoryPaths returns paths minus every agent-memory target.
func withoutAgentMemoryPaths(repoRoot string, paths []string) []string {
	configRoot, configOK := claudeConfigRoot()
	if !configOK || strings.TrimSpace(repoRoot) == "" {
		return append([]string(nil), paths...)
	}
	projectsRoot := filepath.Join(configRoot, "projects")
	return filterAgentMemoryPaths(projectsRoot, paths, func() claudeProjectKeyMatcher {
		return expectedClaudeProjectKeys(repoRoot)
	})
}

func filterAgentMemoryPaths(projectsRoot string, paths []string, loadProjectKeys func() claudeProjectKeyMatcher) []string {
	out := make([]string, 0, len(paths))
	var allowedProjectKeys claudeProjectKeyMatcher
	loadedProjectKeys := false
	for _, path := range paths {
		projectKey, candidate := agentMemoryProjectKeyInProjects(projectsRoot, path)
		if !candidate {
			out = append(out, path)
			continue
		}
		if !loadedProjectKeys {
			allowedProjectKeys = loadProjectKeys()
			loadedProjectKeys = true
		}
		if allowedProjectKeys.allows(projectKey) {
			continue
		}
		out = append(out, path)
	}
	return out
}
