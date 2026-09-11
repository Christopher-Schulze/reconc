package runtime

import (
	stderrors "errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
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
	// TempDir cleanup must not race maintenance detached from a completed commit.
	commandArgs := append([]string{"-c", "maintenance.autoDetach=false", "-c", "gc.autoDetach=false"}, args...)
	c := exec.Command("git", commandArgs...)
	c.Dir = repo
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func gitRename(t *testing.T, repo, oldPath, newPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, newPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(repo, oldPath), filepath.Join(repo, newPath)); err != nil {
		t.Fatal(err)
	}
}

func requireGitPaths(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("paths=%q, want %q", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("paths=%q, want %q", got, want)
		}
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

func TestCollectGitWritePathsStagedRenameIncludesBothPaths(t *testing.T) {
	repo := initGitRepo(t)
	oldPath, newPath := "protected/source.txt", "allowed/destination.txt"
	gitWrite(t, repo, oldPath, "original\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "initial")
	gitRename(t, repo, oldPath, newPath)
	gitRun(t, repo, "add", "-A")

	for _, setting := range []string{"true", "false"} {
		t.Run("diff.renames="+setting, func(t *testing.T) {
			gitRun(t, repo, "config", "diff.renames", setting)
			paths, metadata, err := CollectGitWritePaths(repo, true, "", "")
			if err != nil {
				t.Fatalf("collect: %v", err)
			}
			requireGitPaths(t, paths, []string{newPath, oldPath})
			if metadata.GitCommand != "git diff --cached --no-renames --name-only -z" {
				t.Fatalf("git command=%q", metadata.GitCommand)
			}
		})
	}
}

func TestCollectGitWritePathsRangeRenameIncludesBothPaths(t *testing.T) {
	repo := initGitRepo(t)
	oldPath, newPath := "protected/source.txt", "allowed/destination.txt"
	gitWrite(t, repo, oldPath, "original\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "initial")
	gitRename(t, repo, oldPath, newPath)
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "rename")

	for _, setting := range []string{"true", "false"} {
		t.Run("diff.renames="+setting, func(t *testing.T) {
			gitRun(t, repo, "config", "diff.renames", setting)
			paths, metadata, err := CollectGitWritePaths(repo, false, "HEAD~1", "HEAD")
			if err != nil {
				t.Fatalf("collect: %v", err)
			}
			requireGitPaths(t, paths, []string{newPath, oldPath})
			if metadata.GitCommand != "git diff HEAD~1...HEAD --no-renames --name-only -z" {
				t.Fatalf("git command=%q", metadata.GitCommand)
			}
		})
	}
}

func TestCollectGitWritePathsModifiedRenameIncludesBothPaths(t *testing.T) {
	repo := initGitRepo(t)
	oldPath, newPath := "protected/source.txt", "allowed/destination.txt"
	gitWrite(t, repo, oldPath, "original\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "initial")
	gitRename(t, repo, oldPath, newPath)
	gitWrite(t, repo, newPath, "modified\n")
	gitRun(t, repo, "add", "-A")

	paths, _, err := CollectGitWritePaths(repo, true, "", "")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	requireGitPaths(t, paths, []string{newPath, oldPath})
}

func TestCollectGitWritePathsRenamePreservesSpecialNames(t *testing.T) {
	repo := initGitRepo(t)
	oldPath, newPath := "protected/old \tüber name.txt", "allowed/new \tüber name.txt"
	if goruntime.GOOS == "windows" {
		// Windows rejects control characters in filenames; preserve Unicode,
		// whitespace, and literal Git pathspec punctuation in the native case.
		oldPath, newPath = "protected/old [über] name.txt", "allowed/new [über] name.txt"
	}
	gitWrite(t, repo, oldPath, "original\n")
	gitRun(t, repo, "add", "--", oldPath)
	gitRun(t, repo, "commit", "-q", "-m", "initial")
	gitRename(t, repo, oldPath, newPath)
	gitRun(t, repo, "add", "-A")

	paths, _, err := CollectGitWritePaths(repo, true, "", "")
	if err != nil {
		t.Fatalf("staged collect: %v", err)
	}
	requireGitPaths(t, paths, []string{newPath, oldPath})

	gitRun(t, repo, "commit", "-q", "-m", "rename")
	paths, _, err = CollectGitWritePaths(repo, false, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("range collect: %v", err)
	}
	requireGitPaths(t, paths, []string{newPath, oldPath})
}

func TestCollectGitWritePathsRenameTriggersSourcePolicy(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: protected-source\n    kind: deny_write\n    paths: ['protected/**']\n    mode: block\n    message: source path is protected\n")
	runGit := func(args ...string) { gitRun(t, repo, args...) }
	runGit("init", "--quiet", "-b", "main")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test")
	oldPath, newPath := "protected/source.txt", "allowed/destination.txt"
	gitWrite(t, repo, oldPath, "original\n")
	runGit("add", ".")
	runGit("commit", "-q", "-m", "initial")
	gitRename(t, repo, oldPath, newPath)
	runGit("add", "-A")

	paths, _, err := CollectGitWritePaths(repo, true, "", "")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	requireGitPaths(t, paths, []string{newPath, oldPath})
	report, err := CheckRepoPolicy(repo, ExecutionInputs{WritePaths: paths})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Decision != DecisionBlock {
		t.Fatalf("decision=%s, want block; violations=%+v", report.Decision, report.Violations)
	}
	for _, violation := range report.Violations {
		if violation.RuleID == "protected-source" {
			return
		}
	}
	t.Fatalf("source-path violation missing: %+v", report.Violations)
}

func TestCollectGitWritePathsUnicodeAndSpacedNames(t *testing.T) {
	repo := initGitRepo(t)
	// Default core.quotepath would octal-escape these in non -z output;
	// verbatim bytes must come back so policy globs can match them.
	names := []string{"docs/über plan.md", "src/héllo.go", "with space.txt"}
	for _, name := range names {
		gitWrite(t, repo, name, "content\n")
	}
	gitRun(t, repo, "add", ".")

	paths, meta, err := CollectGitWritePaths(repo, true, "", "")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(paths) != len(names) {
		t.Fatalf("expected %d paths, got %v", len(names), paths)
	}
	got := map[string]bool{}
	for _, p := range paths {
		got[p] = true
	}
	for _, name := range names {
		if !got[name] {
			t.Errorf("expected verbatim path %q in %v", name, paths)
		}
	}
	if meta.WritePathCount != len(names) {
		t.Errorf("metadata count: %d", meta.WritePathCount)
	}
}

func TestCollectGitWritePathsPreservesLeadingSpace(t *testing.T) {
	repo := initGitRepo(t)
	relative := " leading space.txt"
	gitWrite(t, repo, relative, "content\n")
	gitRun(t, repo, "add", "--", relative)

	paths, _, err := CollectGitWritePaths(repo, true, "", "")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(paths) != 1 || paths[0] != relative {
		t.Fatalf("filename framing lost information: got %q want %q", paths, relative)
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
