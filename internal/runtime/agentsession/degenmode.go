package agentsession

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const reconcHookRuntimeEnv = "RECONC_HOOK_RUNTIME"

// degenModeState mirrors the file contract consumed by the OpenCode
// runtime degenmode plugin. The CLI/runtime only consumes the core
// fields needed to coordinate continue/stop behavior.
//
// The shape intentionally stays tiny so older plugin versions or
// hand-edited state files can still be parsed without strict
// coupling.
type degenModeState struct {
	Enabled              bool   `json:"enabled"`
	SessionID            string `json:"session_id"`
	ActiveRunID          string `json:"active_run_id"`
	Runtime              string `json:"runtime,omitempty"`
	NoProgressNudges     int    `json:"no_progress_nudges"`
	DisabledReason       string `json:"disabled_reason"`
	StopAnchorMessageID  string `json:"stop_anchor_message_id"`
	LastHead             string `json:"last_head,omitempty"`
	LastCurrent          string `json:"last_current,omitempty"`
	AwaitingContinuation bool   `json:"awaiting_continuation,omitempty"`
	LastPromptSignature  string `json:"last_prompt_signature,omitempty"`
}

// DegenModeDecision is one append-only record in
// .reconc/degenmode/decisions.jsonl: every degenmode state transition with the
// exact branch taken, so `reconc degenmode log` can show why the runtime did
// what it did without grepping raw JSONL.
type DegenModeDecision struct {
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

type degenModeStopMarker struct {
	SessionID   string `json:"session_id,omitempty"`
	ActiveRunID string `json:"active_run_id,omitempty"`
	Runtime     string `json:"runtime,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type degenModeEvent int

const (
	degenModeSessionStart degenModeEvent = iota
	degenModeUserPrompt
	degenModeToolEvent
	degenModeStopEvent
	degenModeSessionEnd
)

type degenModeIntent int

const (
	degenModeIntentNoop degenModeIntent = iota
	degenModeIntentEnable
)

var (
	hookPromptBlockPattern = regexp.MustCompile(`(?is)<hook_prompt\b[^>]*>.*?</hook_prompt>`)
	fencedCodeBlockPattern = regexp.MustCompile("(?s)```.*?```")
)

func degenModeStatePath(repoRoot string) (string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".reconc", "degenmode", "state.json"), nil
}

func degenModeStopPath(repoRoot string) (string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".reconc", "degenmode", "stop"), nil
}

func degenModeDecisionLogPath(repoRoot string) (string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".reconc", "degenmode", "decisions.jsonl"), nil
}

func appendDegenModeDecision(repoRoot string, decision DegenModeDecision) error {
	path, err := degenModeDecisionLogPath(repoRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	decision.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	body, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(body)
	return err
}

func degenModeEventName(event degenModeEvent) string {
	switch event {
	case degenModeSessionStart:
		return "session_start"
	case degenModeUserPrompt:
		return "user_prompt"
	case degenModeToolEvent:
		return "tool_event"
	case degenModeStopEvent:
		return "stop"
	case degenModeSessionEnd:
		return "session_end"
	default:
		return "unknown"
	}
}

func degenModeIntentName(intent degenModeIntent) string {
	switch intent {
	case degenModeIntentEnable:
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

func clearDegenModeStopFile(repoRoot string) error {
	path, err := degenModeStopPath(repoRoot)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeDegenModeStopFile(repoRoot, sessionID, activeRunID, reason string) error {
	return writeDegenModeStopFileForRuntime(repoRoot, sessionID, activeRunID, "", reason)
}

func writeDegenModeStopFileForRuntime(repoRoot, sessionID, activeRunID, runtime, reason string) error {
	path, err := degenModeStopPath(repoRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	marker := degenModeStopMarker{
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
	return os.WriteFile(path, body, 0o644)
}

func hasDegenModeStopFile(repoRoot string) bool {
	path, err := degenModeStopPath(repoRoot)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func readDegenModeStopMarker(repoRoot string) (degenModeStopMarker, bool, bool) {
	path, err := degenModeStopPath(repoRoot)
	if err != nil {
		return degenModeStopMarker{}, false, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return degenModeStopMarker{}, false, false
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return degenModeStopMarker{}, true, true
	}
	var marker degenModeStopMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return degenModeStopMarker{}, true, true
	}
	marker.SessionID = strings.TrimSpace(marker.SessionID)
	marker.ActiveRunID = strings.TrimSpace(marker.ActiveRunID)
	marker.Runtime = strings.TrimSpace(marker.Runtime)
	marker.Reason = strings.TrimSpace(marker.Reason)
	return marker, true, false
}

func degenModeStopFileAppliesToState(repoRoot string, state degenModeState) bool {
	marker, exists, legacy := readDegenModeStopMarker(repoRoot)
	if !exists {
		return false
	}
	if legacy {
		return true
	}
	return degenModeMarkerMatchesState(marker, state)
}

func degenModeMarkerMatchesState(marker degenModeStopMarker, state degenModeState) bool {
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

func degenModeSessionMatchesRuntime(state degenModeState, sessionID string, runtime string) bool {
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

func loadDegenModeState(repoRoot string) (degenModeState, error) {
	path, err := degenModeStatePath(repoRoot)
	if err != nil {
		return degenModeState{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return degenModeState{}, nil
		}
		return degenModeState{}, err
	}
	var state degenModeState
	if err := json.Unmarshal(data, &state); err != nil {
		return degenModeState{}, err
	}
	state.SessionID = strings.TrimSpace(state.SessionID)
	state.ActiveRunID = strings.TrimSpace(state.ActiveRunID)
	state.Runtime = strings.TrimSpace(state.Runtime)
	if state.NoProgressNudges < 0 {
		state.NoProgressNudges = 0
	}
	if state.Enabled && degenModeStopFileAppliesToState(repoRoot, state) {
		state.Enabled = false
		state.ActiveRunID = ""
		state.NoProgressNudges = 0
		state.DisabledReason = "stop_file"
		state.AwaitingContinuation = false
	}
	return state, nil
}

func degenModeStateLockPath(repoRoot string) (string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".reconc", "degenmode", "state.lock"), nil
}

// withDegenModeLock serializes degenmode state access with an exclusive file
// lock so concurrent `reconc hook runtime` processes (parallel agent tool
// events) cannot race a load-modify-write on state.json. Mirrors the
// per-session locking that SessionState already relies on.
func withDegenModeLock(repoRoot string, fn func() error) error {
	lockPath, err := degenModeStateLockPath(repoRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("mkdir degenmode lock dir: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open degenmode lock: %w", err)
	}
	defer file.Close()
	unlock, err := lockSessionFile(file)
	if err != nil {
		return fmt.Errorf("lock degenmode state: %w", err)
	}
	defer func() { _ = unlock() }()
	return fn()
}

// writeDegenModeStateAtomic writes state via tmp-file-then-rename so a
// concurrent reader never observes a truncated/half-written file (a bare
// os.WriteFile truncates first, and a reader hitting that window decodes an
// empty file into a disabled zero-value state). Callers doing
// read-modify-write MUST hold withDegenModeLock; this primitive only
// guarantees each individual write is atomic, not that two writes serialize.
func writeDegenModeStateAtomic(repoRoot string, state degenModeState) error {
	path, err := degenModeStatePath(repoRoot)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	state.SessionID = strings.TrimSpace(state.SessionID)
	state.ActiveRunID = strings.TrimSpace(state.ActiveRunID)
	state.Runtime = strings.TrimSpace(state.Runtime)
	if state.NoProgressNudges < 0 {
		state.NoProgressNudges = 0
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal degenmode state: %w", err)
	}
	body = append(body, '\n')
	tmpFile, err := os.CreateTemp(dir, "state.json.*.tmp")
	if err != nil {
		return fmt.Errorf("create degenmode state tmp: %w", err)
	}
	tmp := tmpFile.Name()
	if _, err := tmpFile.Write(body); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write degenmode state tmp: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close degenmode state tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename degenmode state: %w", err)
	}
	return nil
}

// saveDegenModeState atomically persists state while holding the degenmode
// state lock. Use this for standalone/terminal writes; use mutateDegenModeState
// for read-modify-write so the load and save share a single lock.
func saveDegenModeState(repoRoot string, state degenModeState) error {
	return withDegenModeLock(repoRoot, func() error {
		return writeDegenModeStateAtomic(repoRoot, state)
	})
}

// mutateDegenModeState serializes load -> mutate -> save under one lock so
// concurrent agent tool events cannot lose each other's updates. Mirror of
// MutateSessionState. A transient load error yields an empty (disabled) state
// to the mutator, matching the previous recovery behavior; with atomic writes
// in place that can now only happen on genuine corruption, never a torn read.
func mutateDegenModeState(repoRoot string, fn func(degenModeState) degenModeState) (degenModeState, degenModeState, error) {
	var before, after degenModeState
	err := withDegenModeLock(repoRoot, func() error {
		state, lerr := loadDegenModeState(repoRoot)
		if lerr != nil {
			state = degenModeState{}
		}
		before = state
		after = fn(state)
		return writeDegenModeStateAtomic(repoRoot, after)
	})
	return before, after, err
}

func reconcileDegenModeState(repoRoot, sessionID string, payload *HookPayload, event degenModeEvent) error {
	return reconcileDegenModeStateForRuntime(repoRoot, sessionID, "", payload, event)
}

func reconcileDegenModeStateForRuntime(repoRoot, sessionID, runtime string, payload *HookPayload, event degenModeEvent) error {
	sessionID = strings.TrimSpace(sessionID)
	runtime = strings.TrimSpace(runtime)
	intent := degenModeIntentFromUserPrompt(payload)
	internalPrompt := event == degenModeUserPrompt && isRuntimeInternalUserPrompt(payload)

	var branch string
	var stopApplies bool
	before, after, err := mutateDegenModeState(repoRoot, func(state degenModeState) degenModeState {
		stopApplies = degenModeStopFileAppliesToState(repoRoot, state)
		next, b := computeDegenModeNextState(repoRoot, state, sessionID, runtime, payload, event, intent, internalPrompt, stopApplies)
		branch = b
		return next
	})
	if err != nil {
		return err
	}
	_ = appendDegenModeDecision(repoRoot, DegenModeDecision{
		Event:                      degenModeEventName(event),
		Branch:                     branch,
		Runtime:                    runtime,
		SessionID:                  sessionIDFromPayload(payload),
		StateSessionID:             after.SessionID,
		ActiveRunID:                after.ActiveRunID,
		Intent:                     degenModeIntentName(intent),
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
	})
	return nil
}

// computeDegenModeNextState is the pure state-transition core of the reconcile.
// It runs under withDegenModeLock (via mutateDegenModeState) and returns the
// next state plus the branch name for the decision log. Stop-file writes/clears
// are intentional side effects of specific transitions.
func computeDegenModeNextState(repoRoot string, state degenModeState, sessionID, runtime string, payload *HookPayload, event degenModeEvent, intent degenModeIntent, internalPrompt bool, stopApplies bool) (degenModeState, string) {
	if stopApplies && !(event == degenModeUserPrompt && intent == degenModeIntentEnable) {
		state.Enabled = false
		state.ActiveRunID = ""
		state.NoProgressNudges = 0
		state.DisabledReason = "stop_file"
		state.AwaitingContinuation = false
	}
	branch := "preserve"

	switch event {
	case degenModeSessionStart:
		if stopApplies {
			state = degenModeState{SessionID: sessionID, Runtime: runtime, DisabledReason: "stop_file"}
			branch = "session_start_stop_file"
		} else if !state.Enabled && sessionID != "" {
			state.SessionID = sessionID
			state.Runtime = runtime
			branch = "session_start_disabled"
		} else {
			branch = "session_start_preserve_active"
		}
	case degenModeUserPrompt:
		if intent != degenModeIntentEnable && state.Enabled && !degenModeSessionMatchesRuntime(state, sessionID, runtime) {
			return state, "preserve_other_runtime_prompt"
		}
		if intent != degenModeIntentEnable && state.Enabled && degenModeSessionMatchesRuntime(state, sessionID, runtime) && isDegenModeSideChannelPrompt(payload) {
			return state, "preserve_side_channel_prompt"
		}
		if intent != degenModeIntentEnable && state.Enabled && degenModeSessionMatchesRuntime(state, sessionID, runtime) && internalPrompt {
			return state, "preserve_runtime_internal_prompt"
		}
		if intent != degenModeIntentEnable && internalPrompt {
			return state, "ignore_runtime_internal_prompt"
		}
		state = degenModeState{
			SessionID: sessionID,
			Runtime:   runtime,
		}
		if intent == degenModeIntentEnable {
			state.Enabled = true
			state.ActiveRunID = sessionID
			state.DisabledReason = ""
			state.NoProgressNudges = 0
			state.AwaitingContinuation = false
			_ = clearDegenModeStopFile(repoRoot)
			branch = "enable_user_prompt"
		} else {
			state.Enabled = false
			state.ActiveRunID = ""
			state.DisabledReason = "user_prompt"
			_ = writeDegenModeStopFileForRuntime(repoRoot, sessionID, sessionID, runtime, "user_prompt")
			branch = "disable_user_prompt"
		}
	case degenModeToolEvent:
		if !state.Enabled || degenModeSessionMatchesRuntime(state, sessionID, runtime) {
			state.AwaitingContinuation = false
			branch = "tool_event_clear_awaiting"
		} else {
			branch = "tool_event_preserve_other_runtime"
		}
	case degenModeStopEvent:
		interrupted := isUserStopInterrupt(payload)
		if interrupted && (!state.Enabled || degenModeSessionMatchesRuntime(state, sessionID, runtime)) {
			activeRunID := state.ActiveRunID
			if activeRunID == "" {
				activeRunID = sessionID
			}
			state.Enabled = false
			state.ActiveRunID = ""
			state.NoProgressNudges = 0
			state.DisabledReason = "user_interrupt"
			_ = writeDegenModeStopFileForRuntime(repoRoot, sessionID, activeRunID, runtime, "user_interrupt")
			branch = "disable_user_interrupt"
		} else {
			branch = "stop_event_preserve"
		}
	case degenModeSessionEnd:
		if !state.Enabled || degenModeSessionMatchesRuntime(state, sessionID, runtime) {
			state.Enabled = false
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

	if event != degenModeSessionStart && sessionID != "" {
		state.SessionID = sessionID
	}
	if !state.Enabled {
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

func degenModeIntentFromUserPrompt(payload *HookPayload) degenModeIntent {
	text := extractDegenModeUserPromptText(payload)
	if text == "" {
		return degenModeIntentNoop
	}
	if !containsDegenModeActivationFlag(text) {
		return degenModeIntentNoop
	}
	return degenModeIntentEnable
}

func isRuntimeInternalUserPrompt(payload *HookPayload) bool {
	text := extractDegenModeRawPromptText(payload)
	if text == "" {
		return false
	}
	if strings.TrimSpace(hookPromptBlockPattern.ReplaceAllString(text, "")) == "" {
		return true
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if strings.HasPrefix(normalized, "degenmode autocontinue.") && strings.Contains(normalized, "continue the repository task lifecycle") {
		return true
	}
	internalPhrases := []string{
		"degenmode autocontinue. continue the repository task lifecycle",
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

func extractDegenModeRawPromptText(payload *HookPayload) string {
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

func extractDegenModeUserPromptText(payload *HookPayload) string {
	if payload == nil {
		return ""
	}
	if payload.Prompt != "" {
		return sanitizeDegenModeIntentText(payload.Prompt)
	}
	parts := []string{}
	for _, key := range []string{"prompt", "user_prompt", "userPrompt", "message", "text"} {
		if value, ok := payload.Raw[key].(string); ok && strings.TrimSpace(value) != "" {
			parts = append(parts, value)
		}
	}
	return sanitizeDegenModeIntentText(strings.Join(parts, "\n"))
}

func sanitizeDegenModeIntentText(text string) string {
	text = hookPromptBlockPattern.ReplaceAllString(text, "\n")
	text = fencedCodeBlockPattern.ReplaceAllString(text, "\n")
	text = stripQuotedDegenModeIntentText(text)
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

func stripQuotedDegenModeIntentText(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	var quote rune
	for _, r := range text {
		if quote != 0 {
			if r == '\n' {
				out.WriteRune('\n')
				continue
			}
			if closesDegenModeQuote(r, quote) {
				quote = 0
			}
			out.WriteRune(' ')
			continue
		}
		if closeQuote, ok := opensDegenModeQuote(r); ok {
			quote = closeQuote
			out.WriteRune(' ')
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func opensDegenModeQuote(r rune) (rune, bool) {
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

func closesDegenModeQuote(r, quote rune) bool {
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

func containsDegenModeActivationFlag(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if looksLikeDegenModeActivationMentionLine(trimmed) {
			continue
		}
		for _, field := range strings.Fields(line) {
			token := strings.Trim(field, " \t\r\n.,;:!?()[]{}<>")
			if token == "/degenmode" {
				return true
			}
		}
	}
	return false
}

func looksLikeDegenModeActivationMentionLine(trimmed string) bool {
	if !strings.Contains(trimmed, "/degenmode") {
		return false
	}
	lower := strings.ToLower(strings.Join(strings.Fields(trimmed), " "))
	for _, phrase := range []string{
		"kein /degenmode",
		"keine /degenmode",
		"ohne /degenmode",
		"no /degenmode",
		"not /degenmode",
		"without /degenmode",
		"contains no /degenmode",
		"enthält kein /degenmode",
		"enthaelt kein /degenmode",
		"degenmode ist aus",
		"degenmode is off",
		"append /degenmode",
		"häng /degenmode",
		"häng dort /degenmode",
		"haeng /degenmode",
		"haeng dort /degenmode",
		"hänge /degenmode",
		"hänge dort /degenmode",
		"haenge /degenmode",
		"haenge dort /degenmode",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func isDegenModeSideChannelPrompt(payload *HookPayload) bool {
	text := strings.TrimSpace(extractDegenModeRawPromptText(payload))
	if text == "" {
		return false
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		return strings.HasPrefix(trimmed, "/btw") && (len(trimmed) == len("/btw") || isDegenModeTokenBoundary(rune(trimmed[len("/btw")])))
	}
	return false
}

func isDegenModeTokenBoundary(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n' || strings.ContainsRune(".,;:!?()[]{}<>", r)
}

// buildDegenModeContinuationPrompt reads the current TASK state and
// constructs the full degenmode auto-continuation prompt used when
// the Stop hook blocks for degenmode. Returns empty string if the
// repo has no current open TASK (degenmode can't continue without one).
func buildDegenModeContinuationPrompt(repoRoot string) string {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return ""
	}
	task, subTask := readCurrentTaskState(root)
	if task == "" {
		return ""
	}
	prompt := `degenmode autocontinue. Continue the repository task lifecycle without asking for routine permission. No ceremony, no confirmation questions - just work.

Active: TASK = ` + task + ` | Sub-Task = ` + subTask + `. Read the live Current: pointer in docs/tasks.md yourself.

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
	return prompt
}

// readCurrentTaskState returns the name of the current TASK from
// docs/tasks.md and the active sub-task from the detail file.
func readCurrentTaskState(repoRoot string) (string, string) {
	tasksPath := filepath.Join(repoRoot, "docs", "tasks.md")
	tasksBytes, err := os.ReadFile(tasksPath)
	if err != nil {
		return "", ""
	}
	re := regexp.MustCompile(`(?m)^Current:\s+(TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*)\s+->\s+(tasks/TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*\.md)$`)
	match := re.FindStringSubmatch(string(tasksBytes))
	if match == nil {
		return "", ""
	}
	taskName := match[1]
	detailPath := filepath.Join(repoRoot, "docs", filepath.FromSlash(match[2]))
	detailBytes, err := os.ReadFile(detailPath)
	if err != nil {
		return taskName, "unknown"
	}
	subTask := parseActiveSubTaskLine(string(detailBytes))
	if subTask == "" {
		subTask = parseFirstOpenSubTaskLine(string(detailBytes))
	}
	if subTask == "" {
		subTask = "continue"
	}
	return taskName, subTask
}

func readCurrentHead(repoRoot string) string {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func readDegenProgressFingerprint(repoRoot string) string {
	task, subTask := readCurrentTaskState(repoRoot)
	return task + "\n" + subTask
}

func parseActiveSubTaskLine(content string) string {
	return parseSubTaskLine(content, "- [~] ")
}

func parseFirstOpenSubTaskLine(content string) string {
	return parseSubTaskLine(content, "- [ ] ")
}

func parseSubTaskLine(content string, prefix string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		}
	}
	return ""
}
