package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIStagedRequiresMatchingWorktreeContents(t *testing.T) {
	tests := []struct {
		name        string
		stagedBody  string
		workingBody string
		untracked   bool
		noGit       bool
		rangeMode   bool
		wantExit    int
	}{
		{name: "unstaged correction cannot conceal staged debt", stagedBody: "package main\n// TODO: implement\n", workingBody: "package main\n", wantExit: 2},
		{name: "untracked input blocks staged candidate", stagedBody: "package main\n", untracked: true, wantExit: 2},
		{name: "unavailable Git fails closed before candidate evaluation", stagedBody: "package main\n", noGit: true, wantExit: 1},
		{name: "matching staged candidate passes", stagedBody: "package main\n"},
		{name: "range mode retains worktree semantics", stagedBody: "package main\n// TODO: implement\n", workingBody: "package main\n", rangeMode: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := makeCheckRepo(t, "rules:\n  - id: source-clean\n    kind: require_assurance\n    mode: block\n    when_paths: ['**/*.go']\n    message: source must be complete\n    assurance:\n      - id: hygiene\n        type: source_hygiene\n        scan_paths: ['**/*.go']\n")
			initGitRepo(t, repo)
			gitCommand(t, repo, "add", ".")
			gitCommand(t, repo, "commit", "-m", "initial")
			path := filepath.Join(repo, "main.go")
			if err := os.WriteFile(path, []byte(test.stagedBody), 0o644); err != nil {
				t.Fatal(err)
			}
			gitCommand(t, repo, "add", "main.go")
			if test.workingBody != "" {
				if err := os.WriteFile(path, []byte(test.workingBody), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if test.untracked {
				if err := os.WriteFile(filepath.Join(repo, "untracked.go"), []byte("package main\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if test.noGit {
				t.Setenv("PATH", t.TempDir())
			}
			for _, format := range []string{"text", "json", "sarif", "junit"} {
				var stdout, stderr bytes.Buffer
				args := []string{"ci", repo, "--staged", "--format", format}
				if test.rangeMode {
					args = []string{"ci", repo, "--base", "HEAD", "--format", format}
				}
				err := Run(args, "test", &stdout, &stderr)
				if ExitCode(err) != test.wantExit {
					t.Fatalf("%s exit = %d, want %d; err=%v stdout=%s", format, ExitCode(err), test.wantExit, err, stdout.String())
				}
				if test.wantExit == 0 {
					continue
				}
				message := stdout.String()
				if err != nil {
					message += err.Error()
				}
				if test.wantExit == 2 && (!strings.Contains(message, "worktree matching the index") || !strings.Contains(message, "reconc ci --staged")) {
					t.Fatalf("%s mismatch omitted remediation: %s", format, message)
				}
				if test.noGit && !strings.Contains(message, "git") {
					t.Fatalf("%s unavailable Git error omitted its cause: %s", format, message)
				}
				if format == "sarif" || format == "junit" {
					assertNativeOperationalError(t, format, stdout.Bytes())
				}
			}
		})
	}
}
