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
