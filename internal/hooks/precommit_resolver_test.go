package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGeneratedGitPreCommitExecutesTrustedStableArtifact(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("generated POSIX pre-commit execution requires a POSIX host")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("generated pre-commit supports amd64 and arm64 release artifacts")
	}

	repo := t.TempDir()
	command := exec.Command("git", "init", "--quiet", repo)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	dist := filepath.Join(repo, "tools", "reconc", "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "arguments.log")
	binary := filepath.Join(dist, "reconc-"+runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$RECONC_PRECOMMIT_LOG\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte(generateGitPreCommit().Content), 0o755); err != nil {
		t.Fatal(err)
	}

	command = exec.Command(hook)
	command.Dir = repo
	command.Env = append(os.Environ(), "RECONC_PRECOMMIT_LOG="+logPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("pre-commit: %v: %s", err, output)
	} else if len(output) != 0 {
		t.Fatalf("pre-commit emitted diagnostics: %q", output)
	}
	arguments, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(arguments)), "\n")
	if len(lines) != 3 || lines[0] != "ci" || lines[2] != "--staged" {
		t.Fatalf("stable artifact arguments = %#v", lines)
	}
	resolvedRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	resolvedArgument, err := filepath.EvalSymlinks(lines[1])
	if err != nil {
		t.Fatal(err)
	}
	if resolvedArgument != resolvedRepo {
		t.Fatalf("stable artifact repository = %q, want %q", resolvedArgument, resolvedRepo)
	}
}
