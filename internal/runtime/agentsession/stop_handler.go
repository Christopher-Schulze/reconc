package agentsession

import (
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/tasklifecycle"
)

const (
	repositoryRunCheckpointEvents   = 64
	repositoryRunCheckpointInterval = 30 * time.Minute
	stopCloseDiagnosticMaxBytes     = 4096
)

type stopTerminalDecision uint8

const (
	stopTerminalDecisionOrdinary stopTerminalDecision = iota
	stopTerminalDecisionUserInterrupt
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
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): %s", err)}
	}
	return runStopResolved(root.path, payloadBytes, "")
}

func runStopResolved(root string, payloadBytes []byte, runtimeName string) (result Result) {
	return runStopResolvedWithEvaluator(root, payloadBytes, runtimeName, runtime.NewEvaluator())
}

func runStopResolvedWithEvaluator(root string, payloadBytes []byte, runtimeName string, evaluator *runtime.Evaluator) (result Result) {
	return runStopResolvedWithEvaluatorAndCache(root, payloadBytes, runtimeName, evaluator, nil)
}

func runStopResolvedWithEvaluatorAndCache(
	root string,
	payloadBytes []byte,
	runtimeName string,
	evaluator *runtime.Evaluator,
	stopCache *StopDecisionCache,
) (result Result) {
	return runStopResolvedWithEvaluatorCacheAndClose(root, payloadBytes, runtimeName, evaluator, stopCache, (*os.File).Close)
}

