package agentsession

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	if pause, paused := activeHookPause(root, time.Now()); paused {
		return Result{ExitCode: 0, Stderr: pause.notice()}
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
	backgroundWork := pendingBackgroundWork(payload)

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
		if !checkpointDue && backgroundWork == 0 {
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
		// Without a stored report the evidence hash cannot match anything.
		evidenceHash := ""
		if state.StopPolicyEvidenceHash != "" && state.StopPolicyReportHash != "" {
			hash, hashErr := stopPolicyEvidenceHash(state)
			if hashErr != nil {
				return resultWithEncodingError(Result{ExitCode: 2}, hashErr)
			}
			evidenceHash = hash
		}
		if cached, ok := cachedCleanStopPolicyReportForEvidenceWithCache(root, state, evidenceHash, stopCache, taskSnapshot); ok {
			if terminalResult, handled, terminalErr := finalizeCleanStop(root, state, cached); terminalErr != nil {
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

	if terminalResult, handled, terminalErr := finalizeCleanStop(root, state, policyResult); terminalErr != nil {
		return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (stop): finalize clean Stop: %s", terminalErr)}
	} else if handled {
		return terminalResult
	}
	if checkpointDue {
		if err := markRepoPolicyCheckpoint(root, runFile, payload, runtimeName, state.MaterialEvents); err != nil {
			return Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc run: checkpoint: %s", err)}
		}
	}

	if backgroundWork > 0 {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (stop): %d background task(s) or wakeup(s) still pending; the session is paused, so repository run waits for the Stop after that work settles", backgroundWork)}
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

// pendingBackgroundWork counts Claude Code's in-flight background tasks and
// one-shot wakeups. Such a Stop pauses the session rather than ending it; the
// host fires another Stop once that work wakes the session and settles.
// Recurring crons re-prompt a finished session and do not hold it open.
func pendingBackgroundWork(payload *HookPayload) int {
	if payload == nil {
		return 0
	}
	pending := 0
	if tasks, ok := payload.Raw["background_tasks"].([]interface{}); ok {
		pending += len(tasks)
	}
	if crons, ok := payload.Raw["session_crons"].([]interface{}); ok {
		for _, cron := range crons {
			if entry, ok := cron.(map[string]interface{}); ok && entry["recurring"] == false {
				pending++
			}
		}
	}
	return pending
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

func finalizeCleanStop(root string, state SessionState, evaluated stopPolicyCheckResult) (Result, bool, error) {
	currentTask, err := captureStopTaskSnapshot(root)
	if err != nil {
		return Result{}, false, fmt.Errorf("recapture TASK snapshot: %w", err)
	}
	currentCacheGit := evaluated.GitSnapshot
	if !evaluated.GitSnapshotCurrent {
		currentCacheGit = stopPolicyGitSnapshotFor(root)
	}
	if !reflect.DeepEqual(evaluated.TaskSnapshot, currentTask) || evaluated.GitSnapshot != currentCacheGit {
		return Result{}, false, fmt.Errorf("terminal repository state changed during Stop evaluation: %s",
			terminalStateChangeDetail(evaluated, currentTask, currentCacheGit))
	}
	// Only a Stop that blocked records the repeated-block hash; a session that
	// never blocked has nothing to clear.
	if state.LastStopBlockViolationHash != "" {
		if _, err := mutateSessionStateResolved(root, state.SessionID, func(state SessionState) SessionState {
			state.LastStopBlockViolationHash = ""
			return state
		}); err != nil {
			return Result{}, false, fmt.Errorf("clear repeated-block state: %w", err)
		}
	}
	if !taskCompletionCommitGateApplies(currentTask) {
		return Result{}, false, nil
	}
	terminalGit := completionPolicyGitSnapshotFor(root)
	return taskCompletionCommitGate(currentTask, terminalGit)
}

// maxReportedTerminalChanges bounds the paths a terminal-change block names.
const maxReportedTerminalChanges = 8

// terminalStateChangeDetail names what changed between the evaluated and the
// terminal repository observation, so a script or tool that writes during
// Stop can be found and made side-effect free or gitignored.
func terminalStateChangeDetail(evaluated stopPolicyCheckResult, currentTask stopTaskSnapshot, currentGit stopPolicyGitSnapshot) string {
	changes := []string{}
	if !reflect.DeepEqual(evaluated.TaskSnapshot, currentTask) {
		changes = append(changes, "the TASK board")
	}
	if evaluated.GitSnapshot.Head != currentGit.Head {
		changes = append(changes, "HEAD")
	}
	before := stopStatusEntries(evaluated.GitSnapshot.Status)
	after := stopStatusEntries(currentGit.Status)
	paths := []string{}
	for path, code := range after {
		if before[path] != code {
			paths = append(paths, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	if len(paths) > maxReportedTerminalChanges {
		paths = append(paths[:maxReportedTerminalChanges], fmt.Sprintf("%d more", len(paths)-maxReportedTerminalChanges))
	}
	changes = append(changes, paths...)
	if evaluated.GitSnapshot.StatusOK != currentGit.StatusOK || evaluated.GitSnapshot.StatusMode != currentGit.StatusMode {
		changes = append(changes, "Git status availability")
	}
	if len(changes) == 0 {
		return "repository observation differs"
	}
	return strings.Join(changes, ", ") + "; a check that writes these must write outside the repository or to gitignored paths"
}

// stopStatusEntries maps each path of a NUL-separated porcelain status to
// its two-letter state; a rename or copy source maps to the same state.
func stopStatusEntries(status string) map[string]string {
	records := strings.Split(status, "\x00")
	entries := make(map[string]string, len(records))
	for index := 0; index < len(records); index++ {
		record := records[index]
		if len(record) < 4 || record[2] != ' ' {
			continue
		}
		entries[filepath.ToSlash(record[3:])] = record[:2]
		if isRenameOrCopyStatus(record[0], record[1]) && index+1 < len(records) {
			index++
			entries[filepath.ToSlash(records[index])] = record[:2]
		}
	}
	return entries
}

// taskCompletionCommitGateApplies reports whether the committed-completion
// gate inspects Git at all, so the complete untracked scan runs only for a
// terminal TASK board under require_committed.
func taskCompletionCommitGateApplies(taskSnapshot stopTaskSnapshot) bool {
	cfg := taskSnapshot.Config
	return cfg.Enabled && cfg.Completion.RequireCommitted && taskSnapshot.State.Disposition == tasklifecycle.RunComplete
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
	if !taskCompletionCommitGateApplies(taskSnapshot) {
		return Result{}, false, nil
	}
	cfg := taskSnapshot.Config
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
	currentRun, err := loadRepositoryRunStateResolved(root)
	if err != nil {
		return Result{}, false, err
	}
	if !repositoryRunEnabled(currentRun) {
		return Result{}, false, nil
	}
	runEpoch := currentRun.EnabledAt
	if prompt != "" {
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
			terminal, branch, handled := repositoryRunTerminalTransition(current, taskState, runEpoch)
			if !handled {
				return current
			}
			decisionBranch = branch
			contResult = Result{ExitCode: 0}
			contHandled = true
			return terminal
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

func repositoryRunTerminalTransition(
	current repositoryRunState,
	taskState tasklifecycle.RunState,
	observedEpoch int64,
) (repositoryRunState, string, bool) {
	if !repositoryRunEnabled(current) || current.EnabledAt != observedEpoch {
		return current, "", false
	}
	after := repositoryRunState{DisabledReason: repositoryRunTerminalReason(taskState)}
	return after, "disable_" + after.DisabledReason.String(), true
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
