package proofbundle

import (
	"path/filepath"
	"slices"

	"reconc.dev/reconc/internal/completiongate"
)

// RepositoryBinding reports whether a valid received bundle describes one
// fresh local completion snapshot. It intentionally exposes field names, not
// local repository values.
type RepositoryBinding struct {
	Match      bool     `json:"match"`
	Mismatches []string `json:"mismatches"`
}

// VerifyRepository compares a valid bundle with one fresh read-only local
// completion evaluation.
func VerifyRepository(bundle *Bundle, repo string) (RepositoryBinding, error) {
	if err := Verify(bundle); err != nil {
		return RepositoryBinding{}, err
	}
	report, err := completiongate.Evaluate(repo, completiongate.Options{})
	if err != nil {
		return RepositoryBinding{}, err
	}
	current := candidateIdentity(filepath.Clean(report.RepoRoot), report.Candidate)
	mismatches := candidateMismatches(bundle.Candidate, current)
	if bundle.CompletionDigest != report.Digest {
		mismatches = append(mismatches, "completion_digest")
	}
	if bundle.Decision != report.Decision {
		mismatches = append(mismatches, "decision")
	}
	return RepositoryBinding{Match: len(mismatches) == 0, Mismatches: mismatches}, nil
}

func candidateMismatches(received, current Candidate) []string {
	checks := []struct {
		name  string
		match bool
	}{
		{"candidate.fingerprint", received.Fingerprint == current.Fingerprint},
		{"candidate.policy_lock_hash", received.PolicyLockHash == current.PolicyLockHash},
		{"candidate.git_available", received.GitAvailable == current.GitAvailable},
		{"candidate.git_head", received.GitHead == current.GitHead},
		{"candidate.git_index_hash", received.GitIndexHash == current.GitIndexHash},
		{"candidate.worktree_hash", received.WorktreeHash == current.WorktreeHash},
		{"candidate.worktree_trusted", received.WorktreeTrusted == current.WorktreeTrusted},
		{"candidate.dirty_paths", slices.Equal(received.DirtyPaths, current.DirtyPaths)},
		{"candidate.policy_report_hash", received.PolicyReportHash == current.PolicyReportHash},
	}
	mismatches := make([]string, 0, len(checks))
	for _, check := range checks {
		if !check.match {
			mismatches = append(mismatches, check.name)
		}
	}
	return mismatches
}
