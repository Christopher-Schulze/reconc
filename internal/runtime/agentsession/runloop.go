package agentsession

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/jsonl"
)

const reconcHookRuntimeEnv = "RECONC_HOOK_RUNTIME"

type runLoopMode string

const (
	runLoopModeSession runLoopMode = "session"
	runLoopModeRepo    runLoopMode = "repo"
)

// runLoopState mirrors the file contract consumed by the OpenCode
// runtime runloop plugin. The CLI/runtime only consumes the core
// fields needed to coordinate continue/stop behavior.
//
// The shape intentionally stays tiny so older plugin versions or
// hand-edited state files can still be parsed without strict
// coupling.
type runLoopState struct {
	Enabled              bool        `json:"enabled"`
	Mode                 runLoopMode `json:"mode,omitempty"`
	SessionID            string      `json:"session_id"`
	ActiveRunID          string      `json:"active_run_id"`
	Runtime              string      `json:"runtime,omitempty"`
	NoProgressNudges     int         `json:"no_progress_nudges"`
	DisabledReason       string      `json:"disabled_reason"`
	StopAnchorMessageID  string      `json:"stop_anchor_message_id"`
	LastHead             string      `json:"last_head,omitempty"`
	LastCurrent          string      `json:"last_current,omitempty"`
	AwaitingContinuation bool        `json:"awaiting_continuation,omitempty"`
	LastPromptSignature  string      `json:"last_prompt_signature,omitempty"`
}

// RunLoopDecision is one append-only record in
// .reconc/runloop/decisions.jsonl: every runloop state transition with the
// exact branch taken, so `reconc runloop log` can show why the runtime did
// what it did without grepping raw JSONL.
type RunLoopDecision struct {
	Timestamp                  string `json:"ts"`
	Event                      string `json:"event"`
	Branch                     string `json:"branch"`
	Runtime                    string `json:"runtime,omitempty"`
	SessionID                  string `json:"session_id,omitempty"`
	StateSessionID             string `json:"state_session_id,omitempty"`
	ActiveRunID                string `json:"active_run_id,omitempty"`
	Intent                     string `json:"intent,omitempty"`
	EnabledBefore              bool   `json:"enabled_before"`
	EnabledAfter               bool   `json:"enabled_after"`
	DisabledReasonBefore       string `json:"disabled_reason_before,omitempty"`
	DisabledReasonAfter        string `json:"disabled_reason_after,omitempty"`
	StopFileApplies            bool   `json:"stop_file_applies,omitempty"`
	RuntimeInternalPrompt      bool   `json:"runtime_internal_prompt,omitempty"`
	AwaitingContinuationBefore bool   `json:"awaiting_continuation_before,omitempty"`
	AwaitingContinuationAfter  bool   `json:"awaiting_continuation_after,omitempty"`
	StopHookActive             bool   `json:"stop_hook_active,omitempty"`
	OpenCodeContinuationDriver bool   `json:"opencode_continuation_driver,omitempty"`
	PolicyBlocked              bool   `json:"policy_blocked,omitempty"`
	ViolationCount             int    `json:"violation_count,omitempty"`
}

