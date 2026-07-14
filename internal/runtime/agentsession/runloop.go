package agentsession

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/jsonl"
)

const reconcHookRuntimeEnv = "RECONC_HOOK_RUNTIME"

type runLoopMode string

const runLoopModeRepo runLoopMode = "repo"

// runLoopState is the repository-wide autonomous run state. Mode is retained
// only to reject legacy session-scoped state safely; every new enabled state
// is repository mode.
type runLoopState struct {
	Enabled              bool        `json:"enabled"`
	Mode                 runLoopMode `json:"mode,omitempty"`
	NoProgressNudges     int         `json:"no_progress_nudges"`
	DisabledReason       string      `json:"disabled_reason"`
	LastCurrent          string      `json:"last_current,omitempty"`
	AwaitingContinuation bool        `json:"awaiting_continuation,omitempty"`
	EnabledAt            string      `json:"enabled_at,omitempty"`
	LastPolicyCheckpoint string      `json:"last_policy_checkpoint,omitempty"`
	CheckpointMaterial   uint64      `json:"checkpoint_material_events,omitempty"`
}

// RunLoopDecision is one append-only record in
// .reconc/runloop/decisions.jsonl: every run-control state transition with the
// exact branch taken, so `reconc run log` can show why the runtime did
// what it did without grepping raw JSONL.
type RunLoopDecision struct {
	Timestamp                  string `json:"ts"`
	Event                      string `json:"event"`
	Branch                     string `json:"branch"`
	Runtime                    string `json:"runtime,omitempty"`
	SessionID                  string `json:"session_id,omitempty"`
	EnabledBefore              bool   `json:"enabled_before"`
	EnabledAfter               bool   `json:"enabled_after"`
	DisabledReasonBefore       string `json:"disabled_reason_before,omitempty"`
	DisabledReasonAfter        string `json:"disabled_reason_after,omitempty"`
	AwaitingContinuationBefore bool   `json:"awaiting_continuation_before,omitempty"`
	AwaitingContinuationAfter  bool   `json:"awaiting_continuation_after,omitempty"`
	StopHookActive             bool   `json:"stop_hook_active,omitempty"`
	PolicyBlocked              bool   `json:"policy_blocked,omitempty"`
	ViolationCount             int    `json:"violation_count,omitempty"`
}

const (
	runLoopDecisionMaxBytes    = 2 * 1024 * 1024
	runLoopDecisionMaxArchives = 2
)

func runLoopStatePath(repoRoot string) (string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".reconc", "runloop", "state.json"), nil
}

func legacyRunLoopStopPath(repoRoot string) (string, error) {
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
	decision.DisabledReasonBefore = truncateBytes(strings.TrimSpace(decision.DisabledReasonBefore), 4096)
	decision.DisabledReasonAfter = truncateBytes(strings.TrimSpace(decision.DisabledReasonAfter), 4096)
	return decision
}

func sessionIDFromPayload(payload *HookPayload) string {
	if payload == nil {
		return ""
	}
	return strings.TrimSpace(payload.SessionID)
}

func clearRunLoopStopFile(repoRoot string) error {
	path, err := legacyRunLoopStopPath(repoRoot)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func runLoopStateApplies(state runLoopState) bool {
	return state.Enabled && state.Mode == runLoopModeRepo
}

func loadRunLoopState(repoRoot string) (runLoopState, error) {
	state, err := readRunLoopStateStored(repoRoot)
	if err != nil {
		return runLoopState{}, err
	}
	if state.Enabled && state.Mode != runLoopModeRepo {
		return runLoopState{DisabledReason: "legacy_session_mode_removed"}, nil
	}
	return state, nil
}

// readRunLoopStateStored returns the exact persisted state used by locked
// read-modify-write transitions.
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
	state.DisabledReason = strings.TrimSpace(state.DisabledReason)
	state.LastCurrent = strings.TrimSpace(state.LastCurrent)
	if state.NoProgressNudges < 0 {
		state.NoProgressNudges = 0
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

// withRunLoopLock serializes repository run state access with an exclusive file
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

// writeRunLoopStateAtomic writes repository run state via tmp-file-then-rename so a
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
	state.DisabledReason = strings.TrimSpace(state.DisabledReason)
	state.LastCurrent = strings.TrimSpace(state.LastCurrent)
	if state.NoProgressNudges < 0 {
		state.NoProgressNudges = 0
	}
	if state.Enabled {
		state.Mode = runLoopModeRepo
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

// saveRunLoopState atomically persists state while holding the repository run
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
