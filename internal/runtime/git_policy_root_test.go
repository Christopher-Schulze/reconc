package runtime

import (
	"path/filepath"
	"testing"
)

func TestGitDiffPathsUseThePolicyRoot(t *testing.T) {
	repo := initGitRepo(t)
	gitWrite(t, repo, "initial.txt", "initial\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "initial")
	gitWrite(t, repo, "module/ protected [name].txt", "protected\n")
	gitWrite(t, repo, "sibling.txt", "outside policy\n")
	gitRun(t, repo, "add", ".")
	root := filepath.Join(repo, "module")
	paths, metadata, err := CollectGitWritePaths(root, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	requireGitPaths(t, paths, []string{" protected [name].txt"})
	if metadata.WritePathCount != 1 {
		t.Fatalf("staged path count = %d", metadata.WritePathCount)
	}
	gitRun(t, repo, "commit", "-q", "-m", "nested change")
	paths, metadata, err = CollectGitWritePaths(root, false, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	requireGitPaths(t, paths, []string{" protected [name].txt"})
	if metadata.WritePathCount != 1 {
		t.Fatalf("range path count = %d", metadata.WritePathCount)
	}
}
