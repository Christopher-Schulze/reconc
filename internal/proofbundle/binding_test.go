package proofbundle_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"reconc.dev/reconc/internal/proofbundle"
)

func TestVerifyRepositoryMatchesFreshCandidateAndRejectsDrift(t *testing.T) {
	repo := proofRepo(t, "rules: []\n", map[string]string{"src/main.go": "package main\n"})
	initGit(t, repo)
	bundle, err := proofbundle.Generate(repo, "test")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := proofbundle.VerifyRepository(bundle, repo)
	if err != nil || !binding.Match || len(binding.Mismatches) != 0 {
		t.Fatalf("initial binding = %+v, %v", binding, err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "main.go"), []byte("package main\n\nconst drift = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binding, err = proofbundle.VerifyRepository(bundle, repo)
	if err != nil || binding.Match {
		t.Fatalf("drift binding = %+v, %v", binding, err)
	}
	for _, field := range []string{"candidate.fingerprint", "candidate.worktree_hash", "candidate.dirty_paths", "completion_digest"} {
		if !slices.Contains(binding.Mismatches, field) {
			t.Errorf("drift mismatches omit %s: %v", field, binding.Mismatches)
		}
	}
}
