package agentsession

func worktreePathHash(repoRoot string, path, indexEntry string) string {
	return worktreePathHashWithScan(repoRoot, path, indexEntry, nil)
}