func runStopResolvedWithEvaluatorCacheAndClose(
	root string,
	payloadBytes []byte,
	runtimeName string,
	evaluator *runtime.Evaluator,
	stopCache *StopDecisionCache,
	closeRunFile func(*os.File) error,
) (result Result) {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): %s", err)}
	}
	if runtimeName == "" {
		runtimeName = runtimeFromPayload(payload)
	}
	runFile, runSnapshot, err := openRepositoryRunStateResolved(root)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: %s", err)}
	}
	terminalDecision := stopTerminalDecisionOrdinary
	if runFile != nil {
		defer func() {
			result = applyStopRunStateClose(result, terminalDecision, closeRunFile(runFile))
		}()
	}
	runState := runSnapshot.State
	loadedEvidence := &stopLoadedEvidence{}

	// An interrupt releases only this host invocation. Durable repository run
	// state remains enabled until `reconc run off` or terminal TASK exhaustion.
	if isUserStopInterrupt(payload) {
		terminalDecision = stopTerminalDecisionUserInterrupt
		if repositoryRunEnabled(runState) {
			err = logRunStopDecision(root, "interrupt_release", payload, runtimeName, runState, runState, false, 0)
		}
		return Result{ExitCode: 0, Stderr: bestEffortStopDecisionDiagnostic(err)}
	}

	taskSnapshot, err := captureStopTaskSnapshot(root)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): capture TASK snapshot: %s", err)}
	}
	taskState := repositoryRunTaskState{RunState: taskSnapshot.State}
	checkpointDue := false
	runApplies := repositoryRunEnabled(runState)

	// Repository mode is explicitly autonomous. An executable TASK continues
	// before the terminal Stop policy and never shells out to Git. Task mutation,
	// pre-commit, and the eventual terminal Stop still retain their hard gates.
	if runApplies && taskState.executable() {
		state, loadErr := loadSessionStateWithLockResolved(root, payload.SessionID)
		if loadErr != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): %s", loadErr)}
		}
		if state.EvidenceOverflow {
			return repositoryRunBlockResult(evidenceOverflowMessage(state))
		}
		rawState := state
		var evidencePrefix verifiedEvidencePrefix
		state, loadErr = loadCompleteSessionEvidenceWithCacheCapture(root, state, stopCache, &evidencePrefix)
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
		revision, revisionErr := stopPolicyEvidenceRevision(rawState)
		if revisionErr != nil {
			return resultWithEncodingError(Result{ExitCode: 2}, revisionErr)
		}
		loadedEvidence.capture(root, rawState, revision, state, evidencePrefix)
	}

	state, err := ensureSessionStateResolved(root, payload.SessionID)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): %s", err)}
	}
	if state.EvidenceOverflow {
		if runApplies {
			return repositoryRunBlockResult(evidenceOverflowMessage(state))
		}
		if _, markErr := mutateSessionStateResolved(root, payload.SessionID, func(current SessionState) SessionState {
			current.UncertifiedTermination = true
			return current
		}); markErr != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): persist uncertified termination: %s", markErr)}
		}
		return Result{ExitCode: 0, Stderr: evidenceOverflowMessage(state) + " Stop released as uncertified because repository run is disabled."}
	}
	state, _, err = loadedEvidence.load(root, state, stopCache)
	if err != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): load evidence chain: %s", err)}
	}

	if payload.StopHookActive && !payload.StrictContinuation && !checkpointDue {
		evidenceHash, hashErr := stopPolicyEvidenceHash(state)
		if hashErr != nil {
			return resultWithEncodingError(Result{ExitCode: 2}, hashErr)
		}
		if cached, ok := cachedCleanStopPolicyReportForEvidenceWithCache(root, state, evidenceHash, stopCache, taskSnapshot); ok {
			if terminalResult, handled, terminalErr := finalizeCleanStop(root, state.SessionID, cached); terminalErr != nil {
				return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): finalize clean Stop: %s", terminalErr)}
			} else if handled {
				return terminalResult
			}
			currentRun, _ := loadRepositoryRunStateResolved(root)
			var logErr error
			if repositoryRunEnabled(currentRun) {
				logErr = logRunStopDecision(root, "stop_hook_active_clean_cache", payload, runtimeName, currentRun, currentRun, false, 0)
			}
			return Result{ExitCode: 0, Stderr: bestEffortStopDecisionDiagnostic(logErr)}
		}
	}

	policyResult, err := runStopPolicyCheckWithSnapshotWithEvaluatorCacheAndEvidence(
		root, state, evaluator, stopCache, &taskSnapshot, loadedEvidence,
	)
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
			logErr := logRunStopDecision(root, "policy_block_stop_hook_active", payload, runtimeName, currentRun, currentRun, true, len(violations))
			return Result{ExitCode: 0, Stderr: bestEffortStopDecisionDiagnostic(logErr)}
		}
		// A user stop/interrupt must always win. Runtimes like Cursor never set
		// StopHookActive, so without this escape an unresolved blocking violation
		// (e.g. an untracked file) traps the session in an unbreakable stop loop.
		// If this exact block already fired on the previous stop, let the stop
		// through: the agent has seen the report once and either cannot or was
		// told not to resolve it. This is the Cursor-equivalent of the
		// StopHookActive escape above.
		vh, hashErr := hashBlockingViolations(violations)
		if hashErr != nil {
			return resultWithEncodingError(Result{ExitCode: 2}, hashErr)
		}
		if !payload.StrictContinuation && vh != "" && state.LastStopBlockViolationHash == vh {
			logErr := logRunStopDecision(root, "policy_block_released_on_repeat", payload, runtimeName, currentRun, currentRun, true, len(violations))
			return Result{ExitCode: 0, Stderr: bestEffortStopDecisionDiagnostic(logErr)}
		}
		logErr := logRunStopDecision(root, "policy_block", payload, runtimeName, currentRun, currentRun, true, len(violations))
		blockOutput, stateErr := stopBlockJSONOutput(root, state.SessionID, report, violations)
		if blockOutput == "" && stateErr != nil {
			return resultWithEncodingError(Result{ExitCode: 2, Stderr: bestEffortStopDecisionDiagnostic(logErr)}, stateErr)
		}
		return Result{ExitCode: 0, Stdout: blockOutput, Stderr: joinStderr(bestEffortStopDecisionDiagnostic(logErr), stopBlockStateDiagnostic(stateErr))}
	}

	if terminalResult, handled, terminalErr := finalizeCleanStop(root, state.SessionID, policyResult); terminalErr != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): finalize clean Stop: %s", terminalErr)}
	} else if handled {
		return terminalResult
	}
	if checkpointDue {
		if err := markRepoPolicyCheckpoint(root, runFile, payload, runtimeName, state.MaterialEvents); err != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: checkpoint: %s", err)}
		}
	}

	if repositoryRunEnabled(runState) {
		if contResult, contHandled, err := runRepositoryContinuation(root, runFile, payload, runtimeName, taskState.RunState); err != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: %s", err)}
		} else if contHandled {
			return contResult
		}
	}

	return Result{ExitCode: 0}
}

