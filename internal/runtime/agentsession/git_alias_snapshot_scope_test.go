//go:build !windows

package agentsession

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime"
)

func countGitAliasSnapshots(t *testing.T) func() int {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "git-calls.log")
	wrapper := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexec '" + realGit + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		body, _ := os.ReadFile(log)
		_ = os.Remove(log)
		return strings.Count(string(body), "--get-regexp ^alias")
	}
}

func TestPreDecisionCapturesGitAliasesOnlyForGitCommands(t *testing.T) {
	repo := setupPolicyRepo(t)
	gitInitHelper(t, repo)
	if _, err := InitializeSessionState(repo, "alias-scope"); err != nil {
		t.Fatal(err)
	}
	snapshots := countGitAliasSnapshots(t)
	run := func(id, command string) {
		payload := `{"session_id":"alias-scope","tool_use_id":"` + id + `","tool_name":"Bash","tool_input":{"command":"` + command + `"}}`
		runPreDecisionResolvedWithEvaluatorAndStopCache(repo, []byte(payload), false, true, runtime.NewEvaluator(), nil)
	}
	run("call-ls", "ls -la")
	if count := snapshots(); count != 0 {
		t.Fatalf("a command without git captured %d alias snapshots", count)
	}
	run("call-git", "git status --short")
	if count := snapshots(); count == 0 {
		t.Fatal("a git command was evaluated without its alias snapshot")
	}
}
