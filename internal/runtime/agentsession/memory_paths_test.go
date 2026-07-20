package agentsession

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	if agentMemoryWritePath(repo, filepath.Join(project, "memory", "MEMORY.md")) {
		t.Fatal("symlinked memory dir must not bypass the write policy")
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
