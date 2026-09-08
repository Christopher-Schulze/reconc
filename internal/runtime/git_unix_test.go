//go:build !windows

package runtime

import "testing"

func TestCollectGitWritePathsPreservesNewline(t *testing.T) {
	repo := initGitRepo(t)
	relative := "line one\nline two.txt"
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

func TestCollectGitWritePathsRenamePreservesNewlines(t *testing.T) {
	repo := initGitRepo(t)
	oldPath, newPath := "protected/old\nname.txt", "allowed/new\nname.txt"
	gitWrite(t, repo, oldPath, "original\n")
	gitRun(t, repo, "add", "--", oldPath)
	gitRun(t, repo, "commit", "-q", "-m", "initial")
	gitRename(t, repo, oldPath, newPath)
	gitRun(t, repo, "add", "-A")

	paths, _, err := CollectGitWritePaths(repo, true, "", "")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	requireGitPaths(t, paths, []string{newPath, oldPath})
}
