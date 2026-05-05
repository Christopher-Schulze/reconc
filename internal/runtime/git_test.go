package runtime

import (
	stderrors "errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	rerrors "reconc.dev/reconc/internal/errors"
)

// initGitRepo creates a fresh temp dir, runs `git init`, sets a
// minimal user identity (so commits work), and returns the repo path.
// Tests that need a working git repo use this helper.
func initGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed; skipping git-integration test")
	}
	repo := t.TempDir()
	cmds := [][]string{
		{"init", "--quiet", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	}
	for _, args := range cmds {
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	return repo
}

func gitWrite(t *testing.T, repo, rel, content string) {
	t.Helper()
	full := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitRun(t *testing.T, repo string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = repo
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestCollectGitWritePathsRejectsNeither(t *testing.T) {
	_, _, err := CollectGitWritePaths(".", false, "", "")
	if err == nil {
		t.Fatal("expected error when neither staged nor base/head specified")
	}
	var ge *rerrors.GitError
	if !stderrors.As(err, &ge) {
		t.Errorf("expected *GitError, got %T", err)
	}
}

func TestCollectGitWritePathsRejectsBoth(t *testing.T) {
	_, _, err := CollectGitWritePaths(".", true, "main", "")
	if err == nil {
		t.Fatal("expected error when both staged and base specified")
	}
}

func TestCollectGitWritePathsStagedEmpty(t *testing.T) {
	repo := initGitRepo(t)
	paths, meta, err := CollectGitWritePaths(repo, true, "", "")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("expected no staged paths in fresh repo, got %v", paths)
	}
	if meta.Mode != GitModeStaged {
		t.Errorf("expected mode=staged, got %s", meta.Mode)
	}
	if meta.WritePathCount != 0 {
		t.Errorf("metadata count mismatch: %d", meta.WritePathCount)
	}
}

func TestCollectGitWritePathsStagedNonEmpty(t *testing.T) {
	repo := initGitRepo(t)
	gitWrite(t, repo, "src/main.go", "package main\n")
	gitWrite(t, repo, "tests/main_test.go", "package main\n")
	gitRun(t, repo, "add", "src/main.go", "tests/main_test.go")

	paths, meta, err := CollectGitWritePaths(repo, true, "", "")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(paths) != 2 {
		t.Errorf("expected 2 staged paths, got %v", paths)
	}
	// Verify POSIX-style paths
	for _, p := range paths {
		if strings.Contains(p, "\\") {
			t.Errorf("expected POSIX path, got %s", p)
		}
	}
	if meta.WritePathCount != 2 {
		t.Errorf("metadata count: %d", meta.WritePathCount)
	}
}

func TestCollectGitWritePathsRangeMode(t *testing.T) {
	repo := initGitRepo(t)
	// Make initial commit
	gitWrite(t, repo, "init.txt", "a\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "initial")

	// Make a second commit
	gitWrite(t, repo, "src/x.go", "package x\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "second")

	paths, meta, err := CollectGitWritePaths(repo, false, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("expected at least one path between HEAD~1..HEAD")
	}
	if meta.Mode != GitModeRange {
		t.Errorf("expected mode=range, got %s", meta.Mode)
	}
	if meta.Base != "HEAD~1" {
		t.Errorf("expected base=HEAD~1, got %s", meta.Base)
	}
	if meta.Head != "HEAD" {
		t.Errorf("expected head=HEAD, got %s", meta.Head)
	}
}

func TestCollectGitWritePathsRangeDefaultsHead(t *testing.T) {
	repo := initGitRepo(t)
	gitWrite(t, repo, "x.txt", "x\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "first")

	_, meta, err := CollectGitWritePaths(repo, false, "HEAD", "")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if meta.Head != "HEAD" {
		t.Errorf("expected default head=HEAD, got %s", meta.Head)
	}
}

func TestCollectGitWritePathsBadRefReturnsGitError(t *testing.T) {
	repo := initGitRepo(t)
	_, _, err := CollectGitWritePaths(repo, false, "no-such-ref", "")
	if err == nil {
		t.Fatal("expected error for bad ref")
	}
	var ge *rerrors.GitError
	if !stderrors.As(err, &ge) {
		t.Errorf("expected *GitError, got %T", err)
	}
}

func TestCollectGitWritePathsNonGitDirReturnsError(t *testing.T) {
	repo := t.TempDir() // no `git init`
	_, _, err := CollectGitWritePaths(repo, true, "", "")
	if err == nil {
		t.Fatal("expected error in non-git dir")
	}
}
