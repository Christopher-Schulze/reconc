package agentsession

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/jsonl"
)

const reconcHookRuntimeEnv = "RECONC_HOOK_RUNTIME"

const (
	repositoryRunDir       = ".reconc/run"
	runDecisionMaxBytes    = 2 * 1024 * 1024
	runDecisionMaxArchives = 2
)

// repositoryRunState is the only autonomous run state. There is no session
// mode and no compatibility discriminator.
type repositoryRunState struct {
	Enabled              bool
	NoProgressNudges     int
	DisabledReason       repositoryRunDisabledReason
	LastProgressHash     [32]byte
	AwaitingContinuation bool
	EnabledAt            int64
	LastPolicyCheckpoint int64
	CheckpointMaterial   uint64
}

type repositoryRunDisabledReason uint8

const (
	repositoryRunDisabledNone repositoryRunDisabledReason = iota
	repositoryRunDisabledCommandOff
	repositoryRunDisabledBlockedTask
	repositoryRunDisabledTaskComplete
	repositoryRunDisabledTaskPlaneAbsent
	repositoryRunDisabledNoExecutableTask
)

func (reason repositoryRunDisabledReason) String() string {
	switch reason {
	case repositoryRunDisabledNone:
		return ""
	case repositoryRunDisabledCommandOff:
		return "command_off"
	case repositoryRunDisabledBlockedTask:
		return "blocked_task"
	case repositoryRunDisabledTaskComplete:
		return "task_complete"
	case repositoryRunDisabledTaskPlaneAbsent:
		return "task_plane_absent"
	case repositoryRunDisabledNoExecutableTask:
		return "no_executable_task"
	default:
		return "invalid"
	}
}

func (reason repositoryRunDisabledReason) valid() bool {
	return reason <= repositoryRunDisabledNoExecutableTask
}

// RunDecision is one append-only record in .reconc/run/decisions.jsonl. It
// records material run-control transitions with the
// exact branch taken, so `reconc run log` can show why the runtime did
// what it did without grepping raw JSONL.
type RunDecision struct {
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

func repositoryRunStatePath(repoRoot string) (string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return "", err
	}
	return repositoryRunStatePathResolved(root), nil
}

func repositoryRunStatePathResolved(root string) string {
	return filepath.Join(root, filepath.FromSlash(repositoryRunDir), "state.bin")
}

func runDecisionLogPath(repoRoot string) (string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return "", err
	}
	return runDecisionLogPathResolved(root), nil
}

func appendRunDecision(repoRoot string, decision RunDecision) error {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	return appendRunDecisionResolved(root, decision)
}

func runDecisionLogPathResolved(root string) string {
	return filepath.Join(root, filepath.FromSlash(repositoryRunDir), "decisions.jsonl")
}

func appendRunDecisionResolved(root string, decision RunDecision) error {
	decision.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	decision = boundedRunDecision(decision)
	body, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	return jsonl.Append(runDecisionLogPathResolved(root), body, jsonl.Policy{MaxBytes: runDecisionMaxBytes, MaxArchives: runDecisionMaxArchives})
}

func boundedRunDecision(decision RunDecision) RunDecision {
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

func repositoryRunEnabled(state repositoryRunState) bool {
	return state.Enabled
}

func loadRepositoryRunState(repoRoot string) (repositoryRunState, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return repositoryRunState{}, err
	}
	return loadRepositoryRunStateResolved(root)
}

func loadRepositoryRunStateResolved(root string) (repositoryRunState, error) {
	return readRepositoryRunStateResolved(root)
}

// openRepositoryRunStateResolved keeps the existing state descriptor open for
// one hook invocation. The caller may later lock and re-read the same file for
// a cross-process-safe mutation without paying for a second open/close pair.
// Missing state remains a read-only disabled result and creates no file.
func openRepositoryRunStateResolved(root string) (*os.File, repositoryRunSnapshot, error) {
	path := repositoryRunStatePathResolved(root)
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return nil, repositoryRunSnapshot{Slot: -1}, nil
	}
	if err != nil {
		return nil, repositoryRunSnapshot{}, fmt.Errorf("read repository run state: %w", err)
	}
	snapshot, err := readRepositoryRunSnapshotFile(file)
	if err != nil {
		_ = file.Close()
		return nil, repositoryRunSnapshot{}, err
	}
	return file, snapshot, nil
}

