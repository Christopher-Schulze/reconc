package agentsession

import (
	"fmt"

	"reconc.dev/reconc/internal/tasklifecycle"
)

// RunStop checks whether any blocking invariant is still unmet at
// session end. If so, emits a JSON control-response with decision=
// block so the agent refuses to stop (prompting the agent to fix
// the remaining violations).
//
// When runloop is enabled and no policy violations block the stop,
// RunStop returns a block decision carrying the runloop continuation
// prompt as the reason. This lets Codex and Claude auto-continue
// without a JS plugin.
func RunStop(repoRoot string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): %s", err)}
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): %s", err)}
	}
	runtimeName := runtimeFromPayload(payload)
	loopState, err := loadRunLoopState(root)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc runloop: %s", err)}
	}

	// Region 1: stop-file / user-interrupt disable. The load+decide+save runs
	// under withRunLoopLock (via mutateRunLoopState) so a concurrent
	// reconcile cannot be clobbered and a fresh disable is respected.
	var earlyResult Result
	earlyHandled := false
	if runLoopStateApplies(loopState, payload.SessionID, runtimeName) &&
		(isUserStopInterrupt(payload) || runLoopStopFileAppliesToState(root, loopState)) {
		if _, _, err := mutateRunLoopState(root, func(dmState runLoopState) runLoopState {
			if !runLoopStateApplies(dmState, payload.SessionID, runtimeName) {
				return dmState
			}
			interrupted := isUserStopInterrupt(payload)
			stopFileApplies := runLoopStopFileAppliesToState(root, dmState)
			if !(stopFileApplies || interrupted) {
				return dmState
			}
			if !stopFileApplies {
				_ = writeRunLoopStopFileForRuntime(root, payload.SessionID, dmState.ActiveRunID, runtimeName, "stop")
			}
			reason := "user_interrupt"
			if stopFileApplies && !interrupted {
				reason = "stop_file"
			}
			after := runLoopState{
				DisabledReason: reason,
			}
			logRunLoopStopDecision(root, "disable_"+reason, payload, runtimeName, dmState, after, stopFileApplies, false, 0)
			earlyResult = Result{ExitCode: 0}
			earlyHandled = true
			return after
		}); err != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc runloop: %s", err)}
		}
	}
	if earlyHandled {
		return earlyResult
	}

	var taskStateInspected bool
	var taskState runLoopTaskState
	loopApplies := runLoopStateApplies(loopState, payload.SessionID, runtimeName)
	if loopApplies {
		inspected, inspectErr := inspectRunLoopTask(root)
		if inspectErr != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: inspect TASK state: %s", inspectErr)}
		}
		taskState = runLoopTaskState{RunState: inspected}
		taskStateInspected = true
	}

	// Repository mode is explicitly autonomous. An executable TASK continues
	// before the terminal Stop policy and never shells out to Git. Task mutation,
	// pre-commit, and the eventual terminal Stop still retain their hard gates.
	if loopApplies && loopState.Mode == runLoopModeRepo && taskState.executable() {
		state, loadErr := loadSessionStateResolved(root, payload.SessionID)
		if loadErr != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): %s", loadErr)}
		}
		if state.EvidenceOverflow {
			return Result{ExitCode: 0, Stdout: runLoopStopBlockJSON(evidenceOverflowMessage(state))}
		}
		if contResult, contHandled, err := runRunLoopContinuation(root, payload, runtimeName, taskState.RunState); err != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc runloop: %s", err)}
		} else if contHandled {
			return contResult
		}
	}

	state, err := EnsureSessionState(root, payload.SessionID)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): %s", err)}
	}
	if state.EvidenceOverflow {
		return Result{ExitCode: 0, Stdout: runLoopStopBlockJSON(evidenceOverflowMessage(state))}
	}

	// Prompt-scoped compatibility mode keeps the stricter legacy behavior: it
	// may bypass policy only without Stop evidence or with an exact clean cache.
	if canUseRunLoopPrePolicyFastPath(loopState, root, state, payload, runtimeName) && taskState.executable() {
		if contResult, contHandled, err := runRunLoopContinuation(root, payload, runtimeName, taskState.RunState); err != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc runloop: %s", err)}
		} else if contHandled {
			return contResult
		}
	}

	if payload.StopHookActive {
		evidenceHash := stopPolicyEvidenceHash(state)
		if _, ok := cachedCleanStopPolicyReportForEvidence(root, state, evidenceHash); ok {
			dmState, _ := loadRunLoopState(root)
			if runLoopStateApplies(dmState, payload.SessionID, runtimeName) {
				logRunLoopStopDecision(root, "stop_hook_active_clean_cache", payload, runtimeName, dmState, dmState, runLoopStopFileAppliesToState(root, dmState), false, 0)
			}
			return Result{ExitCode: 0}
		}
	}

	report, err := runStopPolicyCheck(root, state)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): check failed: %s", err)}
	}
	violations := blockingViolations(report)
	if len(violations) != 0 {
		dmState, _ := loadRunLoopState(root)
		// Avoid endless loops when the agent is already continuing because
		// of this hook.
		if payload.StopHookActive {
			logRunLoopStopDecision(root, "policy_block_stop_hook_active", payload, runtimeName, dmState, dmState, runLoopStopFileAppliesToState(root, dmState), true, len(violations))
			return Result{ExitCode: 0}
		}
		// A user stop/interrupt must always win. Runtimes like Cursor never set
		// StopHookActive, so without this escape an unresolved blocking violation
		// (e.g. an untracked file) traps the session in an unbreakable stop loop.
		// If this exact block already fired on the previous stop, let the stop
		// through: the agent has seen the report once and either cannot or was
		// told not to resolve it. This is the Cursor-equivalent of the
		// StopHookActive escape above.
		if vh := hashBlockingViolations(violations); vh != "" && state.LastStopBlockViolationHash == vh {
			logRunLoopStopDecision(root, "policy_block_released_on_repeat", payload, runtimeName, dmState, dmState, runLoopStopFileAppliesToState(root, dmState), true, len(violations))
			return Result{ExitCode: 0}
		}
		logRunLoopStopDecision(root, "policy_block", payload, runtimeName, dmState, dmState, runLoopStopFileAppliesToState(root, dmState), true, len(violations))
		return Result{ExitCode: 0, Stdout: stopBlockJSONOutput(root, state.SessionID, report, violations)}
	}

	if runLoopStateApplies(loopState, payload.SessionID, runtimeName) && !taskStateInspected {
		inspected, inspectErr := inspectRunLoopTask(root)
		if inspectErr != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: inspect TASK state: %s", inspectErr)}
		}
		taskState = runLoopTaskState{RunState: inspected}
	}
	if contResult, contHandled, err := runRunLoopContinuation(root, payload, runtimeName, taskState.RunState); err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc runloop: %s", err)}
	} else if contHandled {
		return contResult
	}

	dmFinal, err := loadRunLoopState(root)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc runloop: %s", err)}
	}
	if runLoopStateApplies(dmFinal, payload.SessionID, runtimeName) && payload.OpenCodeContinuationDriver {
		logRunLoopStopDecision(root, "runLoop_skip_opencode_driver", payload, runtimeName, dmFinal, dmFinal, false, false, 0)
		return Result{ExitCode: 0}
	}
	return Result{ExitCode: 0}
}

