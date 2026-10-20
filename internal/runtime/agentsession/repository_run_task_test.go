package agentsession

import (
	"reconc.dev/reconc/internal/tasklifecycle"
)

func inspectRepositoryRunTask(repoRoot string) (tasklifecycle.RunState, error) {
	return tasklifecycle.InspectRunStateResolved(repoRoot)
}
