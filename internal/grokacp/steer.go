package grokacp

import (
	"fmt"
	"os"
	"strings"
	"time"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

// SteerEnv disables leader stop steering when set to 0/false/off. The strict
// ACP runner exports it into its spawned Grok agent so the runner's own
// prompt loop stays the only continuation driver there.
const SteerEnv = "RECONC_GROK_STEER"

const (
	// maxStopSteerAttempts bounds interjections per session, mirroring the
	// ACP runner's continuation budget, so a policy that can never be
	// satisfied cannot ping-pong the TUI forever.
	maxStopSteerAttempts = 32
	steerBudget          = 3 * time.Second
)

// steerDial is swappable for tests.
var steerDial = dialLeader

// SteerTUIStop upgrades a passive Grok TUI Stop into an active continuation.
// Grok ignores Stop hook output, but a leader-hosted session accepts
// x.ai/interject over the leader socket, and an interjection landing on an
// idle session starts a new prompt turn immediately. Called from the
// grok-stop hook route with the normalized payload and the Stop evaluation
// result; returns a stderr note ("" when steering does not apply). Strictly
// fail-open: any failure leaves the passive stop report in place.
func SteerTUIStop(repoRoot string, payloadBytes []byte, stopResult agentsession.Result) string {
	if stopResult.ExitCode != 0 {
		return ""
	}
	reason := continuationReason(stopResult.Stdout)
	if reason == "" {
		return ""
	}
	if SteeringDisabled() {
		return ""
	}
	payload, err := agentsession.ParsePayload(payloadBytes)
	if err != nil || strings.TrimSpace(payload.SessionID) == "" {
		return ""
	}
	if payload.IsInterrupt != nil && *payload.IsInterrupt {
		return ""
	}
	// Grok exports GROK_SESSION_ID into every dispatched hook process. Its
	// absence means this Stop did not come from a live Grok dispatch, so
	// there is no session worth steering (and no risk of poking a leader
	// with a hand-crafted envelope).
	if strings.TrimSpace(os.Getenv("GROK_SESSION_ID")) != payload.SessionID {
		return ""
	}
	candidates := leaderSocketCandidates()
	if len(candidates) == 0 {
		// The normal non-leader TUI: no socket, steering silently passive.
		return ""
	}

	attempts, err := incrementSteerAttempts(repoRoot, payload.SessionID)
	if err != nil {
		return "reconc grok steer: " + err.Error()
	}
	if attempts > maxStopSteerAttempts {
		return fmt.Sprintf("reconc grok steer: %d-attempt budget exhausted; continuation left passive", maxStopSteerAttempts)
	}

	deadline := time.Now().Add(steerBudget)
	var lastErr error
	for _, socketPath := range candidates {
		if err := interjectViaLeader(socketPath, deadline, payload.SessionID, reason); err != nil {
			lastErr = err
			continue
		}
		return fmt.Sprintf("reconc grok steer: continuation interjected (%d/%d)", attempts, maxStopSteerAttempts)
	}
	return "reconc grok steer failed: " + lastErr.Error()
}

func interjectViaLeader(socketPath string, deadline time.Time, sessionID, text string) error {
	conn, err := steerDial(socketPath, deadline)
	if err != nil {
		return err
	}
	defer conn.close()
	if err := conn.register(); err != nil {
		return err
	}
	return conn.interject(sessionID, text)
}

// SteeringDisabled reports whether RECONC_GROK_STEER turns leader stop
// steering off. Shared with the deep-doctor check.
func SteeringDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(SteerEnv))) {
	case "0", "false", "off", "no":
		return true
	default:
		return false
	}
}

// incrementSteerAttempts advances the per-session steering counter under the
// session lock and returns the new value. Counting attempts (not successes)
// keeps the budget monotonic even when interjections fail mid-flight.
func incrementSteerAttempts(repoRoot, sessionID string) (uint64, error) {
	state, err := agentsession.MutateSessionState(repoRoot, sessionID, func(state agentsession.SessionState) agentsession.SessionState {
		if state.GrokSteerAttempts < ^uint64(0) {
			state.GrokSteerAttempts++
		}
		return state
	})
	if err != nil {
		return 0, fmt.Errorf("record steer attempt: %s", err)
	}
	return state.GrokSteerAttempts, nil
}
