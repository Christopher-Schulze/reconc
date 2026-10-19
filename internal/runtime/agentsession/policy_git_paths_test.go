package agentsession

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNestedPolicyStatusRetainsGlobalCleanlinessAndLocalPaths(t *testing.T) {
	repo := t.TempDir()
	gitInitHelper(t, repo)
	root := filepath.Join(repo, "module")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := policyRootGitStatus(root, "A  module/ protected.txt\x00R  module/new.txt\x00outside-old.txt\x00?? sibling.txt\x00")
	if err != nil {
		t.Fatal(err)
	}
	paths := dirtyPathsFromStatus(status)
	if strings.Join(paths, "|") != " protected.txt|new.txt" {
		t.Fatalf("policy write paths = %q; status=%q", paths, status)
	}
	if gitWorktreeMatchesIndex(status) {
		t.Fatal("an untracked sibling outside the policy root was concealed")
	}
	if strings.HasPrefix(gitHeadFingerprint(root), "error:") {
		t.Fatal("nested policy root could not resolve its enclosing Git identity")
	}
}
