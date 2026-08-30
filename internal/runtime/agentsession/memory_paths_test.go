package agentsession

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func setTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	return home
}

// TestAgentMemoryWritePathScopesExactlyTheMemoryTree proves the memory
// allowance covers only ~/.claude/projects/<project>/memory/** and nothing
// adjacent: settings, hooks, project roots, and repo paths stay policy-gated.
func TestAgentMemoryWritePathScopesExactlyTheMemoryTree(t *testing.T) {
	home := setTestHome(t)
	repo := filepath.Join(home, "workspace", "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	projectKey := claudeProjectKey(repo)
	memoryDir := filepath.Join(home, ".claude", "projects", projectKey, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"memory file", filepath.Join(memoryDir, "MEMORY.md"), true},
		{"new memory file not yet created", filepath.Join(memoryDir, "fresh-note.md"), true},
		{"nested memory file", filepath.Join(memoryDir, "sub", "deep.md"), true},
		{"memory dir itself has no file target", memoryDir, false},
		{"claude settings stay gated", filepath.Join(home, ".claude", "settings.json"), false},
		{"project root outside memory", filepath.Join(home, ".claude", "projects", "-Users-x-repo", "notes.md"), false},
		{"different project memory", filepath.Join(home, ".claude", "projects", "-Users-other-repo", "memory", "MEMORY.md"), false},
		{"projects root", filepath.Join(home, ".claude", "projects"), false},
		{"memory-named dir outside claude tree", filepath.Join(home, "memory", "MEMORY.md"), false},
		{"relative path", ".claude/projects/x/memory/MEMORY.md", false},
		{"empty", "", false},
		{"traversal out of memory", filepath.Join(memoryDir, "..", "escape.md"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentMemoryWritePath(repo, tc.path); got != tc.want {
				t.Fatalf("agentMemoryWritePath(%q, %q)=%v, want %v", repo, tc.path, got, tc.want)
			}
		})
	}
}

