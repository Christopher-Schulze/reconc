package agentsession

import "time"

// SetRepositoryRun is the only run-state switch used by `reconc run on|off`.
// Hook messages and session lifecycle events never mutate this state.
func SetRepositoryRun(repoRoot string, enabled bool) (RepositoryRunStatus, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return RepositoryRunStatus{}, err
	}
	branch := "run_command_off"
	if enabled {
		branch = "run_command_on"
	}
	before, after, err := mutateRepositoryRunStateResolved(root, func(state repositoryRunState) repositoryRunState {
		if enabled {
			if repositoryRunEnabled(state) {
				return state
			}
			return repositoryRunState{
				Enabled:   true,
				EnabledAt: time.Now().UnixNano(),
			}
		}
		return repositoryRunState{DisabledReason: repositoryRunDisabledCommandOff}
	})
	if err != nil {
		return RepositoryRunStatus{}, err
	}
	if before != after {
		_ = appendRunDecisionResolved(root, RunDecision{
			Event: "command", Branch: branch,
			EnabledBefore: before.Enabled, EnabledAfter: after.Enabled,
			DisabledReasonBefore: before.DisabledReason.String(),
			DisabledReasonAfter:  after.DisabledReason.String(),
		})
	}
	return readRepositoryRunStatusResolved(root)
}
