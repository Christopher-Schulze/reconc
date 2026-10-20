package agentsession

func stopPolicyFingerprintCacheable(input stopPolicyFingerprintInput) bool {
	return stopPolicyFingerprintCacheableWithScan(input, nil)
}

func stopPolicyInputIdentity(repoRoot, path string) policyInputIdentity {
	return stopPolicyInputIdentityWithScan(repoRoot, path, nil)
}

func stopPolicyGitStatus(repoRoot string) (status string, mode string) {
	snapshot := stopPolicyGitSnapshotFor(repoRoot)
	return snapshot.Status, snapshot.StatusMode
}