func applyStopRunStateClose(result Result, terminalDecision stopTerminalDecision, closeErr error) Result {
	if closeErr == nil {
		return result
	}
	warning := "reconc run: close state: " + truncateBytes(closeErr.Error(), stopCloseDiagnosticMaxBytes)
	if terminalDecision == stopTerminalDecisionUserInterrupt || result.Stdout != "" || result.ExitCode != 0 {
		result.Stderr = joinStderr(result.Stderr, warning)
		return result
	}
	result.ExitCode = 2
	result.Stderr = joinStderr(result.Stderr, warning)
	return result
}

func finalizeCleanStop(root, sessionID string, evaluated stopPolicyCheckResult) (Result, bool, error) {
	currentTask, err := captureStopTaskSnapshot(root)
	if err != nil {
		return Result{}, false, fmt.Errorf("recapture TASK snapshot: %w", err)
	}
	currentCacheGit := stopPolicyGitSnapshotFor(root)
	if !reflect.DeepEqual(evaluated.TaskSnapshot, currentTask) || evaluated.GitSnapshot != currentCacheGit {
		return Result{}, false, fmt.Errorf("terminal repository state changed during Stop evaluation")
	}
	if _, err := mutateSessionStateResolved(root, sessionID, func(state SessionState) SessionState {
		state.LastStopBlockViolationHash = ""
		return state
	}); err != nil {
		return Result{}, false, fmt.Errorf("clear repeated-block state: %w", err)
	}
	terminalGit := completionPolicyGitSnapshotFor(root)
	return taskCompletionCommitGate(currentTask, terminalGit)
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
		if logErr := appendRunStopDecision(root, "repo_policy_checkpoint", payload, runtimeName, before, after, false, 0); logErr != nil {
			return fmt.Errorf("record repository policy checkpoint: %w", logErr)
		}
	}
	return err
}

func taskCompletionCommitGate(taskSnapshot stopTaskSnapshot, snapshot stopPolicyGitSnapshot) (Result, bool, error) {
	cfg := taskSnapshot.Config
	if !cfg.Enabled || !cfg.Completion.RequireCommitted {
		return Result{}, false, nil
	}
	state := taskSnapshot.State
	if state.Disposition != tasklifecycle.RunComplete {
		return Result{}, false, nil
	}
	if !snapshot.StatusOK {
		return repositoryRunBlockResult("reconc blocked terminal TASK completion because Git status could not be verified. Restore Git status visibility or interrupt explicitly."), true, nil
	}
	dirty := tasklifecycle.DirtyCompletionPaths(cfg, dirtyPathsFromStatus(snapshot.Status))
	if len(dirty) == 0 {
		return Result{}, false, nil
	}
	return repositoryRunBlockResult("reconc blocked terminal TASK completion because the TASK control plane is not committed: " + strings.Join(dirty, ", ") + ". Commit the completed TASK or interrupt explicitly."), true, nil
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
		_, err = mutateSessionStateResolved(root, sessionIDFromPayload(payload), func(state SessionState) SessionState {
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
		contResult = repositoryRunBlockResult(prompt)
		contHandled = true
		return after
	})
	if err != nil {
		return Result{}, false, err
	}
	if decisionBranch == "run_followup" || decisionBranch == "repo_no_progress_release" {
		if shouldLogRunContinuation(decisionBranch, before, after, sessionNudges, strictContinuation) {
			if err := logRunContinuationDecision(root, decisionBranch, payload, runtimeName, before, after, sessionNudges, strictContinuation); err != nil {
				return Result{}, false, fmt.Errorf("record repository continuation: %w", err)
			}
		}
	} else if decisionBranch != "" && before != after {
		if err := appendRunStopDecision(root, decisionBranch, payload, runtimeName, before, after, false, 0); err != nil {
			return Result{}, false, fmt.Errorf("record repository transition: %w", err)
		}
	}
	return contResult, contHandled, nil
}

func shouldLogRunContinuation(branch string, before, after repositoryRunState, nudges int, strict bool) bool {
	if branch == "repo_no_progress_release" {
		return true
	}
	if before.AwaitingContinuation != after.AwaitingContinuation || before.LastProgressHash != after.LastProgressHash {
		return true
	}
	if strict {
		return false
	}
	return nudges == 3 || nudges == 5
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

func bestEffortStopDecisionDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	return "reconc run decision log (warn): " + truncateBytes(err.Error(), 4096)
}

func stopBlockStateDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	return "reconc stop state (warn): " + truncateBytes(err.Error(), 4096)
}
