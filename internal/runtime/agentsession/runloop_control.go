package agentsession

// SetRunLoopRepoMode is the AI-facing repository-wide on/off switch used by
// `reconc run on|off`. Prompt-scoped `/runloop` remains session mode.
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
			return runLoopState{Enabled: true, Mode: runLoopModeRepo}
		}
		return runLoopState{DisabledReason: "command_off"}
	})
	if err != nil {
		return RunLoopStatusInfo{}, err
	}
	if enabled {
		if err := clearRunLoopStopFile(root); err != nil {
			return RunLoopStatusInfo{}, err
		}
	}
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
