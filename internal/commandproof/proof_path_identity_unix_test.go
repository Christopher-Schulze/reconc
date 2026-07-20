//go:build !windows

package commandproof

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalRepoRootResolvesSymlinkIdentity(t *testing.T) {
	repo := newProofRepo(t)
	alias := filepath.Join(t.TempDir(), "repo-alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	resolved, err := canonicalRepoRoot(alias)
	if err != nil {
		t.Fatal(err)
	}
	repoInfo, err := os.Stat(repo)
	if err != nil {
		t.Fatal(err)
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(repoInfo, resolvedInfo) {
		t.Fatalf("resolved root %q is not the repository identity %q", resolved, repo)
	}
}
