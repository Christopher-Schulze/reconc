package agentsession

import (
	"reconc.dev/reconc/internal/runtime"
)

func runStopPolicyCheck(repoRoot string, state SessionState) (*runtime.CheckReport, error) {
	result, err := runStopPolicyCheckWithSnapshot(repoRoot, state)
	return result.Report, err
}

func runStopPolicyCheckWithSnapshot(repoRoot string, state SessionState) (stopPolicyCheckResult, error) {
	return runStopPolicyCheckWithSnapshotWithEvaluator(repoRoot, state, runtime.NewEvaluator())
}

func runStopPolicyCheckWithSnapshotWithEvaluator(repoRoot string, state SessionState, evaluator *runtime.Evaluator) (stopPolicyCheckResult, error) {
	return runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repoRoot, state, evaluator, nil, nil)
}

func cachedCleanStopPolicyReportForEvidence(repoRoot string, state SessionState, evidenceHash string) (*runtime.CheckReport, bool) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return nil, false
	}
	taskSnapshot, err := captureStopTaskSnapshot(root)
	if err != nil {
		return nil, false
	}
	result, ok := cachedCleanStopPolicyResultForEvidenceAndSnapshot(
		root, state, evidenceHash, taskSnapshot, stopPolicyGitSnapshotFor(root),
	)
	return result.Report, ok
}
