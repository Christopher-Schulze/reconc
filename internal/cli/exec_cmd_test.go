package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/commandproof"
)

func TestExecStagedProofSatisfiesCIWithoutToolHook(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: tests-must-pass\n    kind: require_command_success\n    when_paths: ['src/**']\n    commands: ['go version']\n    mode: block\n    message: tests must pass\n")
	initGitRepo(t, repo)
	gitCommand(t, repo, "add", "AGENTS.md", "policies", ".reconc")
	gitCommand(t, repo, "commit", "-m", "initial")
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repo, "add", "src/main.go")

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"exec", repo, "--staged", "--", "go", "version"}, "0.8.5-test", &stdout, &stderr); err != nil {
		t.Fatalf("exec staged: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	proofs, err := commandproof.LoadCurrentSuccesses(repo, time.Now())
	if err != nil || len(proofs) != 1 {
		t.Fatalf("published command proofs = %+v, err=%v", proofs, err)
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{"ci", repo, "--staged"}, "0.8.5-test", &stdout, &stderr); err != nil {
		t.Fatalf("ci did not accept staged command proof: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "tests-must-pass") {
		t.Fatalf("command proof did not satisfy policy:\n%s", stdout.String())
	}
}

func TestCIStagedRejectsExplicitCommandOutcomeFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "success after staged", args: []string{"ci", "--staged", "--command-success", "go version"}},
		{name: "failure before staged", args: []string{"ci", "--command-failure", "go version", "--staged"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(test.args, "0.8.5-test", &stdout, &stderr)
			if ExitCode(err) != 1 || !strings.Contains(err.Error(), "reconc exec --staged") {
				t.Fatalf("staged CI accepted explicit command outcome: err=%v", err)
			}
		})
	}
}

func TestCIHelpExplainsStagedCommandEvidenceBoundary(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"ci", "--help"}, "0.8.5-test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "--staged rejects explicit command outcome flags") {
		t.Fatalf("CI help omitted staged evidence boundary:\n%s", stdout.String())
	}
}

func TestExecProofDoesNotSurviveIndexChange(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: tests-must-pass\n    kind: require_command_success\n    when_paths: ['src/**']\n    commands: ['go version']\n    mode: block\n    message: tests must pass\n")
	initGitRepo(t, repo)
	gitCommand(t, repo, "add", "AGENTS.md", "policies", ".reconc")
	gitCommand(t, repo, "commit", "-m", "initial")
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repo, "add", "src/main.go")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"exec", repo, "--staged", "--", "go", "version"}, "0.8.5-test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "other.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repo, "add", "src/other.go")
	stdout.Reset()
	stderr.Reset()
	err := Run([]string{"ci", repo, "--staged"}, "0.8.5-test", &stdout, &stderr)
	if ExitCode(err) != 2 || !strings.Contains(stdout.String(), "tests-must-pass") {
		t.Fatalf("changed index accepted stale proof: err=%v\n%s", err, stdout.String())
	}
}

func TestExecRejectsSuccessfulCommandThatChangesIndex(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	initGitRepo(t, repo)
	gitCommand(t, repo, "add", "AGENTS.md", "policies", ".reconc")
	gitCommand(t, repo, "commit", "-m", "initial")
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repo, "add", "src/main.go")
	var stdout, stderr bytes.Buffer
	err := Run([]string{"exec", repo, "--staged", "--", "git", "reset", "HEAD", "--", "src/main.go"}, "0.8.5-test", &stdout, &stderr)
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "staged postcondition") {
		t.Fatalf("index-mutating command accepted: err=%v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
}

func TestExecStagedFailurePublishesNoProof(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	initGitRepo(t, repo)
	gitCommand(t, repo, "add", "AGENTS.md", "policies", ".reconc")
	gitCommand(t, repo, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repo, "candidate.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repo, "add", "candidate.txt")

	var stdout, stderr bytes.Buffer
	err := Run([]string{"exec", repo, "--staged", "--shell", "--", "exit 7"}, "0.8.5-test", &stdout, &stderr)
	if ExitCode(err) != 7 {
		t.Fatalf("exit code = %d, want 7; err=%v", ExitCode(err), err)
	}
	proofs, loadErr := commandproof.LoadCurrentSuccesses(repo, time.Now())
	if loadErr != nil || len(proofs) != 0 {
		t.Fatalf("failed command published proofs = %+v, err=%v", proofs, loadErr)
	}
}

func TestExecPropagatesRealShellExitCode(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	var stdout, stderr bytes.Buffer
	err := Run([]string{"exec", repo, "--shell", "--", "exit 7"}, "0.8.5-test", &stdout, &stderr)
	if ExitCode(err) != 7 {
		t.Fatalf("exit code = %d, want 7; err=%v", ExitCode(err), err)
	}
}

func TestExecHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"exec", "--help"}, "0.8.5-test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Usage: reconc exec", "--staged", "--shell"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("exec help missing %q:\n%s", expected, stdout.String())
		}
	}
}

func TestRenderDirectCommandPreservesPolicyString(t *testing.T) {
	got := renderDirectCommand([]string{"codebase/scripts/tests/run-root-tests.sh", "build", "two words", "it's"})
	want := `codebase/scripts/tests/run-root-tests.sh build 'two words' 'it'"'"'s'`
	if got != want {
		t.Fatalf("rendered command = %q, want %q", got, want)
	}
}

func gitCommand(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
