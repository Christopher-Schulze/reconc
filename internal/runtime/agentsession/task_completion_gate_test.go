package agentsession

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestTerminalTaskCompletionRequiresCommittedControlPlaneWhenConfigured(t *testing.T) {
	t.Setenv(stopPolicyUntrackedModeEnv, "no")
	repo := setupPolicyRepo(t)
	gitInitHelper(t, repo)
	config := "task_lifecycle:\n  profile: sections-v1\n  completion:\n    require_committed: true\nrules: []\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	overview := "# TASK Control Plane\n\n## Active\n\n## Queue\n\n## Blocked\n\n## Done\n"
	if err := os.WriteFile(filepath.Join(repo, "docs", "tasks.md"), []byte(overview), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("refresh policy: %v", err)
	}
	if _, err := InitializeSessionState(repo, "terminal"); err != nil {
		t.Fatal(err)
	}
	cacheSnapshot := stopPolicyGitSnapshotFor(repo)
	if cacheSnapshot.StatusMode != "no" || strings.Contains(cacheSnapshot.Status, "docs/tasks.md") {
		t.Fatalf("cache snapshot did not honor no-untracked tuning: %#v", cacheSnapshot)
	}
	terminalSnapshot := completionPolicyGitSnapshotFor(repo)
	if terminalSnapshot.StatusMode != "all" || !strings.Contains(terminalSnapshot.Status, "docs/tasks.md") {
		t.Fatalf("terminal snapshot did not capture all untracked files: %#v", terminalSnapshot)
	}
	blocked := RunStop(repo, []byte(`{"session_id":"terminal","runtime":"codex"}`))
	if blocked.ExitCode != 0 || !strings.Contains(blocked.Stdout, "TASK control plane is not committed") {
		t.Fatalf("dirty terminal TASK control plane was not blocked: %+v", blocked)
	}
	cachedBlocked := RunStop(repo, []byte(`{"session_id":"terminal","runtime":"codex","stop_hook_active":true}`))
	if cachedBlocked.ExitCode != 0 || !strings.Contains(cachedBlocked.Stdout, "TASK control plane is not committed") {
		t.Fatalf("clean Stop-cache hit bypassed the terminal TASK gate: %+v", cachedBlocked)
	}
	command := exec.Command("git", "-C", repo, "add", ".reconc.yml", "docs/tasks.md")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, output)
	}
	command = exec.Command("git", "-C", repo, "commit", "-m", "terminal task", "--quiet")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, output)
	}
	clean := RunStop(repo, []byte(`{"session_id":"terminal","runtime":"codex"}`))
	if clean.ExitCode != 0 || clean.Stdout != "" || clean.Stderr != "" {
		t.Fatalf("committed terminal TASK control plane did not release Stop: %+v", clean)
	}
}

