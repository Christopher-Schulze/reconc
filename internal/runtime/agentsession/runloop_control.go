package agentsession

import "time"

// SetRunLoopRepoMode is the only run-state switch used by `reconc run on|off`.
// Hook messages and session lifecycle events never mutate this state.
func SetRunLoopRepoMode(repoRoot string, enabled bool) (RunLoopStatusInfo, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return RunLoopStatusInfo{}, err
	}
	branch := "run_command_off"
	if enabled {
		branch = "run_command_on"
	}
	before, after, err := mutateRunLoopState(root, func(state runLoopState) runLoopState {
		if enabled {
			if runLoopStateApplies(state) {
				return state
			}
			return runLoopState{
				Enabled:   true,
				Mode:      runLoopModeRepo,
				EnabledAt: time.Now().UTC().Format(time.RFC3339Nano),
			}
		}
		return runLoopState{DisabledReason: "command_off"}
	})
	if err != nil {
		return RunLoopStatusInfo{}, err
	}
	// The marker belonged to the removed prompt/session mode and has no control
	// semantics. Cleanup is best-effort and cannot invalidate a successful
	// canonical state transition.
	_ = clearRunLoopStopFile(root)
	if before != after {
		_ = appendRunLoopDecision(root, RunLoopDecision{
			Event: "command", Branch: branch,
			EnabledBefore: before.Enabled, EnabledAfter: after.Enabled,
			DisabledReasonBefore: before.DisabledReason,
			DisabledReasonAfter:  after.DisabledReason,
		})
	}
	return ReadRunLoopStatus(root)
}
