package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// `git commit -a` runs the pre-commit hook against a temporary index named by
// GIT_INDEX_FILE; the staged gate must read that index, never .git/index.
func TestCollectGitWritePathsReadsGitsCommitIndex(t *testing.T) {
	repo := initGitRepo(t)
	gitWrite(t, repo, "f.txt", "base\n")
	gitRun(t, repo, "add", "f.txt")
	gitRun(t, repo, "commit", "-q", "-m", "base")
	gitWrite(t, repo, "f.txt", "changed\n")
	commitIndex := filepath.Join(".git", "next-index-4242.lock")
	for _, args := range [][]string{{"read-tree", "HEAD"}, {"add", "f.txt"}} {
		command := exec.Command("git", args...)
		command.Dir = repo
		command.Env = append(os.Environ(), "GIT_INDEX_FILE="+commitIndex)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	paths, _, err := CollectGitWritePaths(repo, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	requireGitPaths(t, paths, nil)
	t.Setenv("GIT_INDEX_FILE", commitIndex)
	paths, _, err = CollectGitWritePaths(repo, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	requireGitPaths(t, paths, []string{"f.txt"})
	outside := filepath.Join(t.TempDir(), "index")
	if err := os.WriteFile(outside, []byte("not an index"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_INDEX_FILE", outside)
	paths, _, err = CollectGitWritePaths(repo, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	requireGitPaths(t, paths, nil)
}