type runLoopStopMarker struct {
	SessionID   string `json:"session_id,omitempty"`
	ActiveRunID string `json:"active_run_id,omitempty"`
	Runtime     string `json:"runtime,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type runLoopEvent int

const (
	runLoopSessionStart runLoopEvent = iota
	runLoopUserPrompt
	runLoopToolEvent
	runLoopStopEvent
	runLoopSessionEnd
)

type runLoopIntent int

const (
	runLoopIntentNoop runLoopIntent = iota
	runLoopIntentEnable
)

var (
	hookPromptBlockPattern = regexp.MustCompile(`(?is)<hook_prompt\b[^>]*>.*?</hook_prompt>`)
	fencedCodeBlockPattern = regexp.MustCompile("(?s)```.*?```")
)

const (
	runLoopDecisionMaxBytes    = 2 * 1024 * 1024
	runLoopDecisionMaxArchives = 2
)

const legacyRunLoopContinuationPrompt = `runloop autocontinue. Continue the repository task lifecycle without asking for routine permission. No ceremony, no confirmation questions - just work.

Active: TASK = <task> | Sub-Task = <sub-task>. Read the live Current: pointer in docs/tasks.md yourself.

Quality gate (mandatory before any Done):
- Brutal efficient, performance- and efficiency-maximized, secure (deny-by-default, fail-closed), maintainable.
- NO gaps, nothing forgotten: implement every spec atom or own it via a concrete follow-up TASK. Never declare NO_SPEC_SURFACE without grepping docs/spec.md first.
- Read and adapt the Research Refs (a floor, not inspiration) before coding.
- Max out each feature's intended effect - innovative, not the smallest runnable approximation.
- Integrate into existing repository subsystems; never build a parallel/duplicate system (grep for the existing mechanism first).
- Same-TASK substantive tests, then a real Final Reality Check + Contradiction Check with concrete file:line evidence. Verify goal by goal, atomically - no sampling.
- Exactly one commit per TASK including git rm of the archived task path; never bundle TASKs, never stack uncommitted work.
- After every completed TASK, run the per-TASK Reality-Check loop in docs/task-loop-workflow.md before continuing: fresh-eyes, strict, paranoid, forensically deep, LINE BY LINE - zero guessing, nothing from memory, no sampling or spot-checks; verify every goal and every changed line hard and explicitly. Check for gaps; is this REALLY EXACTLY what we wanted or something else; does it honestly meet our quality standards (Hard Quality Mandate)? If there is any potential work, ALWAYS do it, then re-run the loop - repeat until the honest hard Reality-Check finds nothing left to do. Only then continue.

After a completed TASK promote/resume the next executable TASK and continue immediately.
Stop only for: user stop, destructive/high-risk choice, missing credentials/access, unresolved test/build failure after root-cause attempts, Reconc/spec/policy conflict needing user direction, repeated no-progress, or the zero-finding Terminal Gate in workflow-complete-loop.md.
Never auto-push. Never touch _drop/, research/, or README.md unless explicitly instructed.`

func runLoopStatePath(repoRoot string) (string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".reconc", "runloop", "state.json"), nil
}

func runLoopStopPath(repoRoot string) (string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".reconc", "runloop", "stop"), nil
}

func runLoopDecisionLogPath(repoRoot string) (string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".reconc", "runloop", "decisions.jsonl"), nil
}

func appendRunLoopDecision(repoRoot string, decision RunLoopDecision) error {
	path, err := runLoopDecisionLogPath(repoRoot)
	if err != nil {
		return err
	}
	decision.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	decision = boundedRunLoopDecision(decision)
	body, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	return jsonl.Append(path, body, jsonl.Policy{MaxBytes: runLoopDecisionMaxBytes, MaxArchives: runLoopDecisionMaxArchives})
}

func boundedRunLoopDecision(decision RunLoopDecision) RunLoopDecision {
	decision.Event = truncateBytes(strings.TrimSpace(decision.Event), 256)
	decision.Branch = truncateBytes(strings.TrimSpace(decision.Branch), 256)
	decision.Runtime = truncateBytes(strings.TrimSpace(decision.Runtime), 256)
	decision.SessionID = truncateBytes(strings.TrimSpace(decision.SessionID), 4096)
	decision.StateSessionID = truncateBytes(strings.TrimSpace(decision.StateSessionID), 4096)
	decision.ActiveRunID = truncateBytes(strings.TrimSpace(decision.ActiveRunID), 4096)
	decision.Intent = truncateBytes(strings.TrimSpace(decision.Intent), 256)
	decision.DisabledReasonBefore = truncateBytes(strings.TrimSpace(decision.DisabledReasonBefore), 4096)
	decision.DisabledReasonAfter = truncateBytes(strings.TrimSpace(decision.DisabledReasonAfter), 4096)
	return decision
}

func runLoopEventName(event runLoopEvent) string {
	switch event {
	case runLoopSessionStart:
		return "session_start"
	case runLoopUserPrompt:
		return "user_prompt"
	case runLoopToolEvent:
		return "tool_event"
	case runLoopStopEvent:
		return "stop"
	case runLoopSessionEnd:
		return "session_end"
	default:
		return "unknown"
	}
}

func runLoopIntentName(intent runLoopIntent) string {
	switch intent {
	case runLoopIntentEnable:
		return "enable"
	default:
		return "noop"
	}
}

func sessionIDFromPayload(payload *HookPayload) string {
	if payload == nil {
		return ""
	}
	return strings.TrimSpace(payload.SessionID)
}

func clearRunLoopStopFile(repoRoot string) error {
	path, err := runLoopStopPath(repoRoot)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeRunLoopStopFile(repoRoot, sessionID, activeRunID, reason string) error {
	return writeRunLoopStopFileForRuntime(repoRoot, sessionID, activeRunID, "", reason)
}

func writeRunLoopStopFileForRuntime(repoRoot, sessionID, activeRunID, runtime, reason string) error {
	path, err := runLoopStopPath(repoRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	marker := runLoopStopMarker{
		SessionID:   strings.TrimSpace(sessionID),
		ActiveRunID: strings.TrimSpace(activeRunID),
		Runtime:     strings.TrimSpace(runtime),
		Reason:      strings.TrimSpace(reason),
	}
	body, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	_, err = atomicfile.WriteIfChanged(path, body, 0o644)
	return err
}

func hasRunLoopStopFile(repoRoot string) bool {
	path, err := runLoopStopPath(repoRoot)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func readRunLoopStopMarker(repoRoot string) (runLoopStopMarker, bool, bool) {
	path, err := runLoopStopPath(repoRoot)
	if err != nil {
		return runLoopStopMarker{}, false, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return runLoopStopMarker{}, false, false
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return runLoopStopMarker{}, true, true
	}
	var marker runLoopStopMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return runLoopStopMarker{}, true, true
	}
	marker.SessionID = strings.TrimSpace(marker.SessionID)
	marker.ActiveRunID = strings.TrimSpace(marker.ActiveRunID)
	marker.Runtime = strings.TrimSpace(marker.Runtime)
	marker.Reason = strings.TrimSpace(marker.Reason)
	return marker, true, false
}

func runLoopStopFileAppliesToState(repoRoot string, state runLoopState) bool {
	marker, exists, legacy := readRunLoopStopMarker(repoRoot)
	if !exists {
		return false
	}
	if legacy {
		return true
	}
	return runLoopMarkerMatchesState(marker, state)
}

func runLoopMarkerMatchesState(marker runLoopStopMarker, state runLoopState) bool {
	if marker.Runtime != "" && state.Runtime != "" && marker.Runtime != state.Runtime {
		return false
	}
	activeRunID := strings.TrimSpace(state.ActiveRunID)
	sessionID := strings.TrimSpace(state.SessionID)
	if marker.ActiveRunID != "" && activeRunID != "" {
		return marker.ActiveRunID == activeRunID
	}
	if marker.SessionID != "" {
		return marker.SessionID == sessionID || marker.SessionID == activeRunID
	}
	return false
}

func runLoopSessionMatchesRuntime(state runLoopState, sessionID string, runtime string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	runtime = strings.TrimSpace(runtime)
	if state.Runtime != "" && runtime != "" && state.Runtime != runtime {
		return false
	}
	if state.ActiveRunID != "" {
		return sessionID == state.ActiveRunID
	}
	return sessionID == state.SessionID
}

func runLoopStateApplies(state runLoopState, sessionID, runtime string) bool {
	if !state.Enabled {
		return false
	}
	if state.Mode == runLoopModeRepo {
		return true
	}
	return runLoopSessionMatchesRuntime(state, sessionID, runtime)
}

func loadRunLoopState(repoRoot string) (runLoopState, error) {
	state, err := readRunLoopStateStored(repoRoot)
	if err != nil {
		return runLoopState{}, err
	}
	if state.Enabled && runLoopStopFileAppliesToState(repoRoot, state) {
		state.Enabled = false
		state.Mode = ""
		state.ActiveRunID = ""
		state.NoProgressNudges = 0
		state.DisabledReason = "stop_file"
		state.AwaitingContinuation = false
	}
	return state, nil
}

// readRunLoopStateStored returns the exact persisted state. State mutations
// use this form so a stop marker is converted into one durable transition,
// rather than being mistaken for an already-persisted disabled state.
func readRunLoopStateStored(repoRoot string) (runLoopState, error) {
	path, err := runLoopStatePath(repoRoot)
	if err != nil {
		return runLoopState{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return runLoopState{}, nil
		}
		return runLoopState{}, err
	}
	var state runLoopState
	if err := json.Unmarshal(data, &state); err != nil {
		return runLoopState{}, err
	}
	state.SessionID = strings.TrimSpace(state.SessionID)
	state.ActiveRunID = strings.TrimSpace(state.ActiveRunID)
	state.Runtime = strings.TrimSpace(state.Runtime)
	if state.NoProgressNudges < 0 {
		state.NoProgressNudges = 0
	}
	if state.Enabled && state.Mode == "" {
		state.Mode = runLoopModeSession
	}
	if !state.Enabled {
		state.Mode = ""
	}
	return state, nil
}

func runLoopStateLockPath(repoRoot string) (string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".reconc", "runloop", "state.lock"), nil
}

// withRunLoopLock serializes runloop state access with an exclusive file
// lock so concurrent `reconc hook runtime` processes (parallel agent tool
// events) cannot race a load-modify-write on state.json. Mirrors the
// per-session locking that SessionState already relies on.
func withRunLoopLock(repoRoot string, fn func() error) error {
	lockPath, err := runLoopStateLockPath(repoRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("mkdir runloop lock dir: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open runloop lock: %w", err)
	}
	defer file.Close()
	unlock, err := filelock.Lock(file)
	if err != nil {
		return fmt.Errorf("lock runloop state: %w", err)
	}
	defer func() { _ = unlock() }()
	return fn()
}

// writeRunLoopStateAtomic writes state via tmp-file-then-rename so a
// concurrent reader never observes a truncated/half-written file (a bare
// os.WriteFile truncates first, and a reader hitting that window decodes an
// empty file into a disabled zero-value state). Callers doing
// read-modify-write MUST hold withRunLoopLock; this primitive only
// guarantees each individual write is atomic, not that two writes serialize.
func writeRunLoopStateAtomic(repoRoot string, state runLoopState) error {
	path, err := runLoopStatePath(repoRoot)
	if err != nil {
		return err
	}
	state.SessionID = strings.TrimSpace(state.SessionID)
	state.ActiveRunID = strings.TrimSpace(state.ActiveRunID)
	state.Runtime = strings.TrimSpace(state.Runtime)
	if state.NoProgressNudges < 0 {
		state.NoProgressNudges = 0
	}
	if state.Enabled && state.Mode == "" {
		state.Mode = runLoopModeSession
	}
	if !state.Enabled {
		state.Mode = ""
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runloop state: %w", err)
	}
	body = append(body, '\n')
	if _, err := atomicfile.WriteIfChanged(path, body, 0o644); err != nil {
		return fmt.Errorf("write runloop state: %w", err)
	}
	return nil
}

// saveRunLoopState atomically persists state while holding the runloop
// state lock. Use this for standalone/terminal writes; use mutateRunLoopState
// for read-modify-write so the load and save share a single lock.
func saveRunLoopState(repoRoot string, state runLoopState) error {
	return withRunLoopLock(repoRoot, func() error {
		return writeRunLoopStateAtomic(repoRoot, state)
	})
}

// mutateRunLoopState serializes load -> mutate -> save under one lock so
// concurrent agent tool events cannot lose each other's updates. Mirror of
// MutateSessionState. Corrupt or unreadable state fails closed and is never
// replaced with a zero value; atomic writes prevent torn-read recovery cases.
func mutateRunLoopState(repoRoot string, fn func(runLoopState) runLoopState) (runLoopState, runLoopState, error) {
	var before, after runLoopState
	err := withRunLoopLock(repoRoot, func() error {
		state, lerr := readRunLoopStateStored(repoRoot)
		if lerr != nil {
			return lerr
		}
		before = state
		after = fn(state)
		if before == after {
			return nil
		}
		return writeRunLoopStateAtomic(repoRoot, after)
	})
	return before, after, err
}

func reconcileRunLoopState(repoRoot, sessionID string, payload *HookPayload, event runLoopEvent) error {
	return reconcileRunLoopStateForRuntime(repoRoot, sessionID, "", payload, event)
}

func reconcileRunLoopStateForRuntime(repoRoot, sessionID, runtime string, payload *HookPayload, event runLoopEvent) error {
	sessionID = strings.TrimSpace(sessionID)
	runtime = strings.TrimSpace(runtime)
	intent := runLoopIntentFromUserPrompt(payload)
	internalPrompt := event == runLoopUserPrompt && isRuntimeInternalUserPrompt(payload)
	stored, err := readRunLoopStateStored(repoRoot)
	if err != nil {
		return err
	}
	if !stored.Enabled && intent != runLoopIntentEnable {
		return nil
	}

	var branch string
	var stopApplies bool
	before, after, err := mutateRunLoopState(repoRoot, func(state runLoopState) runLoopState {
		stopApplies = runLoopStopFileAppliesToState(repoRoot, state)
		next, b := computeRunLoopNextState(repoRoot, state, sessionID, runtime, payload, event, intent, internalPrompt, stopApplies)
		branch = b
		return next
	})
	if err != nil {
		return err
	}
	decision := RunLoopDecision{
		Event:                      runLoopEventName(event),
		Branch:                     branch,
		Runtime:                    runtime,
		SessionID:                  sessionIDFromPayload(payload),
		StateSessionID:             after.SessionID,
		ActiveRunID:                after.ActiveRunID,
		Intent:                     runLoopIntentName(intent),
		EnabledBefore:              before.Enabled,
		EnabledAfter:               after.Enabled,
		DisabledReasonBefore:       before.DisabledReason,
		DisabledReasonAfter:        after.DisabledReason,
		StopFileApplies:            stopApplies,
		RuntimeInternalPrompt:      internalPrompt,
		AwaitingContinuationBefore: before.AwaitingContinuation,
		AwaitingContinuationAfter:  after.AwaitingContinuation,
		StopHookActive:             payload != nil && payload.StopHookActive,
		OpenCodeContinuationDriver: payload != nil && payload.OpenCodeContinuationDriver,
	}
	if before != after || intent == runLoopIntentEnable || stopApplies {
		_ = appendRunLoopDecision(repoRoot, decision)
	}
	return nil
}

// computeRunLoopNextState is the pure state-transition core of the reconcile.
// It runs under withRunLoopLock (via mutateRunLoopState) and returns the
// next state plus the branch name for the decision log. Stop-file writes/clears
// are intentional side effects of specific transitions.
func computeRunLoopNextState(repoRoot string, state runLoopState, sessionID, runtime string, payload *HookPayload, event runLoopEvent, intent runLoopIntent, internalPrompt bool, stopApplies bool) (runLoopState, string) {
	if stopApplies && event != runLoopUserPrompt {
		state.Enabled = false
		state.ActiveRunID = ""
		state.NoProgressNudges = 0
		state.DisabledReason = "stop_file"
		state.AwaitingContinuation = false
	}
	branch := "preserve"

	switch event {
	case runLoopSessionStart:
		if stopApplies {
			state = runLoopState{SessionID: sessionID, Runtime: runtime, DisabledReason: "stop_file"}
			branch = "session_start_stop_file"
		} else if state.Enabled && state.Mode == runLoopModeRepo {
			branch = "session_start_preserve_repo"
		} else if !state.Enabled && sessionID != "" {
			state.SessionID = sessionID
			state.Runtime = runtime
			branch = "session_start_disabled"
		} else {
			branch = "session_start_preserve_active"
		}
	case runLoopUserPrompt:
		if state.Enabled && state.Mode == runLoopModeRepo {
			if intent == runLoopIntentEnable {
				_ = clearRunLoopStopFile(repoRoot)
				branch = "preserve_repo_enable_prompt"
			} else {
				branch = "preserve_repo_prompt"
			}
			return state, branch
		}
		if intent != runLoopIntentEnable && state.Enabled && !runLoopSessionMatchesRuntime(state, sessionID, runtime) {
			return state, "preserve_other_runtime_prompt"
		}
		if intent != runLoopIntentEnable && state.Enabled && runLoopSessionMatchesRuntime(state, sessionID, runtime) && isRunLoopSideChannelPrompt(payload) {
			return state, "preserve_side_channel_prompt"
		}
		if intent != runLoopIntentEnable && state.Enabled && runLoopSessionMatchesRuntime(state, sessionID, runtime) && internalPrompt {
			return state, "preserve_runtime_internal_prompt"
		}
		if intent != runLoopIntentEnable && internalPrompt {
			return state, "ignore_runtime_internal_prompt"
		}
		wasEnabled := state.Enabled
		state = runLoopState{
			SessionID: sessionID,
			Runtime:   runtime,
		}
		if intent == runLoopIntentEnable {
			state.Enabled = true
			state.Mode = runLoopModeSession
			state.ActiveRunID = sessionID
			state.DisabledReason = ""
			state.NoProgressNudges = 0
			state.AwaitingContinuation = false
			_ = clearRunLoopStopFile(repoRoot)
			branch = "enable_user_prompt"
		} else if wasEnabled {
			state.Enabled = false
			state.Mode = ""
			state.ActiveRunID = ""
			state.DisabledReason = "user_prompt"
			_ = writeRunLoopStopFileForRuntime(repoRoot, sessionID, sessionID, runtime, "user_prompt")
			branch = "disable_user_prompt"
		} else {
			state.DisabledReason = ""
			branch = "disabled_user_prompt"
		}
	case runLoopToolEvent:
		if !state.Enabled || runLoopStateApplies(state, sessionID, runtime) {
			state.AwaitingContinuation = false
			branch = "tool_event_clear_awaiting"
		} else {
			branch = "tool_event_preserve_other_runtime"
		}
	case runLoopStopEvent:
		interrupted := isUserStopInterrupt(payload)
		if interrupted && (!state.Enabled || runLoopStateApplies(state, sessionID, runtime)) {
			activeRunID := state.ActiveRunID
			if activeRunID == "" {
				activeRunID = sessionID
			}
			state.Enabled = false
			state.Mode = ""
			state.ActiveRunID = ""
			state.NoProgressNudges = 0
			state.DisabledReason = "user_interrupt"
			_ = writeRunLoopStopFileForRuntime(repoRoot, sessionID, activeRunID, runtime, "user_interrupt")
			branch = "disable_user_interrupt"
		} else {
			branch = "stop_event_preserve"
		}
	case runLoopSessionEnd:
		if state.Enabled && state.Mode == runLoopModeRepo {
			branch = "session_end_preserve_repo"
		} else if !state.Enabled || runLoopSessionMatchesRuntime(state, sessionID, runtime) {
			state.Enabled = false
			state.Mode = ""
			state.ActiveRunID = ""
			state.NoProgressNudges = 0
			state.DisabledReason = ""
			state.SessionID = sessionID
			state.Runtime = runtime
			branch = "session_end_disable"
		} else {
			branch = "session_end_preserve_other_runtime"
		}
	}

	if event != runLoopSessionStart && sessionID != "" && state.Mode != runLoopModeRepo {
		state.SessionID = sessionID
	}
	if !state.Enabled {
		state.Mode = ""
		state.ActiveRunID = ""
		if state.NoProgressNudges < 0 {
			state.NoProgressNudges = 0
		}
	}

	return state, branch
}

func isUserStopInterrupt(payload *HookPayload) bool {
	if payload == nil {
		return false
	}
	// Only the explicit is_interrupt flag is a definitive user abort.
	// Text patterns from Error/Raw produced false positives for internal
	// runtime events such as compaction or model switches.
	if payload.IsInterrupt != nil && *payload.IsInterrupt {
		return true
	}
	return false
}

func runLoopIntentFromUserPrompt(payload *HookPayload) runLoopIntent {
	text := extractRunLoopUserPromptText(payload)
	if text == "" {
		return runLoopIntentNoop
	}
	if !containsRunLoopActivationFlag(text) {
		return runLoopIntentNoop
	}
	return runLoopIntentEnable
}

func isRuntimeInternalUserPrompt(payload *HookPayload) bool {
	text := extractRunLoopRawPromptText(payload)
	if text == "" {
		return false
	}
	if strings.TrimSpace(hookPromptBlockPattern.ReplaceAllString(text, "")) == "" {
		return true
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if strings.HasPrefix(normalized, "reconc run is on.") {
		return true
	}
	if strings.HasPrefix(normalized, "runloop autocontinue. reconc run is on") {
		return true
	}
	if strings.HasPrefix(normalized, "runloop autocontinue. let me cook. reconc run is on") {
		return true
	}
	if strings.HasPrefix(normalized, "runloop autocontinue.") && strings.Contains(normalized, "continue the repository task lifecycle") {
		return true
	}
	internalPhrases := []string{
		normalizedRunLoopPromptFirstLine(legacyRunLoopContinuationPrompt),
		"briefly inform the user about the task result and perform any follow-up actions",
		"the above subagent result is already visible to the user",
		"otherwise end your response with a brief third-person confirmation that the subagent has completed",
	}
	for _, phrase := range internalPhrases {
		if strings.HasPrefix(normalized, phrase) {
			return true
		}
	}
	return false
}

func normalizedRunLoopPromptFirstLine(prompt string) string {
	line := strings.SplitN(prompt, "\n", 2)[0]
	return strings.ToLower(strings.Join(strings.Fields(line), " "))
}

func extractRunLoopRawPromptText(payload *HookPayload) string {
	if payload == nil {
		return ""
	}
	if strings.TrimSpace(payload.Prompt) != "" {
		return payload.Prompt
	}
	parts := []string{}
	for _, key := range []string{"prompt", "user_prompt", "userPrompt", "message", "text"} {
		if value, ok := payload.Raw[key].(string); ok && strings.TrimSpace(value) != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n")
}

func extractRunLoopUserPromptText(payload *HookPayload) string {
	if payload == nil {
		return ""
	}
	if payload.Prompt != "" {
		return sanitizeRunLoopIntentText(payload.Prompt)
	}
	parts := []string{}
	for _, key := range []string{"prompt", "user_prompt", "userPrompt", "message", "text"} {
		if value, ok := payload.Raw[key].(string); ok && strings.TrimSpace(value) != "" {
			parts = append(parts, value)
		}
	}
	return sanitizeRunLoopIntentText(strings.Join(parts, "\n"))
}

func sanitizeRunLoopIntentText(text string) string {
	text = hookPromptBlockPattern.ReplaceAllString(text, "\n")
	text = fencedCodeBlockPattern.ReplaceAllString(text, "\n")
	text = stripQuotedRunLoopIntentText(text)
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			out = append(out, "")
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			continue
		}
		if strings.HasPrefix(trimmed, `"`) || strings.HasPrefix(trimmed, "“") || strings.HasPrefix(trimmed, "”") || looksLikePastedTranscriptLine(trimmed) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func stripQuotedRunLoopIntentText(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	var quote rune
	for _, r := range text {
		if quote != 0 {
			if r == '\n' {
				out.WriteRune('\n')
				continue
			}
			if closesRunLoopQuote(r, quote) {
				quote = 0
			}
			out.WriteRune(' ')
			continue
		}
		if closeQuote, ok := opensRunLoopQuote(r); ok {
			quote = closeQuote
			out.WriteRune(' ')
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func opensRunLoopQuote(r rune) (rune, bool) {
	switch r {
	case '"':
		return '"', true
	case '“':
		return '”', true
	case '„':
		return '“', true
	case '«':
		return '»', true
	case '‹':
		return '›', true
	default:
		return 0, false
	}
}

func closesRunLoopQuote(r, quote rune) bool {
	return r == quote || (quote == '”' && r == '“')
}

func looksLikePastedTranscriptLine(trimmed string) bool {
	switch {
	case strings.HasPrefix(trimmed, "• "),
		strings.HasPrefix(trimmed, "› "),
		strings.HasPrefix(trimmed, "■ "),
		strings.HasPrefix(trimmed, "────────────────"):
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{
		"feedback:",
		"hook context:",
		"stop hook ",
		"posttooluse hook ",
		"pretooluse hook ",
		"running stop hook",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func containsRunLoopActivationFlag(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if looksLikeRunLoopActivationMentionLine(trimmed) {
			continue
		}
		for _, field := range strings.Fields(line) {
			token := strings.Trim(field, " \t\r\n.,;:!?()[]{}<>")
			if token == "/runloop" {
				return true
			}
		}
	}
	return false
}

func looksLikeRunLoopActivationMentionLine(trimmed string) bool {
	if !strings.Contains(trimmed, "/runloop") {
		return false
	}
	lower := strings.ToLower(strings.Join(strings.Fields(trimmed), " "))
	for _, phrase := range []string{
		"kein /runloop",
		"keine /runloop",
		"ohne /runloop",
		"no /runloop",
		"not /runloop",
		"without /runloop",
		"contains no /runloop",
		"enthält kein /runloop",
		"enthaelt kein /runloop",
		"runloop ist aus",
		"runloop is off",
		"append /runloop",
		"häng /runloop",
		"häng dort /runloop",
		"haeng /runloop",
		"haeng dort /runloop",
		"hänge /runloop",
		"hänge dort /runloop",
		"haenge /runloop",
		"haenge dort /runloop",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func isRunLoopSideChannelPrompt(payload *HookPayload) bool {
	text := strings.TrimSpace(extractRunLoopRawPromptText(payload))
	if text == "" {
		return false
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		return strings.HasPrefix(trimmed, "/btw") && (len(trimmed) == len("/btw") || isRunLoopTokenBoundary(rune(trimmed[len("/btw")])))
	}
	return false
}

func isRunLoopTokenBoundary(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n' || strings.ContainsRune(".,;:!?()[]{}<>", r)
}
