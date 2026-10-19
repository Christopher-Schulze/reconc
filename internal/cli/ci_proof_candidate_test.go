package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	productruntime "reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestCIStagedDoesNotReuseProofLoadedBeforeItsCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX Git interception fixture")
	}
	repo := makeCheckRepo(t, "rules:\n  - id: tests-required\n    kind: require_command_success\n    when_paths: ['src/**']\n    commands: ['true']\n    mode: block\n    message: verify this candidate\n")
	initGitRepo(t, repo)
	gitCommand(t, repo, "add", ".")
	gitCommand(t, repo, "commit", "-qm", "initial")
	path := filepath.Join(repo, "src", "file.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("candidate A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repo, "add", "src/file.txt")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"exec", repo, "--staged", "--", "true"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("record proof A: %v %s", err, stderr.String())
	}
	interceptFirstGitStatus(t, repo, path)
	stdout.Reset()
	stderr.Reset()
	err := Run([]string{"ci", repo, "--staged"}, "test", &stdout, &stderr)
	if ExitCode(err) != 2 || !strings.Contains(stdout.String(), "tests-required") {
		t.Fatalf("candidate B reused proof A: err=%v stdout=%s stderr=%s", err, &stdout, &stderr)
	}
}

func TestCIStagedRejectsSessionHistoryWithReservedProofEpoch(t *testing.T) {
	repo := makeCheckRepo(t, "rules:\n  - id: tests-required\n    kind: require_command_success\n    when_paths: ['src/**']\n    commands: ['true']\n    mode: block\n    message: verify this candidate\n")
	initGitRepo(t, repo)
	gitCommand(t, repo, "add", ".")
	gitCommand(t, repo, "commit", "-qm", "initial")
	if err := os.Mkdir(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src/file.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repo, "add", ".")
	state, err := agentsession.InitializeSessionState(repo, "ordinary-history")
	if err != nil {
		t.Fatal(err)
	}
	state = agentsession.AppendCommandResult(state, agentsession.CommandResult{
		Command: "true", Outcome: productruntime.CommandOutcomeSuccess, EvidenceEpoch: productruntime.ExplicitEvidenceEpoch,
	})
	if err := agentsession.SaveSessionState(state); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err = Run([]string{"ci", repo, "--staged"}, "test", &stdout, &stderr)
	if ExitCode(err) != 2 || !strings.Contains(stdout.String(), "tests-required") {
		t.Fatalf("session history impersonated a staged proof: %v stdout=%s stderr=%s", err, &stdout, &stderr)
	}
}

func TestCINestedPolicyRootEnforcesItsDenyRule(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	checkout := t.TempDir()
	repo := filepath.Join(checkout, "module")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	policy := "rules:\n  - id: protected-path\n    kind: deny_write\n    paths: ['protected.txt']\n    mode: block\n    message: protected path\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, checkout)
	gitCommand(t, checkout, "add", ".")
	gitCommand(t, checkout, "commit", "-qm", "initial")
	if err := os.WriteFile(filepath.Join(repo, "protected.txt"), []byte("change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, checkout, "add", ".")
	assertNestedCIDenied(t, []string{"ci", repo, "--staged"})
	gitCommand(t, checkout, "commit", "-qm", "changed")
	assertNestedCIDenied(t, []string{"ci", repo, "--base", "HEAD~1", "--head", "HEAD"})
}

func assertNestedCIDenied(t *testing.T, args []string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run(args, "test", &stdout, &stderr)
	if ExitCode(err) != 2 || !strings.Contains(stdout.String(), "protected-path") {
		t.Fatalf("nested write bypassed policy: %v stdout=%s stderr=%s", err, &stdout, &stderr)
	}
}

func interceptFirstGitStatus(t *testing.T, repo, path string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	marker := filepath.Join(directory, "changed")
	script := fmt.Sprintf("#!/bin/sh\ncase \" $* \" in *' status '*)\nif [ ! -f %s ]; then\nprintf 'candidate B\\n' > %s\n%s -C %s add src/file.txt || exit $?\n: > %s\nfi;; esac\nexec %s \"$@\"\n",
		quoteCommandArgument(marker), quoteCommandArgument(path), quoteCommandArgument(git), quoteCommandArgument(repo), quoteCommandArgument(marker), quoteCommandArgument(git))
	if err := os.WriteFile(filepath.Join(directory, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
}
