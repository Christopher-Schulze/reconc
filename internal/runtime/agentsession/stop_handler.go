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
// When repository run is enabled and no policy violations block the stop,
// RunStop returns a block decision carrying the TASK continuation
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
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: %s", err)}
	}

	// An interrupt releases only this host invocation. Durable repository run
	// state remains enabled until `reconc run off` or terminal TASK exhaustion.
	if isUserStopInterrupt(payload) {
		if runLoopStateApplies(loopState) {
			logRunLoopStopDecision(root, "interrupt_release", payload, runtimeName, loopState, loopState, false, 0)
		}
		return Result{ExitCode: 0}
	}

	var taskStateInspected bool
	var taskState runLoopTaskState
	checkpointDue := false
	loopApplies := runLoopStateApplies(loopState)
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
	if loopApplies && taskState.executable() {
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
				return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: %s", err)}
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

	if payload.StopHookActive && !checkpointDue {
		evidenceHash := stopPolicyEvidenceHash(state)
		if _, ok := cachedCleanStopPolicyReportForEvidence(root, state, evidenceHash); ok {
			dmState, _ := loadRunLoopState(root)
			if runLoopStateApplies(dmState) {
				logRunLoopStopDecision(root, "stop_hook_active_clean_cache", payload, runtimeName, dmState, dmState, false, 0)
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
			logRunLoopStopDecision(root, "policy_block_stop_hook_active", payload, runtimeName, dmState, dmState, true, len(violations))
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
			logRunLoopStopDecision(root, "policy_block_released_on_repeat", payload, runtimeName, dmState, dmState, true, len(violations))
			return Result{ExitCode: 0}
		}
		logRunLoopStopDecision(root, "policy_block", payload, runtimeName, dmState, dmState, true, len(violations))
		return Result{ExitCode: 0, Stdout: stopBlockJSONOutput(root, state.SessionID, report, violations)}
	}

	if result, blocked, terminalErr := taskCompletionCommitGate(root, policyResult.GitSnapshot); terminalErr != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): TASK completion gate: %s", terminalErr)}
	} else if blocked {
		return result
	}
	if checkpointDue {
		if err := markRepoPolicyCheckpoint(root, payload, runtimeName, state.MaterialEvents); err != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: checkpoint: %s", err)}
		}
	}

	if runLoopStateApplies(loopState) && !taskStateInspected {
		inspected, inspectErr := inspectRunLoopTask(root)
		if inspectErr != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: inspect TASK state: %s", inspectErr)}
		}
		taskState = runLoopTaskState{RunState: inspected}
	}
	if contResult, contHandled, err := runRunLoopContinuation(root, payload, runtimeName, taskState.RunState, state.MaterialEvents); err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: %s", err)}
	} else if contHandled {
		return contResult
	}

	return Result{ExitCode: 0}
}

func repoRunPolicyCheckpointDue(loop runLoopState, state SessionState, now time.Time) bool {
	if !runLoopStateApplies(loop) || state.MaterialEvents <= loop.CheckpointMaterial {
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
		if !runLoopStateApplies(state) {
			return state
		}
		state.CheckpointMaterial = materialEvents
		state.LastPolicyCheckpoint = time.Now().UTC().Format(time.RFC3339Nano)
		return state
	})
	if err == nil && before != after {
		logRunLoopStopDecision(root, "repo_policy_checkpoint", payload, runtimeName, before, after, false, 0)
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

// runRunLoopContinuation emits the autonomous continuation prompt. Callers
// choose whether this runs before or after the Stop policy gate.
func runRunLoopContinuation(root string, payload *HookPayload, runtimeName string, taskState tasklifecycle.RunState, materialEvents uint64) (Result, bool, error) {
	var contResult Result
	contHandled := false
	decisionBranch := ""
	before, after, err := mutateRunLoopState(root, func(dmState runLoopState) runLoopState {
		if !runLoopStateApplies(dmState) {
			return dmState
		}
		prompt := buildRunLoopContinuationPrompt(taskState)
		if prompt == "" {
			if taskState.Disposition != tasklifecycle.RunComplete && taskState.Disposition != tasklifecycle.RunAbsent {
				return dmState
			}
			after := runLoopState{DisabledReason: runLoopTerminalReason(taskState)}
			decisionBranch = "disable_" + after.DisabledReason
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
			after := dmState
			after.NoProgressNudges = 0
			after.LastCurrent = progress
			after.AwaitingContinuation = false
			decisionBranch = "repo_no_progress_release"
			contResult = Result{ExitCode: 0}
			contHandled = true
			return after
		}

		after := runLoopState{
			Enabled:              true,
			Mode:                 runLoopModeRepo,
			NoProgressNudges:     nudges,
			LastCurrent:          progress,
			AwaitingContinuation: true,
			EnabledAt:            dmState.EnabledAt,
			LastPolicyCheckpoint: dmState.LastPolicyCheckpoint,
			CheckpointMaterial:   dmState.CheckpointMaterial,
		}
		decisionBranch = "run_followup"
		contResult = Result{ExitCode: 0, Stdout: runLoopStopBlockJSON(prompt)}
		contHandled = true
		return after
	})
	if err != nil {
		return Result{}, false, err
	}
	if decisionBranch != "" && before != after {
		logRunLoopStopDecision(root, decisionBranch, payload, runtimeName, before, after, false, 0)
	}
	return contResult, contHandled, nil
}
