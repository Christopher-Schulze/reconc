package runtime

import (
	"testing"
)

// A submodule bump must reach path rules even when repository configuration
// asks Git to ignore submodules, because `git commit` still records it.
func TestCollectGitWritePathsIncludesIgnoredSubmoduleBumps(t *testing.T) {
	repo := initGitRepo(t)
	gitWrite(t, repo, "README.md", "base\n")
	gitRun(t, repo, "add", "README.md")
	gitRun(t, repo, "commit", "-q", "-m", "base")
	gitRun(t, repo, "config", "diff.ignoreSubmodules", "all")
	gitRun(t, repo, "update-index", "--add", "--cacheinfo", "160000,1111111111111111111111111111111111111111,vendor/sm")
	paths, _, err := CollectGitWritePaths(repo, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	requireGitPaths(t, paths, []string{"vendor/sm"})
	// git commit itself honors the ignore setting when deciding emptiness.
	gitRun(t, repo, "-c", "diff.ignoreSubmodules=none", "commit", "-q", "-m", "bump")
	paths, _, err = CollectGitWritePaths(repo, false, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	requireGitPaths(t, paths, []string{"vendor/sm"})
}