func TestTerminalTaskCompletionBlocksDirtyGitlinkAncestor(t *testing.T) {
	if testing.Short() {
		t.Skip("creates a local Git submodule fixture")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.useConfigOnly")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	t.Setenv(stopPolicyUntrackedModeEnv, "no")
	repo := setupPolicyRepo(t)
	gitInitHelper(t, repo)
	config := "task_lifecycle:\n  profile: sections-v1\n  completion:\n    require_committed: true\nrules: []\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	overview := "# TASK Control Plane\n\n## Active\n\n## Queue\n\n## Blocked\n\n## Done\n"
	addSessionGitlink(t, repo, "docs", overview, true)
	addSessionGitlink(t, repo, "vendor/lib", "unrelated\n", false)
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("refresh policy: %v", err)
	}
	sessionGit(t, repo, "add", "-A")
	sessionGit(t, repo, "commit", "-m", "task and unrelated gitlinks", "--quiet")
	if _, err := InitializeSessionState(repo, "terminal-gitlink"); err != nil {
		t.Fatal(err)
	}
	clean := RunStop(repo, []byte(`{"session_id":"terminal-gitlink","runtime":"codex"}`))
	if clean.ExitCode != 0 || clean.Stdout != "" || clean.Stderr != "" {
		t.Fatalf("clean TASK gitlink did not release Stop: %+v", clean)
	}

	if err := os.WriteFile(filepath.Join(repo, "docs", "tasks.md"), []byte(overview+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocked := RunStop(repo, []byte(`{"session_id":"terminal-gitlink","runtime":"codex"}`))
	if blocked.ExitCode != 0 || !strings.Contains(blocked.Stdout, "TASK control plane is not committed") || !strings.Contains(blocked.Stdout, "docs") {
		t.Fatalf("dirty TASK gitlink ancestor was not blocked: %+v", blocked)
	}

	if err := os.WriteFile(filepath.Join(repo, "vendor", "lib", "state.txt"), []byte("dirty-unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocked = RunStop(repo, []byte(`{"session_id":"terminal-gitlink","runtime":"codex"}`))
	if blocked.ExitCode != 0 || !strings.Contains(blocked.Stdout, "docs") || strings.Contains(blocked.Stdout, "vendor/lib") {
		t.Fatalf("unrelated dirty gitlink entered terminal TASK Stop: %+v", blocked)
	}

	sessionGit(t, filepath.Join(repo, "docs"), "add", "tasks.md")
	sessionGit(t, filepath.Join(repo, "docs"), "commit", "-m", "task dirty", "--quiet")
	sessionGit(t, repo, "add", "docs")
	sessionGit(t, repo, "commit", "-m", "record task gitlink", "--quiet")
	released := RunStop(repo, []byte(`{"session_id":"terminal-gitlink","runtime":"codex"}`))
	if released.ExitCode != 0 || released.Stdout != "" || released.Stderr != "" {
		t.Fatalf("unrelated dirty gitlink blocked terminal TASK Stop: %+v", released)
	}
}

func sessionGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git -C %s %v: %v\n%s", repo, args, err, output)
	}
}

func addSessionGitlink(t *testing.T, repo, mount, content string, taskRoot bool) {
	t.Helper()
	child := t.TempDir()
	sessionGit(t, child, "init", "--quiet")
	sessionGit(t, child, "config", "user.name", "reconc-test")
	sessionGit(t, child, "config", "user.email", "reconc-test@example.com")
	if taskRoot {
		if err := os.MkdirAll(filepath.Join(child, "tasks"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(child, "tasks.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(child, "tasks", ".gitkeep"), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(filepath.Join(child, "state.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionGit(t, child, "add", "-A")
	sessionGit(t, child, "commit", "-m", "gitlink child", "--quiet")
	sessionGit(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", "--quiet", child, mount)
	sessionGit(t, filepath.Join(repo, mount), "config", "user.name", "reconc-test")
	sessionGit(t, filepath.Join(repo, mount), "config", "user.email", "reconc-test@example.com")
}

func TestUncacheableStopRejectsTerminalGitDriftDuringEvaluation(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	config := "task_lifecycle:\n  profile: sections-v1\n  completion:\n    require_committed: true\nrules: []\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	overview := "# TASK Control Plane\n\n## Active\n\n## Queue\n\n## Blocked\n\n## Done\n"
	if err := os.WriteFile(filepath.Join(repo, "docs", "tasks.md"), []byte(overview), 0o644); err != nil {
		t.Fatal(err)
	}
	removeScriptCacheInputs(t, repo)
	script := "#!/bin/sh\nprintf '\\n<!-- mutated during Stop -->\\n' >> docs/tasks.md\nexit 0\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc", "scripts", "stop-gate.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "-C", repo, "add", ".reconc.yml", "docs/tasks.md")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, output)
	}
	command = exec.Command("git", "-C", repo, "commit", "-m", "terminal control plane", "--quiet")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, output)
	}
	if _, err := InitializeSessionState(repo, "terminal-drift"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "terminal-drift", func(state SessionState) SessionState {
		return AppendWritePath(state, "src/a.go")
	}); err != nil {
		t.Fatal(err)
	}

	result := RunStop(repo, []byte(`{"session_id":"terminal-drift","runtime":"codex"}`))
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "terminal repository state changed during Stop evaluation") {
		t.Fatalf("terminal Git drift was released from an uncacheable Stop: %+v", result)
	}
}
