//go:build !windows

package agentsession

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// recordGitStatusCalls puts a logging git wrapper first on PATH and returns a
// reader for the status invocations it observed since the last read.
func recordGitStatusCalls(t *testing.T) func() []string {
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
	return func() []string {
		body, err := os.ReadFile(log)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.Remove(log); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		var status []string
		for _, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, " status --porcelain") {
				status = append(status, line)
			}
		}
		return status
	}
}

func TestStopObservesGitStatusOnlyAtEvaluationBarriers(t *testing.T) {
	repo := setupPolicyRepo(t)
	gitInitHelper(t, repo)
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "a.go"), []byte("package src\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InitializeSessionState(repo, "observe"); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "observe", func(state SessionState) SessionState {
		state = AppendWritePath(state, "src/a.go")
		state.Claims = append(state.Claims, "ci-green")
		return state
	}); err != nil {
		t.Fatal(err)
	}
	statusCalls := recordGitStatusCalls(t)

	cold := RunStop(repo, []byte(`{"session_id":"observe","runtime":"codex"}`))
	if cold.ExitCode != 0 || cold.Stdout != "" {
		t.Fatalf("clean Stop was not released: %+v", cold)
	}
	coldCalls := statusCalls()
	for _, call := range coldCalls {
		if strings.Contains(call, "--untracked-files=all") {
			t.Fatalf("non-terminal Stop ran the complete untracked scan: %q", coldCalls)
		}
	}
	if len(coldCalls) != 2 {
		t.Fatalf("evaluating Stop observed Git status %d times, want one before and one after evaluation: %q", len(coldCalls), coldCalls)
	}

	warm := RunStop(repo, []byte(`{"session_id":"observe","runtime":"codex"}`))
	if warm.ExitCode != 0 || warm.Stdout != "" {
		t.Fatalf("warm clean Stop was not released: %+v", warm)
	}
	if warmCalls := statusCalls(); len(warmCalls) != 1 {
		t.Fatalf("report-reusing Stop observed Git status %d times, want one: %q", len(warmCalls), warmCalls)
	}
}
