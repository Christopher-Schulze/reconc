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
var (
	steerDial               = dialLeader
	nativeStopGateAvailable = func() bool { return ProbeNativeStopGate().Supported }
)

// PrepareStrictTUIStop marks every eligible live Grok Stop as strict before
// policy evaluation. Strict payload semantics do not depend on a leader or on
// the optional leader-steering switch; host enforcement is capability-probed.
func PrepareStrictTUIStop(payloadBytes []byte) ([]byte, bool, error) {
	payload, err := liveTUIStopPayload(payloadBytes)
	if err != nil || payload == nil {
		return payloadBytes, false, err
	}
	payload.Raw["strict_continuation"] = true
	body, err := json.Marshal(payload.Raw)
	if err != nil {
		return payloadBytes, false, fmt.Errorf("encode strict Grok stop payload: %w", err)
	}
	return body, true, nil
}

// SteerTUIStop supplies backward-compatible continuation through an optional
// leader. When the installed Grok distribution advertises native Stop
// enforcement, leader interjection is suppressed to avoid delivering the same
// continuation twice.
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
	candidates, err := leaderSocketCandidates()
	if err != nil {
		return "reconc grok steer: discover leader endpoints: " + err.Error()
	}
	if len(candidates) == 0 {
		return ""
	}

	attempt, allowed, err := prepareSteerAttempt(repoRoot, payload.SessionID, reason)
	if err != nil {
		return "reconc grok steer: " + err.Error()
	}
	if !allowed {
		return fmt.Sprintf("reconc grok steer: %d-attempt leader fallback budget exhausted", maxStopSteerAttempts)
	}

	overallDeadline := time.Now().Add(steerBudget)
	var lastErr error
	nativeLeaderSeen := false
	for index, endpoint := range candidates {
		deadline := fairCandidateDeadline(overallDeadline, len(candidates)-index)
		interjected, err := interjectViaLeader(endpoint, deadline, payload.SessionID, reason)
		if err != nil {
			lastErr = err
			continue
		}
		if !interjected {
			nativeLeaderSeen = true
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
	if nativeLeaderSeen {
		return ""
	}
	if lastErr == nil {
		return ""
	}
	return "reconc grok steer failed: " + lastErr.Error()
}

func interjectViaLeader(endpoint string, deadline time.Time, sessionID, text string) (bool, error) {
	conn, err := steerDial(endpoint, deadline)
	if err != nil {
		return false, err
	}
	defer conn.close()
	registration, err := conn.register()
	if err != nil {
		return false, err
	}
	if err := validateLeaderProtocol(registration); err != nil {
		return false, err
	}
	if nativeStopGateAvailable() {
		return false, nil
	}
	if err := conn.interject(sessionID, text); err != nil {
		return false, err
	}
	return true, nil
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

func activeSteerPayload(payloadBytes []byte) (*agentsession.HookPayload, error) {
	if SteeringDisabled() {
		return nil, nil
	}
	return liveTUIStopPayload(payloadBytes)
}

func liveTUIStopPayload(payloadBytes []byte) (*agentsession.HookPayload, error) {
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
	switch strings.ToLower(strings.TrimSpace(payloadReason(payload))) {
	case "channel_closed", "shutdown":
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

func payloadReason(payload *agentsession.HookPayload) string {
	reason, _ := payload.Raw["reason"].(string)
	return reason
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
