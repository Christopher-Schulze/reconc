package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/boundedexec"
)

const maxGitPorcelainBytes = 16 << 20

// gitIsClean runs `git status --porcelain` and returns (clean, detail).
// Non-git repos return (true, "not a git repo") so the check is a
// no-op there, keeping the gate useful before a repository is initialized.
func gitIsClean(repoRoot string) (bool, string) {
	gitDir := filepath.Join(repoRoot, ".git")
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		return true, "not a git repo"
	}
	out, err := runGitPorcelain(repoRoot)
	if err != nil {
		return false, "git status failed: " + err.Error()
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return true, ""
	}
	lines := strings.Count(trimmed, "\n") + 1
	return false, fmt.Sprintf("%d unstaged/untracked change(s)", lines)
}

// runGitPorcelain is factored out so tests can replace it if needed.
// Kept minimal: no env scrubbing (git porcelain is read-only) but we
// explicitly set Dir so CI environment doesn't leak.
func runGitPorcelain(repoRoot string) (string, error) {
	cmd := osExecCommand("git", "status", "--porcelain")
	cmd.Dir = repoRoot
	b, err := boundedexec.Output(cmd, maxGitPorcelainBytes)
	return string(b), err
}

// osExecCommand is an indirection so the post-task-check test suite
// can stub git calls without actually invoking git binaries in CI.
// Defaults to exec.Command.
var osExecCommand = exec.Command
