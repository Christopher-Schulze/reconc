package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedReferenceSourceFailsClosedWhenSourceTreeIsMissing(t *testing.T) {
	root := t.TempDir()
	if err := validateGeneratedReferenceSource(filepath.Join(root, "missing-source")); err == nil {
		t.Fatal("missing source tree must fail closed")
	}
}

func TestGitBackedAuditsFailClosedOnBrokenRepository(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, failures := collectGitDiffFiles(root); len(failures) == 0 {
		t.Fatal("agent-quality Git discovery failure was hidden")
	}
	if failures := auditRepoCleanliness(root); len(failures) == 0 {
		t.Fatal("repo-cleanliness Git discovery failure was hidden")
	}
}
