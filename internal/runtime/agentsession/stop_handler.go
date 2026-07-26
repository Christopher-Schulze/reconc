package agentsession

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"reconc.dev/reconc/internal/tasklifecycle"
)

const (
	repositoryRunCheckpointEvents   = 64
	repositoryRunCheckpointInterval = 30 * time.Minute
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
func RunStop(repoRoot string, payloadBytes []byte) (result Result) {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): %s", err)}
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): %s", err)}
	}
	runtimeName := runtimeFromPayload(payload)
	runFile, runSnapshot, err := openRepositoryRunStateResolved(root)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: %s", err)}
	}
	if runFile != nil {
		defer func() {
			closeErr := runFile.Close()
			if closeErr == nil || result.ExitCode != 0 {
				return
			}
			// Never discard a computed decision payload (block report or
			// continuation prompt); surface the close error alongside it.
			warn := fmt.Sprintf("reconc run: close state: %s", closeErr)
			if result.Stdout != "" {
				result.Stderr = joinStderr(result.Stderr, warn)
				return
			}
			result = Result{ExitCode: 2, Stderr: warn}
		}()
	}
	runState := runSnapshot.State

	// An interrupt releases only this host invocation. Durable repository run
	// state remains enabled until `reconc run off` or terminal TASK exhaustion.
	if isUserStopInterrupt(payload) {
		if repositoryRunEnabled(runState) {
			logRunStopDecision(root, "interrupt_release", payload, runtimeName, runState, runState, false, 0)
		}
		return Result{ExitCode: 0}
	}

	var taskStateInspected bool
	var taskState repositoryRunTaskState
	checkpointDue := false
	runApplies := repositoryRunEnabled(runState)
	if runApplies {
		inspected, inspectErr := inspectRepositoryRunTask(root)
		if inspectErr != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: inspect TASK state: %s", inspectErr)}
		}
		taskState = repositoryRunTaskState{RunState: inspected}
		taskStateInspected = true
	}

	// Repository mode is explicitly autonomous. An executable TASK continues
	// before the terminal Stop policy and never shells out to Git. Task mutation,
	// pre-commit, and the eventual terminal Stop still retain their hard gates.
	if runApplies && taskState.executable() {
		state, loadErr := loadSessionStateResolved(root, payload.SessionID)
		if loadErr != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): %s", loadErr)}
		}
		if state.EvidenceOverflow {
			return Result{ExitCode: 0, Stdout: repositoryRunBlockJSON(evidenceOverflowMessage(state))}
		}
		state, loadErr = loadCompleteSessionEvidence(root, state)
		if loadErr != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): load evidence chain: %s", loadErr)}
		}
		checkpointDue = repoRunPolicyCheckpointDue(runState, state, time.Now())
		if !checkpointDue {
			if contResult, contHandled, err := runRepositoryContinuation(root, runFile, payload, runtimeName, taskState.RunState); err != nil {
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
		if runApplies {
			return Result{ExitCode: 0, Stdout: repositoryRunBlockJSON(evidenceOverflowMessage(state))}
		}
		if _, markErr := MutateSessionState(root, payload.SessionID, func(current SessionState) SessionState {
			current.UncertifiedTermination = true
			return current
		}); markErr != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): persist uncertified termination: %s", markErr)}
		}
		return Result{ExitCode: 0, Stderr: evidenceOverflowMessage(state) + " Stop released as uncertified because repository run is disabled."}
	}
	state, err = loadCompleteSessionEvidence(root, state)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): load evidence chain: %s", err)}
	}

	if payload.StopHookActive && !payload.StrictContinuation && !checkpointDue {
		evidenceHash := stopPolicyEvidenceHash(state)
		if _, ok := cachedCleanStopPolicyReportForEvidence(root, state, evidenceHash); ok {
			currentRun, _ := loadRepositoryRunStateResolved(root)
			if repositoryRunEnabled(currentRun) {
				logRunStopDecision(root, "stop_hook_active_clean_cache", payload, runtimeName, currentRun, currentRun, false, 0)
			}
			return Result{ExitCode: 0}
		}
	}

	policyResult, err := runStopPolicyCheckWithSnapshot(root, state)
	if err != nil {
		if isLockfileError(err) {
			// A stale lockfile must still hold the session open, but it must
			// not repeat an unreachable remediation forever. The pre-tool
			// route admits the repair, so this block is now actionable.
			return Result{ExitCode: 2, Stderr: lockfileBlockMessage("stop", err)}
		}
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): check failed: %s", err)}
	}
	report := policyResult.Report
	violations := blockingViolations(report)
	if len(violations) != 0 {
		currentRun, _ := loadRepositoryRunStateResolved(root)
		// Avoid endless loops when the agent is already continuing because
		// of this hook.
		if payload.StopHookActive && !payload.StrictContinuation {
			logRunStopDecision(root, "policy_block_stop_hook_active", payload, runtimeName, currentRun, currentRun, true, len(violations))
			return Result{ExitCode: 0}
		}
		// A user stop/interrupt must always win. Runtimes like Cursor never set
		// StopHookActive, so without this escape an unresolved blocking violation
		// (e.g. an untracked file) traps the session in an unbreakable stop loop.
		// If this exact block already fired on the previous stop, let the stop
		// through: the agent has seen the report once and either cannot or was
		// told not to resolve it. This is the Cursor-equivalent of the
		// StopHookActive escape above.
		if vh := hashBlockingViolations(violations); !payload.StrictContinuation && vh != "" && state.LastStopBlockViolationHash == vh {
			logRunStopDecision(root, "policy_block_released_on_repeat", payload, runtimeName, currentRun, currentRun, true, len(violations))
			return Result{ExitCode: 0}
		}
		logRunStopDecision(root, "policy_block", payload, runtimeName, currentRun, currentRun, true, len(violations))
		return Result{ExitCode: 0, Stdout: stopBlockJSONOutput(root, state.SessionID, report, violations)}
	}

	if result, blocked, terminalErr := taskCompletionCommitGate(root, policyResult.GitSnapshot); terminalErr != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): TASK completion gate: %s", terminalErr)}
	} else if blocked {
		return result
	}
	if checkpointDue {
		if err := markRepoPolicyCheckpoint(root, runFile, payload, runtimeName, state.MaterialEvents); err != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: checkpoint: %s", err)}
		}
	}

	if repositoryRunEnabled(runState) {
		if !taskStateInspected {
			inspected, inspectErr := inspectRepositoryRunTask(root)
			if inspectErr != nil {
				return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: inspect TASK state: %s", inspectErr)}
			}
			taskState = repositoryRunTaskState{RunState: inspected}
		}
		if contResult, contHandled, err := runRepositoryContinuation(root, runFile, payload, runtimeName, taskState.RunState); err != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: %s", err)}
		} else if contHandled {
			return contResult
		}
	}

	return Result{ExitCode: 0}
}