// readRepositoryRunStateResolved returns the exact persisted state used by
// locked read-modify-write transitions. root must already be canonical.
func readRepositoryRunStateResolved(root string) (repositoryRunState, error) {
	snapshot, err := readRepositoryRunSnapshot(repositoryRunStatePathResolved(root))
	return snapshot.State, err
}

// withRepositoryRunFileResolved locks state.bin itself so one descriptor owns
// lock, read, and write. CRC-protected alternating slots keep unlocked readers
// safe while eliminating a separate lock-file open from every Stop.
func withRepositoryRunFileResolved(root string, fn func(*os.File) error) error {
	path := repositoryRunStatePathResolved(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir repository run state dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open repository run state: %w", err)
	}
	defer file.Close()
	unlock, err := filelock.Lock(file)
	if err != nil {
		return fmt.Errorf("lock repository run state: %w", err)
	}
	defer func() { _ = unlock() }()
	return fn(file)
}

// saveRepositoryRunState persists state while holding the repository run state
// lock. Use this for standalone/terminal writes; use
// mutateRepositoryRunStateResolved
// for read-modify-write so the load and save share a single lock.
func saveRepositoryRunState(repoRoot string, state repositoryRunState) error {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	return withRepositoryRunFileResolved(root, func(file *os.File) error {
		snapshot, readErr := readRepositoryRunSnapshotFile(file)
		if readErr != nil {
			return readErr
		}
		return writeRepositoryRunSnapshotFile(file, state, snapshot)
	})
}

// mutateRepositoryRunStateResolved serializes load -> mutate -> save under one lock so
// concurrent agent tool events cannot lose each other's updates. Mirror of
// MutateSessionState. Corrupt or unreadable state fails closed and is never
// replaced with a zero value; CRC-protected alternating slots recover from a
// torn newest write without accepting corrupt state.
func mutateRepositoryRunState(repoRoot string, fn func(repositoryRunState) repositoryRunState) (repositoryRunState, repositoryRunState, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return repositoryRunState{}, repositoryRunState{}, err
	}
	return mutateRepositoryRunStateResolved(root, fn)
}

func mutateRepositoryRunStateResolved(root string, fn func(repositoryRunState) repositoryRunState) (repositoryRunState, repositoryRunState, error) {
	var before, after repositoryRunState
	err := withRepositoryRunFileResolved(root, func(file *os.File) error {
		var mutateErr error
		before, after, mutateErr = mutateRepositoryRunStateLockedFile(file, fn)
		return mutateErr
	})
	return before, after, err
}

func mutateRepositoryRunStateOpenFile(file *os.File, fn func(repositoryRunState) repositoryRunState) (repositoryRunState, repositoryRunState, error) {
	if file == nil {
		return repositoryRunState{}, repositoryRunState{}, fmt.Errorf("repository run state file is unavailable")
	}
	unlock, err := filelock.Lock(file)
	if err != nil {
		return repositoryRunState{}, repositoryRunState{}, fmt.Errorf("lock repository run state: %w", err)
	}
	defer func() { _ = unlock() }()
	return mutateRepositoryRunStateLockedFile(file, fn)
}

func mutateRepositoryRunStateLockedFile(file *os.File, fn func(repositoryRunState) repositoryRunState) (repositoryRunState, repositoryRunState, error) {
	snapshot, err := readRepositoryRunSnapshotFile(file)
	if err != nil {
		return repositoryRunState{}, repositoryRunState{}, err
	}
	before := snapshot.State
	after := fn(before)
	if before == after {
		return before, after, nil
	}
	if err := writeRepositoryRunSnapshotFile(file, after, snapshot); err != nil {
		return before, after, err
	}
	return before, after, nil
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
