package agentsession

func stopGenerationWorthwhile(repoRoot string, files []gitDirtyFile) bool {
	return stopGenerationWorthwhileWithMetrics(repoRoot, files, nil)
}