func repoRunPolicyCheckpointDue(run repositoryRunState, state SessionState, now time.Time) bool {
	if !repositoryRunEnabled(run) || state.MaterialEvents <= run.CheckpointMaterial {
		return false
	}
	if state.MaterialEvents-run.CheckpointMaterial >= repositoryRunCheckpointEvents {
		return true
	}
	if len(state.CommandResults) > 0 && state.CommandResults[len(state.CommandResults)-1].Outcome == "failure" {
		return true
	}
	anchor := run.LastPolicyCheckpoint
	if anchor == 0 {
		anchor = run.EnabledAt
	}
	return anchor > 0 && now.Sub(time.Unix(0, anchor)) >= repositoryRunCheckpointInterval
}

func markRepoPolicyCheckpoint(root string, runFile *os.File, payload *HookPayload, runtimeName string, materialEvents uint64) error {
	before, after, err := mutateRepositoryRunStateOpenFile(root, runFile, func(state repositoryRunState) repositoryRunState {
		if !repositoryRunEnabled(state) {
			return state
		}
		state.CheckpointMaterial = materialEvents
		state.LastPolicyCheckpoint = time.Now().UnixNano()
		return state
	})
	if err == nil && before != after {
		logRunStopDecision(root, "repo_policy_checkpoint", payload, runtimeName, before, after, false, 0)
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
		return Result{ExitCode: 0, Stdout: repositoryRunBlockJSON("reconc blocked terminal TASK completion because Git status could not be verified. Restore Git status visibility or interrupt explicitly.")}, true, nil
	}
	dirty := tasklifecycle.DirtyCompletionPaths(cfg, dirtyPathsFromStatus(snapshot.Status))
	if len(dirty) == 0 {
		return Result{}, false, nil
	}
	return Result{ExitCode: 0, Stdout: repositoryRunBlockJSON("reconc blocked terminal TASK completion because the TASK control plane is not committed: " + strings.Join(dirty, ", ") + ". Commit the completed TASK or interrupt explicitly.")}, true, nil
}