// TestAgentMemoryWritePathRefusesSymlinkedMemoryEscape proves the symlink
// hardening: a memory directory that is really a symlink into another tree is
// NOT treated as memory, so the normal repo write policy still applies.
func TestAgentMemoryWritePathRefusesSymlinkedMemoryEscape(t *testing.T) {
	home := setTestHome(t)
	repo := filepath.Join(home, "workspace", "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(home, ".claude", "projects", claudeProjectKey(repo))
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(home, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(project, "memory")); err != nil {
		// Symlink creation is privilege-gated on some Windows hosts; the
		// hardening scenario cannot be constructed there, and unix runs
		// assert it unconditionally.
		if runtime.GOOS == "windows" {
			t.Logf("cannot construct symlinked memory dir: %v", err)
			return
		}
		t.Fatal(err)
	}
	memoryPath := filepath.Join(project, "memory", "MEMORY.md")
	if agentMemoryWritePath(repo, memoryPath) {
		t.Fatal("symlinked memory dir must not bypass the write policy")
	}
	matcherLoads := 0
	got := filterAgentMemoryPaths(filepath.Join(home, ".claude", "projects"), []string{memoryPath}, func() claudeProjectKeyMatcher {
		matcherLoads++
		return expectedClaudeProjectKeys(repo)
	})
	if len(got) != 1 || got[0] != memoryPath || matcherLoads != 0 {
		t.Fatalf("symlink escape filter=%q matcher loads=%d, want unchanged path and no identity lookup", got, matcherLoads)
	}
}

func TestAgentMemoryPathPreflightDefersCommonDirDiscovery(t *testing.T) {
	home := setTestHome(t)
	projectsRoot := filepath.Join(home, ".claude", "projects")
	primary := filepath.Join(home, "workspace", "primary")
	worktree := filepath.Join(home, "workspace", "worktree")
	for _, path := range []string{projectsRoot, primary, worktree} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: ../primary/.git/worktrees/worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	commonDirCalls := 0
	matcherLoads := 0
	loadMatcher := func() claudeProjectKeyMatcher {
		matcherLoads++
		return expectedClaudeProjectKeysWithCommonDir(worktree, func(string) (string, bool) {
			commonDirCalls++
			return filepath.Join(primary, ".git"), true
		})
	}
	ordinary := []string{"src/file.go", filepath.Join(worktree, "README.md"), filepath.Join(home, "notes.md")}
	if got := filterAgentMemoryPaths(projectsRoot, ordinary, loadMatcher); strings.Join(got, "\x00") != strings.Join(ordinary, "\x00") {
		t.Fatalf("ordinary paths changed: got=%q want=%q", got, ordinary)
	}
	if matcherLoads != 0 || commonDirCalls != 0 {
		t.Fatalf("ordinary paths loaded matcher=%d common-dir=%d, want zero", matcherLoads, commonDirCalls)
	}

	primaryMemory := filepath.Join(projectsRoot, claudeProjectKey(primary), "memory")
	worktreeMemory := filepath.Join(projectsRoot, claudeProjectKey(worktree), "memory")
	otherMemory := filepath.Join(projectsRoot, claudeProjectKey(filepath.Join(home, "other")), "memory")
	for _, path := range []string{primaryMemory, worktreeMemory, otherMemory} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	otherFile := filepath.Join(otherMemory, "MEMORY.md")
	got := filterAgentMemoryPaths(projectsRoot, []string{
		filepath.Join(primaryMemory, "MEMORY.md"),
		filepath.Join(worktreeMemory, "MEMORY.md"),
		otherFile,
	}, loadMatcher)
	if len(got) != 1 || got[0] != otherFile {
		t.Fatalf("worktree/common-dir filtering = %q, want only %q", got, otherFile)
	}
	if matcherLoads != 1 || commonDirCalls != 1 {
		t.Fatalf("candidate paths loaded matcher=%d common-dir=%d, want exactly once", matcherLoads, commonDirCalls)
	}
}

// TestRunPreToolUseAllowsAgentMemoryWrites proves the hook lets a Write to
// the agent memory tree through without evaluating it as a repo write, and
// that no repo write evidence is recorded for it.
func TestRunPreToolUseAllowsAgentMemoryWrites(t *testing.T) {
	home := setTestHome(t)
	repo := setupPolicyRepo(t)
	memoryFile := filepath.Join(home, ".claude", "projects", claudeProjectKey(repo), "memory", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(memoryFile), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = RunSessionStart(repo, []byte(`{"session_id":"s-mem"}`))

	payload := fmt.Sprintf(`{"session_id":"s-mem","tool_name":"Write","tool_input":{"file_path":%q}}`, memoryFile)
	if result := RunPreToolUse(repo, []byte(payload)); result.ExitCode != 0 {
		t.Fatalf("memory write must be allowed, got exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	if result := RunPostToolUse(repo, []byte(payload)); result.ExitCode != 0 {
		t.Fatalf("memory write post hook: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	state, err := LoadSessionState(repo, "s-mem")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range state.WritePaths {
		if path == memoryFile {
			t.Fatalf("memory write leaked into repo write evidence: %v", state.WritePaths)
		}
	}
}

func TestAgentMemoryWritePathHonorsClaudeConfigDir(t *testing.T) {
	home := setTestHome(t)
	repo := filepath.Join(home, "workspace", "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	configRoot := filepath.Join(home, "claude-config")
	t.Setenv("CLAUDE_CONFIG_DIR", configRoot)
	memoryFile := filepath.Join(configRoot, "projects", claudeProjectKey(repo), "memory", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(memoryFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if !agentMemoryWritePath(repo, memoryFile) {
		t.Fatalf("current project memory under CLAUDE_CONFIG_DIR must be allowed: %s", memoryFile)
	}
}

var benchmarkAgentMemoryPaths []string

func BenchmarkAgentMemoryPathPreflight(b *testing.B) {
	root := b.TempDir()
	projectsRoot := filepath.Join(root, "claude", "projects")
	repo := filepath.Join(root, "worktree")
	if err := os.MkdirAll(projectsRoot, 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: ../missing\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	paths := []string{"src/file.go", "docs/documentation.md", filepath.Join(repo, "README.md")}

	b.Run("preflight", func(b *testing.B) {
		gitInvocations := 0
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			benchmarkAgentMemoryPaths = filterAgentMemoryPaths(projectsRoot, paths, func() claudeProjectKeyMatcher {
				return expectedClaudeProjectKeysWithCommonDir(repo, func(root string) (string, bool) {
					gitInvocations++
					return resolveGitCommonDir(root)
				})
			})
		}
		b.ReportMetric(float64(gitInvocations)/float64(b.N), "git-invocations/op")
	})

	b.Run("eager-baseline", func(b *testing.B) {
		gitInvocations := 0
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			matcher := expectedClaudeProjectKeysWithCommonDir(repo, func(root string) (string, bool) {
				gitInvocations++
				return resolveGitCommonDir(root)
			})
			out := make([]string, 0, len(paths))
			for _, path := range paths {
				if !agentMemoryWritePathInProjects(projectsRoot, matcher, path) {
					out = append(out, path)
				}
			}
			benchmarkAgentMemoryPaths = out
		}
		b.ReportMetric(float64(gitInvocations)/float64(b.N), "git-invocations/op")
	})
}
