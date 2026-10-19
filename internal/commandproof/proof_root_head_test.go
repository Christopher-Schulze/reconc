package commandproof

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureStagedCleanSeesUntrackedPathsOutsideASubdirectoryRoot(t *testing.T) {
	repo := newProofRepo(t)
	subdirectory := filepath.Join(repo, "service")
	if err := os.MkdirAll(subdirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "top-level-untracked.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := CaptureStagedClean(subdirectory)
	if err == nil || !strings.Contains(err.Error(), "top-level-untracked.txt") {
		t.Fatalf("an untracked file outside the subdirectory root was ignored: %v", err)
	}
}

func TestGitHeadReportsUnbornOnlyForAMissingSymbolicTarget(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	head, err := gitHead(repo)
	if err != nil || head != "UNBORN" {
		t.Fatalf("fresh repository HEAD = %q, %v", head, err)
	}
	// A branch ref file with garbage content is corrupt, not unborn:
	// rev-parse fails while the symbolic target exists.
	ref, err := gitOutput(repo, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	refPath := filepath.Join(repo, ".git", filepath.FromSlash(ref))
	if err := os.MkdirAll(filepath.Dir(refPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(refPath, []byte("not-an-object-id\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if head, err := gitHead(repo); err == nil || head == "UNBORN" {
		t.Fatalf("broken HEAD reported as %q, %v", head, err)
	}
}