// runRepositoryContinuation emits the autonomous continuation prompt. Callers
// choose whether this runs before or after the Stop policy gate.
func runRepositoryContinuation(root string, runFile *os.File, payload *HookPayload, runtimeName string, taskState tasklifecycle.RunState) (Result, bool, error) {
	var contResult Result
	contHandled := false
	decisionBranch := ""
	prompt := buildRepositoryRunPrompt(taskState)
	var progressHash [32]byte
	sessionNudges := 0
	sessionReleased := false
	strictContinuation := payload != nil && payload.StrictContinuation
	runEpoch := int64(0)
	if prompt != "" {
		current, err := loadRepositoryRunStateResolved(root)
		if err != nil {
			return Result{}, false, err
		}
		if !repositoryRunEnabled(current) {
			return Result{}, false, nil
		}
		runEpoch = current.EnabledAt
		_, err = MutateSessionState(root, sessionIDFromPayload(payload), func(state SessionState) SessionState {
			progressHash = repositoryRunProgressHash(taskState, state.MaterialEvents)
			encodedHash := hex.EncodeToString(progressHash[:])
			if state.RepositoryRunEnabledAt != runEpoch {
				state.RepositoryRunNudges = 0
				state.RepositoryRunAwaiting = false
			}
			noProgress := state.RepositoryRunAwaiting && state.RepositoryRunProgressHash == encodedHash
			if strictContinuation {
				state.RepositoryRunNudges = 0
			} else if noProgress {
				state.RepositoryRunNudges++
			} else {
				state.RepositoryRunNudges = 0
			}
			sessionNudges = state.RepositoryRunNudges
			sessionReleased = !strictContinuation && sessionNudges >= 6
			state.RepositoryRunEnabledAt = runEpoch
			state.RepositoryRunProgressHash = encodedHash
			state.RepositoryRunAwaiting = !sessionReleased
			if sessionReleased {
				state.RepositoryRunNudges = 0
			}
			return state
		})
		if err != nil {
			return Result{}, false, fmt.Errorf("update per-session run guard: %w", err)
		}
	}
	mutate := mutateRepositoryRunStateResolved
	if runFile != nil {
		mutate = func(_ string, fn func(repositoryRunState) repositoryRunState) (repositoryRunState, repositoryRunState, error) {
			return mutateRepositoryRunStateOpenFile(root, runFile, fn)
		}
	}
	before, after, err := mutate(root, func(current repositoryRunState) repositoryRunState {
		if !repositoryRunEnabled(current) {
			return current
		}
		if prompt == "" {
			if taskState.Disposition != tasklifecycle.RunComplete && taskState.Disposition != tasklifecycle.RunAbsent {
				return current
			}
			after := repositoryRunState{DisabledReason: repositoryRunTerminalReason(taskState)}
			decisionBranch = "disable_" + after.DisabledReason.String()
			contResult = Result{ExitCode: 0}
			contHandled = true
			return after
		}
		if current.EnabledAt != runEpoch {
			return current
		}
		if sessionReleased {
			after := current
			after.NoProgressNudges = 0
			after.LastProgressHash = progressHash
			after.AwaitingContinuation = false
			decisionBranch = "repo_no_progress_release"
			contResult = Result{ExitCode: 0}
			contHandled = true
			return after
		}

		after := repositoryRunState{
			Enabled:              true,
			NoProgressNudges:     sessionNudges,
			LastProgressHash:     progressHash,
			AwaitingContinuation: true,
			EnabledAt:            current.EnabledAt,
			LastPolicyCheckpoint: current.LastPolicyCheckpoint,
			CheckpointMaterial:   current.CheckpointMaterial,
		}
		decisionBranch = "run_followup"
		contResult = Result{ExitCode: 0, Stdout: repositoryRunBlockJSON(prompt)}
		contHandled = true
		return after
	})
	if err != nil {
		return Result{}, false, err
	}
	if decisionBranch == "run_followup" || decisionBranch == "repo_no_progress_release" {
		logRunContinuationDecision(root, decisionBranch, payload, runtimeName, before, after, sessionNudges, strictContinuation)
	} else if decisionBranch != "" && before != after {
		logRunStopDecision(root, decisionBranch, payload, runtimeName, before, after, false, 0)
	}
	return contResult, contHandled, nil
}

// joinStderr combines two stderr fragments with a newline, tolerating
// either side being empty.
func joinStderr(existing, extra string) string {
	if existing == "" {
		return extra
	}
	if extra == "" {
		return existing
	}
	return existing + "\n" + extra
}
