package agentsession

import (
	"fmt"
	"os"
	"time"
)

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

// ResetRepositoryRun is a recovery-only operation. It replaces only the
// integrity-bound state.bin with a clean disabled state and preserves the
// bounded decision log and every other repository artifact.
func ResetRepositoryRun(repoRoot string) (RepositoryRunStatus, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return RepositoryRunStatus{}, err
	}
	err = withRepositoryRunFileResolved(root, func(file *os.File) error {
		if err := file.Truncate(0); err != nil {
			return fmt.Errorf("truncate repository run state: %w", err)
		}
		if err := file.Chmod(0o600); err != nil {
			return fmt.Errorf("protect repository run state: %w", err)
		}
		state := repositoryRunState{
			DisabledReason: repositoryRunDisabledCommandOff,
			RootIdentity:   repositoryRunRootIdentity(root),
		}
		if err := writeRepositoryRunSnapshotFile(file, state, repositoryRunSnapshot{Slot: -1}); err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			return fmt.Errorf("sync repository run state: %w", err)
		}
		return nil
	})
	if err != nil {
		return RepositoryRunStatus{}, err
	}
	_ = appendRunDecisionResolved(root, RunDecision{
		Event: "recovery", Branch: "run_state_reset",
		DisabledReasonAfter: repositoryRunDisabledCommandOff.String(),
	})
	return readRepositoryRunStatusResolved(root)
}
