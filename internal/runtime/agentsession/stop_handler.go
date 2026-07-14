package agentsession

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"reconc.dev/reconc/internal/tasklifecycle"
)

const (
	runLoopPolicyCheckpointEvents   = 64
	runLoopPolicyCheckpointInterval = 30 * time.Minute
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
	checkpointDue := false
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
		checkpointDue = repoRunPolicyCheckpointDue(loopState, state, time.Now())
		if !checkpointDue {
			if contResult, contHandled, err := runRunLoopContinuation(root, payload, runtimeName, taskState.RunState, state.MaterialEvents); err != nil {
				return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc runloop: %s", err)}
			} else if contHandled {
				return contResult
			}
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
		if contResult, contHandled, err := runRunLoopContinuation(root, payload, runtimeName, taskState.RunState, state.MaterialEvents); err != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc runloop: %s", err)}
		} else if contHandled {
			return contResult
		}
	}

	if payload.StopHookActive && !checkpointDue {
		evidenceHash := stopPolicyEvidenceHash(state)
		if _, ok := cachedCleanStopPolicyReportForEvidence(root, state, evidenceHash); ok {
			dmState, _ := loadRunLoopState(root)
			if runLoopStateApplies(dmState, payload.SessionID, runtimeName) {
				logRunLoopStopDecision(root, "stop_hook_active_clean_cache", payload, runtimeName, dmState, dmState, runLoopStopFileAppliesToState(root, dmState), false, 0)
			}
			return Result{ExitCode: 0}
		}
	}

	policyResult, err := runStopPolicyCheckWithSnapshot(root, state)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): check failed: %s", err)}
	}
	report := policyResult.Report
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

	if !isUserStopInterrupt(payload) {
		if result, blocked, terminalErr := taskCompletionCommitGate(root, policyResult.GitSnapshot); terminalErr != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): TASK completion gate: %s", terminalErr)}
		} else if blocked {
			return result
		}
	}
	if checkpointDue {
		if err := markRepoPolicyCheckpoint(root, payload, runtimeName, state.MaterialEvents); err != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc runloop: checkpoint: %s", err)}
		}
	}

	if runLoopStateApplies(loopState, payload.SessionID, runtimeName) && !taskStateInspected {
		inspected, inspectErr := inspectRunLoopTask(root)
		if inspectErr != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: inspect TASK state: %s", inspectErr)}
		}
		taskState = runLoopTaskState{RunState: inspected}
	}
	if contResult, contHandled, err := runRunLoopContinuation(root, payload, runtimeName, taskState.RunState, state.MaterialEvents); err != nil {
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

func repoRunPolicyCheckpointDue(loop runLoopState, state SessionState, now time.Time) bool {
	if !loop.Enabled || loop.Mode != runLoopModeRepo || state.MaterialEvents <= loop.CheckpointMaterial {
		return false
	}
	if state.MaterialEvents-loop.CheckpointMaterial >= runLoopPolicyCheckpointEvents {
		return true
	}
	if len(state.CommandResults) > 0 && state.CommandResults[len(state.CommandResults)-1].Outcome == "failure" {
		return true
	}
	anchor := loop.LastPolicyCheckpoint
	if anchor == "" {
		anchor = loop.EnabledAt
	}
	started, err := time.Parse(time.RFC3339Nano, anchor)
	return err == nil && now.Sub(started) >= runLoopPolicyCheckpointInterval
}

func markRepoPolicyCheckpoint(root string, payload *HookPayload, runtimeName string, materialEvents uint64) error {
	before, after, err := mutateRunLoopState(root, func(state runLoopState) runLoopState {
		if !runLoopStateApplies(state, payload.SessionID, runtimeName) || state.Mode != runLoopModeRepo {
			return state
		}
		state.CheckpointMaterial = materialEvents
		state.LastPolicyCheckpoint = time.Now().UTC().Format(time.RFC3339Nano)
		return state
	})
	if err == nil && before != after {
		logRunLoopStopDecision(root, "repo_policy_checkpoint", payload, runtimeName, before, after, false, false, 0)
	}
	return err
}

func taskCompletionCommitGate(root string, snapshot stopPolicyGitSnapshot) (Result, bool, error) {
	cfg, err := tasklifecycle.LoadConfig(root)
	if err != nil || !cfg.Enabled || !cfg.Completion.RequireCommitted {
		return Result{}, false, err
	}
	state, err := tasklifecycle.InspectRunState(root)
	if err != nil {
		return Result{}, false, err
	}
	if state.Disposition != tasklifecycle.RunComplete {
		return Result{}, false, nil
	}
	if !snapshot.StatusOK {
		return Result{ExitCode: 0, Stdout: runLoopStopBlockJSON("reconc blocked terminal TASK completion because Git status could not be verified. Restore Git status visibility or interrupt explicitly.")}, true, nil
	}
	dirty := taskControlPlaneDirtyPaths(cfg, snapshot.Status)
	if len(dirty) == 0 {
		return Result{}, false, nil
	}
	return Result{ExitCode: 0, Stdout: runLoopStopBlockJSON("reconc blocked terminal TASK completion because the TASK control plane is not committed: " + strings.Join(dirty, ", ") + ". Commit the completed TASK or interrupt explicitly.")}, true, nil
}

func taskControlPlaneDirtyPaths(cfg tasklifecycle.Config, status string) []string {
	paths := make([]string, 0)
	for _, path := range dirtyPathsFromStatus(status) {
		dirtyDir := strings.TrimSuffix(path, "/")
		detailDir := strings.TrimSuffix(cfg.DetailDir, "/")
		if path == cfg.OverviewPath || dirtyDir == detailDir ||
			strings.HasPrefix(path, detailDir+"/") ||
			(strings.HasSuffix(path, "/") && (strings.HasPrefix(cfg.OverviewPath, dirtyDir+"/") || strings.HasPrefix(detailDir, dirtyDir+"/"))) {
			paths = append(paths, path)
		}
	}
	return paths
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
func runRunLoopContinuation(root string, payload *HookPayload, runtimeName string, taskState tasklifecycle.RunState, materialEvents uint64) (Result, bool, error) {
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

		progress := runLoopTaskProgressFingerprint(taskState) + "|material=" + strconv.FormatUint(materialEvents, 10)
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
			EnabledAt:            dmState.EnabledAt,
			LastPolicyCheckpoint: dmState.LastPolicyCheckpoint,
			CheckpointMaterial:   dmState.CheckpointMaterial,
			LastPromptSignature:  dmState.LastPromptSignature,
			StopAnchorMessageID:  dmState.StopAnchorMessageID,
			LastHead:             dmState.LastHead,
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