func canUseRunLoopPrePolicyFastPath(loopState runLoopState, root string, state SessionState, payload *HookPayload, runtimeName string) bool {
	if payload.OpenCodeContinuationDriver {
		return false
	}
	if loopState.Mode == runLoopModeRepo || !runLoopStateApplies(loopState, payload.SessionID, runtimeName) {
		return false
	}
	if !sessionHasStopPolicyEvidence(state) {
		return true
	}
	return payload.StopHookActive && cachedStopPolicyReportIsClean(root, state)
}

func sessionHasStopPolicyEvidence(state SessionState) bool {
	return len(state.ReadPaths) != 0 ||
		len(state.WritePaths) != 0 ||
		len(state.Commands) != 0 ||
		len(state.Claims) != 0 ||
		len(state.CommandResults) != 0
}

func cachedStopPolicyReportIsClean(root string, state SessionState) bool {
	_, ok := cachedCleanStopPolicyReportForEvidence(root, state, stopPolicyEvidenceHash(state))
	return ok
}

// runRunLoopContinuation emits the autonomous continuation prompt. Callers
// choose whether this runs before or after the Stop policy gate.
func runRunLoopContinuation(root string, payload *HookPayload, runtimeName string, taskState tasklifecycle.RunState) (Result, bool, error) {
	if payload.OpenCodeContinuationDriver {
		return Result{}, false, nil
	}

	var contResult Result
	contHandled := false
	if _, _, err := mutateRunLoopState(root, func(dmState runLoopState) runLoopState {
		if !runLoopStateApplies(dmState, payload.SessionID, runtimeName) {
			return dmState
		}
		prompt := buildRunLoopContinuationPrompt(taskState)
		if prompt == "" {
			if dmState.Mode == runLoopModeRepo {
				return dmState
			}
			after := runLoopState{DisabledReason: runLoopTerminalReason(taskState)}
			logRunLoopStopDecision(root, "disable_"+after.DisabledReason, payload, runtimeName, dmState, after, false, false, 0)
			contResult = Result{ExitCode: 0}
			contHandled = true
			return after
		}

		progress := runLoopTaskProgressFingerprint(taskState)
		noProgress := dmState.AwaitingContinuation && dmState.LastCurrent == progress
		nudges := dmState.NoProgressNudges
		if noProgress {
			nudges++
		} else {
			nudges = 0
		}
		if nudges >= 6 {
			if dmState.Mode == runLoopModeRepo {
				after := dmState
				after.NoProgressNudges = 0
				after.LastHead = ""
				after.LastCurrent = progress
				after.AwaitingContinuation = false
				logRunLoopStopDecision(root, "repo_no_progress_release", payload, runtimeName, dmState, after, false, false, 0)
				contResult = Result{ExitCode: 0}
				contHandled = true
				return after
			}
			after := runLoopState{
				DisabledReason:   "no_progress_guard",
				NoProgressNudges: nudges,
				LastCurrent:      progress,
			}
			logRunLoopStopDecision(root, "disable_no_progress_guard", payload, runtimeName, dmState, after, false, false, 0)
			contResult = Result{ExitCode: 0}
			contHandled = true
			return after
		}

		after := runLoopState{
			Enabled:              true,
			Mode:                 dmState.Mode,
			SessionID:            dmState.SessionID,
			ActiveRunID:          dmState.ActiveRunID,
			Runtime:              dmState.Runtime,
			NoProgressNudges:     nudges,
			LastCurrent:          progress,
			AwaitingContinuation: true,
		}
		logRunLoopStopDecision(root, "runLoop_followup_message", payload, runtimeName, dmState, after, false, false, 0)
		contResult = Result{ExitCode: 0, Stdout: runLoopStopBlockJSON(prompt)}
		contHandled = true
		return after
	}); err != nil {
		return Result{}, false, err
	}
	return contResult, contHandled, nil
}
