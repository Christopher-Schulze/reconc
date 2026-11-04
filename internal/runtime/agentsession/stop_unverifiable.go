package agentsession

import (
	stderrors "errors"
	"fmt"

	rerrors "reconc.dev/reconc/internal/errors"
)

// errStopInputsUnstable marks a Stop whose inputs kept changing across the
// bounded evaluation retries, which means another writer is active.
var errStopInputsUnstable = stderrors.New("stop inputs changed")

// errStopTerminalStateMoved marks a Stop whose terminal repository state changed
// after its clean evaluation, for the same reason.
var errStopTerminalStateMoved = stderrors.New("terminal repository state changed during Stop evaluation")

const (
	// unverifiableStopMarker identifies a held unverifiable Stop in the repeated
	// block state, for hosts that never set stop_hook_active.
	unverifiableStopMarker = "unverifiable-stop"

	policySourceRepairHint = "Policy cannot be enforced while its source is invalid, and agents cannot change it in that state. Ask the operator to fix the file or restore it from Git, then run `reconc refresh .`."

	// stopFailureDetailMaxBytes bounds one failure detail that reaches the agent.
	stopFailureDetailMaxBytes = 1024
)

func isPolicySourceError(err error) bool {
	var sourceErr *rerrors.PolicySourceError
	return stderrors.As(err, &sourceErr)
}

func policySourceBlockMessage(surface string, err error) string {
	return "reconc hook (" + surface + "): " + truncateBytes(err.Error(), stopFailureDetailMaxBytes) + ". " + policySourceRepairHint
}

// preCheckFailureMessage names the reachable repair when a pre-tool check failed
// on an invalid policy source. Everything stays blocked in that state.
func preCheckFailureMessage(surface, check string, err error) string {
	if isPolicySourceError(err) {
		return policySourceBlockMessage(surface, err)
	}
	return fmt.Sprintf("reconc hook (%s): %s failed: %s", surface, check, err)
}

// unverifiableStopResult decides a Stop whose policy cannot be evaluated. The
// first Stop holds the session so the agent sees why. Holding it again cannot
// help, since the agent can neither repair an unenforceable policy nor finish
// another writer's work, so the next one ends the session as uncertified: it is
// never reported as clean, and the commit gate still evaluates the repository.
// Strict continuation hosts keep their no-release contract.
func unverifiableStopResult(root string, payload *HookPayload, message string) Result {
	held := Result{ExitCode: 2, Stderr: message}
	if payload.StrictContinuation {
		return held
	}
	if _, err := ensureSessionStateResolved(root, payload.SessionID); err != nil {
		return held
	}
	release := payload.StopHookActive
	if _, err := mutateSessionStateResolved(root, payload.SessionID, func(current SessionState) SessionState {
		release = release || current.LastStopBlockViolationHash == unverifiableStopMarker
		current.LastStopBlockViolationHash = unverifiableStopMarker
		if release {
			current.UncertifiedTermination = true
		}
		return current
	}); err != nil {
		return held
	}
	if !release {
		return held
	}
	return Result{ExitCode: 0, Stderr: message + " Stop released as uncertified."}
}

func stopInputsMoving(err error) bool {
	return stderrors.Is(err, errStopInputsUnstable) || stderrors.Is(err, errStopTerminalStateMoved)
}

// stopFinalizeFailureResult maps a failed terminal check to its host response.
func stopFinalizeFailureResult(root string, payload *HookPayload, backgroundWork int, err error) Result {
	if stopInputsMoving(err) {
		return stopCheckFailureResult(root, payload, backgroundWork, err)
	}
	return Result{ExitCode: 2, Stderr: "reconc hook (stop): finalize clean Stop: " + truncateBytes(err.Error(), stopFailureDetailMaxBytes)}
}

// stopCheckFailureResult maps a failed Stop policy check to its host response.
func stopCheckFailureResult(root string, payload *HookPayload, backgroundWork int, err error) Result {
	detail := truncateBytes(err.Error(), stopFailureDetailMaxBytes)
	switch {
	case isLockfileError(err):
		// A stale lockfile must still hold the session open, but it must not
		// repeat an unreachable remediation forever. The pre-tool route admits
		// the repair, so this block is now actionable.
		return Result{ExitCode: 2, Stderr: lockfileBlockMessage("stop", err)}
	case isPolicySourceError(err):
		return unverifiableStopResult(root, payload, policySourceBlockMessage("stop", err))
	case stopInputsMoving(err) && backgroundWork > 0:
		// Such a Stop pauses the session; the host fires another Stop after that
		// work settles, and that Stop evaluates the settled repository.
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (stop): %s while %d background task(s) or wakeup(s) run; verification waits for the Stop after that work settles", detail, backgroundWork)}
	case stopInputsMoving(err):
		return unverifiableStopResult(root, payload, fmt.Sprintf("reconc hook (stop): %s, so another writer is active. Wait until it finishes, then stop again.", detail))
	}
	return Result{ExitCode: 2, Stderr: "reconc hook (stop): check failed: " + detail}
}
