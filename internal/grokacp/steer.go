package grokacp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	// maxStopSteerAttempts bounds each consecutive no-progress series,
	// mirroring the ACP runner's continuation budget.
	maxStopSteerAttempts = 32
	steerBudget          = 3 * time.Second
)

// steerDial is swappable for tests.
var steerDial = dialLeader

// PrepareStrictTUIStop marks an eligible native Grok leader Stop as strict
// before policy evaluation. Without this bit the generic repeated-block escape
// releases the same violation on the second Stop before steering can interject
// another continuation.
func PrepareStrictTUIStop(payloadBytes []byte) ([]byte, bool, error) {
	payload, candidates, err := activeSteerTarget(payloadBytes)
	if err != nil || payload == nil || len(candidates) == 0 {
		return payloadBytes, false, err
	}
	payload.Raw["strict_continuation"] = true
	body, err := json.Marshal(payload.Raw)
	if err != nil {
		return payloadBytes, false, fmt.Errorf("encode strict Grok stop payload: %w", err)
	}
	return body, true, nil
}

// SteerTUIStop upgrades a passive Grok TUI Stop into an active continuation.
// Grok ignores Stop hook output, but a leader-hosted session accepts
// _x.ai/interject over the leader endpoint, and an interjection landing on an
// idle session starts a new prompt turn immediately. Called from the
// grok-stop hook route with the normalized payload and the Stop evaluation
// result; returns a stderr note ("" when steering does not apply). Strictly
// fail-open: any failure leaves the passive stop report in place.
func SteerTUIStop(repoRoot string, payloadBytes []byte, stopResult agentsession.Result) string {
	if stopResult.ExitCode != 0 {
		return ""
	}
	payload, err := activeSteerPayload(payloadBytes)
	if err != nil || payload == nil {
		return ""
	}
	reason := continuationReason(stopResult.Stdout)
	if reason == "" {
		if err := resetSteerBudget(repoRoot, payload.SessionID); err != nil {
			return "reconc grok steer: " + err.Error()
		}
		return ""
	}
	candidates := leaderSocketCandidates()
	if len(candidates) == 0 {
		return ""
	}

	attempt, allowed, err := prepareSteerAttempt(repoRoot, payload.SessionID, reason)
	if err != nil {
		return "reconc grok steer: " + err.Error()
	}
	if !allowed {
		return fmt.Sprintf("reconc grok steer: %d-attempt budget exhausted; continuation left passive", maxStopSteerAttempts)
	}

	overallDeadline := time.Now().Add(steerBudget)
	var lastErr error
	for index, endpoint := range candidates {
		deadline := fairCandidateDeadline(overallDeadline, len(candidates)-index)
		if err := interjectViaLeader(endpoint, deadline, payload.SessionID, reason); err != nil {
			lastErr = err
			continue
		}
		attempts, counted, err := commitSteerAttempt(repoRoot, payload.SessionID, attempt)
		if err != nil {
			return "reconc grok steer: continuation interjected; " + err.Error()
		}
		if !counted {
			return "reconc grok steer: continuation interjected after material progress; no-progress budget unchanged"
		}
		return fmt.Sprintf("reconc grok steer: continuation interjected (%d/%d)", attempts, maxStopSteerAttempts)
	}
	if lastErr == nil {
		return ""
	}
	return "reconc grok steer failed: " + lastErr.Error()
}

func interjectViaLeader(endpoint string, deadline time.Time, sessionID, text string) error {
	conn, err := steerDial(endpoint, deadline)
	if err != nil {
		return err
	}
	defer conn.close()
	registration, err := conn.register()
	if err != nil {
		return err
	}
	if err := validateLeaderProtocol(registration); err != nil {
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

func activeSteerTarget(payloadBytes []byte) (*agentsession.HookPayload, []string, error) {
	payload, err := activeSteerPayload(payloadBytes)
	if err != nil || payload == nil {
		return nil, nil, err
	}
	candidates := leaderSocketCandidates()
	if len(candidates) == 0 {
		return nil, nil, nil
	}
	return payload, candidates, nil
}

func activeSteerPayload(payloadBytes []byte) (*agentsession.HookPayload, error) {
	if SteeringDisabled() {
		return nil, nil
	}
	payload, err := agentsession.ParsePayload(payloadBytes)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.SessionID) == "" {
		return nil, nil
	}
	if payload.IsInterrupt != nil && *payload.IsInterrupt {
		return nil, nil
	}
	// Grok exports GROK_SESSION_ID into every dispatched hook process. Its
	// absence means this Stop did not come from a live Grok dispatch, so
	// there is no session worth steering.
	if strings.TrimSpace(os.Getenv("GROK_SESSION_ID")) != payload.SessionID {
		return nil, nil
	}
	return payload, nil
}

type steerAttempt struct {
	continuationKey string
	materialEvents  uint64
}

// prepareSteerAttempt validates the current no-progress series without
// consuming budget. Only a successfully delivered interjection is committed.
func prepareSteerAttempt(repoRoot, sessionID, reason string) (steerAttempt, bool, error) {
	attempt := steerAttempt{continuationKey: steerContinuationKey(reason)}
	allowed := false
	state, err := agentsession.MutateSessionState(repoRoot, sessionID, func(state agentsession.SessionState) agentsession.SessionState {
		if state.GrokSteerContinuationKey != attempt.continuationKey || state.GrokSteerMaterialEvents != state.MaterialEvents {
			state.GrokSteerAttempts = 0
		}
		state.GrokSteerContinuationKey = attempt.continuationKey
		state.GrokSteerMaterialEvents = state.MaterialEvents
		attempt.materialEvents = state.MaterialEvents
		allowed = state.GrokSteerAttempts < maxStopSteerAttempts
		return state
	})
	if err != nil {
		return steerAttempt{}, false, fmt.Errorf("prepare steer attempt: %s", err)
	}
	if !allowed {
		attempt.materialEvents = state.MaterialEvents
	}
	return attempt, allowed, nil
}

func commitSteerAttempt(repoRoot, sessionID string, attempt steerAttempt) (uint64, bool, error) {
	counted := false
	state, err := agentsession.MutateSessionState(repoRoot, sessionID, func(state agentsession.SessionState) agentsession.SessionState {
		if state.GrokSteerContinuationKey != attempt.continuationKey ||
			state.MaterialEvents != attempt.materialEvents ||
			state.GrokSteerMaterialEvents != attempt.materialEvents {
			return state
		}
		if state.GrokSteerAttempts < maxStopSteerAttempts {
			state.GrokSteerAttempts++
			counted = true
		}
		return state
	})
	if err != nil {
		return 0, false, fmt.Errorf("record successful steer attempt: %s", err)
	}
	return state.GrokSteerAttempts, counted, nil
}

func resetSteerBudget(repoRoot, sessionID string) error {
	_, err := agentsession.MutateSessionState(repoRoot, sessionID, func(state agentsession.SessionState) agentsession.SessionState {
		state.GrokSteerAttempts = 0
		state.GrokSteerContinuationKey = ""
		state.GrokSteerMaterialEvents = 0
		return state
	})
	if err != nil {
		return fmt.Errorf("reset steer budget: %s", err)
	}
	return nil
}

func steerContinuationKey(reason string) string {
	for _, line := range strings.Split(reason, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Feedback:") {
			feedback := strings.TrimSpace(strings.TrimPrefix(line, "Feedback:"))
			if feedback != "" {
				return "feedback:" + feedback
			}
		}
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(reason)))
	return "reason:" + hex.EncodeToString(sum[:])
}
