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
	blocked := RunStop(repo, []byte(`{"session_id":"terminal","runtime":"codex"}`))
	if blocked.ExitCode != 0 || !strings.Contains(blocked.Stdout, "TASK control plane is not committed") {
		t.Fatalf("dirty terminal TASK control plane was not blocked: %+v", blocked)
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
