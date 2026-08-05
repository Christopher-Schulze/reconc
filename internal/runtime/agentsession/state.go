// Package agentsession is the stateful hook-runtime core used by every
// registered agent adapter. It uses runtime.CheckRepoPolicy as the policy
// backend.
//
// Session state is a small JSON file per session kept under
//
//	$RECONC_HOME/sessions/claude/projects/<hash16(repo)>/sessions/<id>.json
//
// The location is outside the repo so multiple concurrent checkouts
// of the same remote don't clobber each other's state, and so a
// destructive `rm -rf .reconc/` doesn't wipe live-session evidence
// mid-flight.
//
// Security guarantees come from the threat-model documented in
// docs/architecture.md#threat-model-hook-runtime. Key invariants:
//
//   - session_id mismatch across events -> rejected.
//   - repo_root stored in state is canonicalised to the operating-system
//     filesystem identity, so aliases such as macOS /var vs /private/var and
//     Windows 8.3 paths do not reject legitimate events.
//   - state is rewritten atomically via a tmp-then-rename dance so a
//     crash mid-write doesn't produce a half-parsed file.
//   - session mutations are serialized with a per-session file lock and
//     unique temp files so parallel agent tool events cannot race.
package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/retention"
)

// StateRootEnv lets operators pin the session-state directory to a
// specific location. Useful for sandboxes and CI isolation. When
// unset we default to $RECONC_HOME/sessions/claude/.
const StateRootEnv = "RECONC_CLAUDE_STATE_DIR"

// CommandResult mirrors the evaluator's runtime.CommandResult but we
// keep our own copy in the session state so we can replay / inspect
// them without importing runtime types back into the state file.
type CommandResult struct {
	Command       string `json:"command"`
	Outcome       string `json:"outcome"` // "success" | "failure"
	EvidenceEpoch uint64 `json:"evidence_epoch,omitempty"`
	ToolUseID     string `json:"tool_use_id,omitempty"`
	ExitCode      *int   `json:"exit_code,omitempty"`
	Error         string `json:"error,omitempty"`
	IsInterrupt   *bool  `json:"is_interrupt,omitempty"`
}

type PendingToolCall struct {
	ToolName  string                 `json:"tool_name"`
	ToolInput map[string]interface{} `json:"tool_input"`
	ToolUseID string                 `json:"tool_use_id,omitempty"`
}

// SessionState is the on-disk shape of one agent session's accumulated
// evidence. Every field is JSON-tagged so adding a new one is strictly
// additive for back-compat.
type SessionState struct {
	RepoRoot       string            `json:"repo_root"`
	SessionID      string            `json:"session_id"`
	Runtime        string            `json:"runtime,omitempty"`
	ReadPaths      []string          `json:"read_paths"`
	WritePaths     []string          `json:"write_paths"`
	WriteEpochs    map[string]uint64 `json:"write_epochs,omitempty"`
	EvidenceEpoch  uint64            `json:"evidence_epoch,omitempty"`
	Commands       []string          `json:"commands"`
	Claims         []string          `json:"claims"`
	CommandResults []CommandResult   `json:"command_results"`
	// CommandResultBytes caches the JSON-encoded byte total of
	// CommandResults so the append budget check is O(1) instead of
	// re-marshaling every stored result. Legacy states load with it zero
	// and backfill through the normalize rebuild.
	CommandResultBytes         int64                      `json:"command_result_bytes,omitempty"`
	ReportPath                 string                     `json:"report_path"`
	StopPolicyFingerprint      string                     `json:"stop_policy_fingerprint,omitempty"`
	StopPolicyEvidenceHash     string                     `json:"stop_policy_evidence_hash,omitempty"`
	StopPolicyReportHash       string                     `json:"stop_policy_report_hash,omitempty"`
	LastStopBlockViolationHash string                     `json:"last_stop_block_violation_hash,omitempty"`
	PendingToolCalls           map[string]PendingToolCall `json:"pending_tool_calls,omitempty"`
	MaterialEvents             uint64                     `json:"material_events,omitempty"`
	LastMaterialSignature      string                     `json:"last_material_signature,omitempty"`
	GrokSteerAttempts          uint64                     `json:"grok_steer_attempts,omitempty"`
	GrokSteerContinuationKey   string                     `json:"grok_steer_continuation_key,omitempty"`
	GrokSteerMaterialEvents    uint64                     `json:"grok_steer_material_events,omitempty"`
	RepositoryRunEnabledAt     int64                      `json:"repository_run_enabled_at,omitempty"`
	RepositoryRunProgressHash  string                     `json:"repository_run_progress_hash,omitempty"`
	RepositoryRunNudges        int                        `json:"repository_run_nudges,omitempty"`
	RepositoryRunAwaiting      bool                       `json:"repository_run_awaiting,omitempty"`
	EvidenceSegmentCount       uint64                     `json:"evidence_segment_count,omitempty"`
	EvidenceSegmentDigest      string                     `json:"evidence_segment_digest,omitempty"`
	EvidenceOverflow           bool                       `json:"evidence_overflow,omitempty"`
	EvidenceOverflowReason     string                     `json:"evidence_overflow_reason,omitempty"`
	EvidenceOverflowLimit      string                     `json:"evidence_overflow_limit,omitempty"`
	UncertifiedTermination     bool                       `json:"uncertified_termination,omitempty"`
}

// emptyState builds a fresh, unpopulated state for a (repo, session).
// The ReportPath is set up-front so callers referencing state.ReportPath
// before any check has run still see a valid path (the file may not
// exist yet).
func emptyState(repoRoot, sessionID string) SessionState {
	return SessionState{
		RepoRoot:       repoRoot,
		SessionID:      sessionID,
		ReadPaths:      []string{},
		WritePaths:     []string{},
		WriteEpochs:    map[string]uint64{},
		Commands:       []string{},
		Claims:         []string{},
		CommandResults: []CommandResult{},
		ReportPath:     sessionReportPath(repoRoot, sessionID),
	}
}

// --- path helpers ---------------------------------------------------

// stateRoot returns the base directory where every agent session's
// state lives. Honours RECONC_CLAUDE_STATE_DIR first, then falls back
// to $RECONC_HOME/sessions/claude, then ~/.reconc/sessions/claude.
func stateRoot() string {
	return retention.ResolveStateRoot()
}

// projectKey is a short deterministic hash of the repo root. Keeps
// state paths stable + filesystem-safe regardless of the repo's real
// path (which may contain spaces or non-ASCII characters).
func projectKey(repoRoot string) string {
	sum := sha256.Sum256([]byte(repoRoot))
	return hex.EncodeToString(sum[:])[:16]
}

func projectDir(repoRoot string) string {
	return retention.ProjectDir(stateRoot(), repoRoot)
}

func sessionStatePath(repoRoot, sessionID string) string {
	return filepath.Join(projectDir(repoRoot), "sessions", sessionFileKey(sessionID)+".json")
}

func sessionReportPath(repoRoot, sessionID string) string {
	return filepath.Join(projectDir(repoRoot), "reports", sessionFileKey(sessionID)+".json")
}

func activeSessionPath(repoRoot string) string {
	return filepath.Join(projectDir(repoRoot), "active-session.txt")
}

func activeSessionLockPath(repoRoot string) string {
	return filepath.Join(projectDir(repoRoot), "locks", "active-session.lock")
}

func sessionLockPath(repoRoot, sessionID string) string {
	return filepath.Join(projectDir(repoRoot), "locks", sessionFileKey(sessionID)+".lock")
}

func legacySessionStatePath(repoRoot, sessionID string) string {
	return filepath.Join(projectDir(repoRoot), "sessions", sanitiseID(sessionID)+".json")
}

func legacySessionReportPath(repoRoot, sessionID string) string {
	return filepath.Join(projectDir(repoRoot), "reports", sanitiseID(sessionID)+".json")
}

// sanitiseID scrubs a session id to a safe filename. Claude Code sends
// UUIDs so this is almost always a no-op, but we defend against any
// payload that slips path-traversal-like characters through.
func sanitiseID(id string) string {
	id = strings.TrimSpace(id)
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "unknown"
	}
	return out
}

func sessionFileKey(id string) string {
	return retention.SessionFileID(id)
}

func validateSessionID(sessionID string) error {
	return retention.ValidateSessionID(sessionID)
}

// --- resolve repo root ----------------------------------------------

// ResolvedRepoRoot is a validated operating-system filesystem identity. The
// path is intentionally private so callers outside this package cannot forge a
// resolved root and bypass the existence, directory, or identity checks.
type ResolvedRepoRoot struct {
	path string
}

// Path returns the canonical filesystem path carried by this root handle.
func (root ResolvedRepoRoot) Path() string {
	return root.path
}

// ResolveRepoRoot resolves the repo root to its operating-system filesystem
// identity. This follows Unix symlinks and Windows reparse points and expands
// Windows 8.3 aliases. The returned path is stamped into every state file so
// all events compare consistently.
//
// Errors if the path does not exist or is not a directory -- we want
// the hook adapter to fail fast on bogus paths rather than silently
// create state for a nonexistent repo.
func ResolveRepoRoot(repoRoot string) (string, error) {
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return "", err
	}
	return root.path, nil
}

// ResolveRepoRootRef validates and canonicalizes a repository root once for a
// complete hook request. Downstream resolved APIs accept this opaque value and
// therefore do not rediscover the same filesystem identity.
func ResolveRepoRootRef(repoRoot string) (ResolvedRepoRoot, error) {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return ResolvedRepoRoot{}, fmt.Errorf("resolve repo path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return ResolvedRepoRoot{}, fmt.Errorf("repo path does not exist: %s: %w", abs, err)
	}
	if !info.IsDir() {
		return ResolvedRepoRoot{}, fmt.Errorf("repo path is not a directory: %s", abs)
	}
	resolved, err := pathidentity.ResolveExisting(abs)
	if err != nil {
		return ResolvedRepoRoot{}, fmt.Errorf("resolve repo filesystem identity: %w", err)
	}
	return ResolvedRepoRoot{path: resolved}, nil
}

// --- load / save -----------------------------------------------------

// LoadSessionState returns the state file for (repo, session_id), or
// an empty state if the file doesn't yet exist. Malformed on-disk
// content is a hard error; we refuse to keep running with a state
// file we can't trust.
func LoadSessionState(repoRoot, sessionID string) (SessionState, error) {
	if err := validateSessionID(sessionID); err != nil {
		return SessionState{}, err
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return SessionState{}, err
	}
	return loadSessionStateWithLockResolved(root, sessionID)
}

func loadSessionStateWithLockResolved(root, sessionID string) (SessionState, error) {
	var state SessionState
	err := withSessionLock(root, sessionID, func() error {
		loaded, err := loadSessionStateResolved(root, sessionID)
		state = loaded
		return err
	})
	return state, err
}

func loadSessionStateResolved(root, sessionID string) (SessionState, error) {
	if err := validateSessionID(sessionID); err != nil {
		return SessionState{}, err
	}
	path := sessionStatePath(root, sessionID)
	file, err := os.Open(path)
	loadedLegacyPath := false
	legacyPath := legacySessionStatePath(root, sessionID)
	if os.IsNotExist(err) && legacyPath != path {
		file, err = os.Open(legacyPath)
		if err == nil {
			path = legacyPath
			loadedLegacyPath = true
		}
	}
	if err != nil {
		if os.IsNotExist(err) {
			return emptyState(root, sessionID), nil
		}
		return SessionState{}, fmt.Errorf("read session state %s: %w", path, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxLegacySessionStateBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return SessionState{}, fmt.Errorf("read session state %s: %w", path, errors.Join(readErr, closeErr))
	}
	if len(data) > maxLegacySessionStateBytes {
		return SessionState{}, fmt.Errorf("session state exceeds %d-byte recovery limit: %s", maxLegacySessionStateBytes, path)
	}

	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return SessionState{}, fmt.Errorf("session state is not valid JSON: %s: %w", path, err)
	}
	// Validate identity and command-result invariants before normalizing the
	// bounded evidence collections below.
	if state.SessionID != "" && state.SessionID != sessionID {
		return SessionState{}, fmt.Errorf("%s: session_id %q does not match requested session %q", path, state.SessionID, sessionID)
	}
	state.SessionID = sessionID
	state.Runtime = normalizeRuntimeName(state.Runtime)
	if state.RepoRoot != "" {
		storedRoot := filepath.Clean(state.RepoRoot)
		if storedRoot != filepath.Clean(root) {
			resolvedStoredRoot, resolveErr := pathidentity.ResolveExisting(state.RepoRoot)
			if resolveErr != nil || filepath.Clean(resolvedStoredRoot) != filepath.Clean(root) {
				return SessionState{}, fmt.Errorf("%s: repo_root %q does not match resolved repository %q", path, state.RepoRoot, root)
			}
		} else if _, resolveErr := os.Stat(state.RepoRoot); resolveErr != nil {
			return SessionState{}, fmt.Errorf("%s: repo_root %q does not match resolved repository %q", path, state.RepoRoot, root)
		}
	}
	state.RepoRoot = root
	if len(state.WritePaths) > 0 && state.EvidenceEpoch == 0 && len(state.WriteEpochs) == 0 {
		// Legacy states cannot prove whether recorded commands ran before or
		// after their writes. Put every legacy write one epoch ahead so old
		// command outcomes fail closed until the command is rerun.
		state.EvidenceEpoch = 1
		state.WriteEpochs = make(map[string]uint64, len(state.WritePaths))
		for _, writePath := range state.WritePaths {
			state.WriteEpochs[writePath] = state.EvidenceEpoch
		}
	}
	for i, cr := range state.CommandResults {
		if strings.TrimSpace(cr.Command) == "" {
			return SessionState{}, fmt.Errorf("%s: command_results[%d].command is empty", path, i)
		}
		if cr.Outcome != "success" && cr.Outcome != "failure" {
			return SessionState{}, fmt.Errorf("%s: command_results[%d].outcome must be success|failure", path, i)
		}
	}
	expectedReportPath := sessionReportPath(root, sessionID)
	if state.ReportPath != "" && filepath.Clean(state.ReportPath) != filepath.Clean(expectedReportPath) {
		legacyReportPath := legacySessionReportPath(root, sessionID)
		if !loadedLegacyPath || filepath.Clean(state.ReportPath) != filepath.Clean(legacyReportPath) {
			return SessionState{}, fmt.Errorf("%s: report_path %q does not match session report %q", path, state.ReportPath, expectedReportPath)
		}
	}
	state.ReportPath = expectedReportPath
	state = normalizeSessionState(state)
	if taint, err := loadEvidenceTaint(root); err != nil {
		return SessionState{}, err
	} else if taint != nil {
		applyEvidenceTaint(&state, *taint)
	} else if state.EvidenceOverflow {
		if err := persistEvidenceTaint(root, state); err != nil {
			return SessionState{}, err
		}
	}
	return state, nil
}

// saveSessionState writes the state file atomically. Tmp-file-then-
// rename so a crash mid-write never leaves an unreadable state.
func saveSessionStateLocked(state SessionState) error {
	_, err := saveSessionStateLockedIfChanged(state)
	return err
}

func saveSessionStateLockedIfChanged(state SessionState) (bool, error) {
	if err := validateSessionID(state.SessionID); err != nil {
		return false, err
	}
	path := sessionStatePath(state.RepoRoot, state.SessionID)
	if err := ensurePrivateStateDir(filepath.Dir(path)); err != nil {
		return false, fmt.Errorf("mkdir session dir: %w", err)
	}
	// Deterministic marshalling (sorted keys, 2-space indent, trailing
	// newline) so diffing session state across runs is git-friendly.
	data, err := marshalStateDeterministic(state)
	if err != nil {
		return false, fmt.Errorf("marshal session state: %w", err)
	}
	written, err := atomicfile.WriteIfChanged(path, data, 0o600)
	if err != nil {
		return false, fmt.Errorf("write session state: %w", err)
	}
	return written, nil
}

func ensurePrivateStateDir(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("private state path is not a directory: %s", path)
		}
		if filepath.Separator == '\\' || info.Mode().Perm() == 0o700 {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func saveSessionState(state SessionState) error {
	root, err := ResolveRepoRoot(state.RepoRoot)
	if err != nil {
		return err
	}
	state.RepoRoot = root
	return withSessionLock(root, state.SessionID, func() error {
		return saveSessionStateLocked(state)
	})
}

func withSessionLock(repoRoot, sessionID string, fn func() error) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	return withStateLock(sessionLockPath(repoRoot, sessionID), "session", fn)
}

func withActiveSessionLock(repoRoot string, fn func() error) error {
	return withStateLock(activeSessionLockPath(repoRoot), "active session", fn)
}

func withStateLock(lockPath, subject string, fn func() error) error {
	if err := ensurePrivateStateDir(filepath.Dir(lockPath)); err != nil {
		return fmt.Errorf("mkdir %s lock dir: %w", subject, err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open %s lock: %w", subject, err)
	}
	unlock, err := filelock.Lock(file)
	if err != nil {
		closeErr := file.Close()
		return errors.Join(fmt.Errorf("lock %s state: %w", subject, err), wrapOperationError("close "+subject+" lock", closeErr))
	}
	fnErr := fn()
	unlockErr := unlock()
	closeErr := file.Close()
	return errors.Join(fnErr, wrapOperationError("unlock "+subject+" state", unlockErr), wrapOperationError("close "+subject+" lock", closeErr))
}

func wrapOperationError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// MutateSessionState serializes load -> mutate -> save for one session.
// Hook evidence updates must use this path so concurrent tool events
// merge instead of overwriting each other.
func MutateSessionState(repoRoot, sessionID string, mutate func(SessionState) SessionState) (SessionState, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return SessionState{}, err
	}
	return mutateSessionStateResolved(root, sessionID, mutate)
}

func mutateSessionStateResolved(root, sessionID string, mutate func(SessionState) SessionState) (SessionState, error) {
	var updated SessionState
	err := withSessionLock(root, sessionID, func() error {
		state, err := loadSessionStateResolved(root, sessionID)
		if err != nil {
			return err
		}
		updated = mutate(state)
		if updated.EvidenceOverflow && !state.EvidenceOverflow && evidenceFieldRotatable(updated.EvidenceOverflowReason) && sessionHasEvidence(state) {
			rotated, rotateErr := rotateSessionEvidenceLocked(root, state)
			if rotateErr == nil {
				updated = mutate(rotated)
			} else {
				if state.EvidenceSegmentCount >= maxEvidenceSegments {
					updated.EvidenceOverflowLimit = "segment_count"
				} else {
					updated.EvidenceOverflowLimit = "segment_storage"
				}
			}
		}
		updated.RepoRoot = root
		updated.SessionID = sessionID
		if updated.ReportPath == "" {
			updated.ReportPath = sessionReportPath(root, sessionID)
		}
		stateChanged := !reflect.DeepEqual(state, updated)
		if stateChanged {
			updated = normalizeSessionState(updated)
			stateChanged = !reflect.DeepEqual(state, updated)
		}
		if !stateChanged {
			info, err := os.Stat(sessionStatePath(root, sessionID))
			if errors.Is(err, os.ErrNotExist) {
				stateChanged = true
			} else if err != nil {
				return fmt.Errorf("inspect session state before no-op mutation: %w", err)
			} else if filepath.Separator != '\\' && info.Mode().Perm() != 0o600 {
				stateChanged = true
			}
		}
		if stateChanged {
			if err := saveSessionStateLocked(updated); err != nil {
				return err
			}
		}
		if updated.EvidenceOverflow {
			if err := persistEvidenceTaint(root, updated); err != nil {
				return err
			}
		}
		return writeActiveSession(root, sessionID)
	})
	if err != nil {
		return SessionState{}, err
	}
	return updated, nil
}

// marshalStateDeterministic serialises SessionState with sorted keys
// (which Go's default json.Marshal does for struct fields anyway) and
// trailing newline. We dedupe + sort the slice fields first so two
// semantically-equal states produce identical bytes.
func marshalStateDeterministic(state SessionState) ([]byte, error) {
	state = normalizeSessionState(state)
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	if len(body) > MaxSessionStateBytes {
		return nil, fmt.Errorf("bounded session state is %d bytes; maximum is %d", len(body), MaxSessionStateBytes)
	}
	return body, nil
}

func sortedUnique(xs []string) []string {
	if len(xs) == 0 {
		return []string{}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(xs))
	for _, v := range xs {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func sortedUniqueExact(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// --- active session tracking ---------------------------------------

// InitializeSessionState resets the session state (used at SessionStart).
// Also records the sessionID as the active one for this repo so later
// events without a session_id can fall back to it.
func InitializeSessionState(repoRoot, sessionID string) (SessionState, error) {
	return initializeSessionState(repoRoot, sessionID, "")
}

func initializeSessionState(repoRoot, sessionID, runtime string) (SessionState, error) {
	if err := validateSessionID(sessionID); err != nil {
		return SessionState{}, err
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return SessionState{}, err
	}
	return initializeSessionStateResolved(root, sessionID, runtime)
}

func initializeSessionStateResolved(root, sessionID, runtime string) (SessionState, error) {
	if err := validateSessionID(sessionID); err != nil {
		return SessionState{}, err
	}
	state := emptyState(root, sessionID)
	state.Runtime = normalizeRuntimeName(runtime)
	if taint, err := loadEvidenceTaint(root); err != nil {
		return SessionState{}, err
	} else if taint != nil {
		applyEvidenceTaint(&state, *taint)
	}
	if err := withSessionLock(root, sessionID, func() error {
		if err := saveSessionStateLocked(state); err != nil {
			return err
		}
		return writeActiveSession(root, sessionID)
	}); err != nil {
		return SessionState{}, err
	}
	return state, nil
}

// EnsureSessionState loads the state for (repo, session_id), creating
// an empty one if missing. Also refreshes the active-session pointer
// so resolveActiveSessionID returns this sessionID next time.
func EnsureSessionState(repoRoot, sessionID string) (SessionState, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return SessionState{}, err
	}
	return ensureSessionStateResolved(root, sessionID)
}

func ensureSessionStateResolved(root, sessionID string) (SessionState, error) {
	var state SessionState
	err := withSessionLock(root, sessionID, func() error {
		loaded, err := loadSessionStateResolved(root, sessionID)
		if err != nil {
			return err
		}
		state = loaded
		// Persist any default-normalisation done by LoadSessionState and
		// record this as the active session.
		if err := saveSessionStateLocked(state); err != nil {
			return err
		}
		return writeActiveSession(root, sessionID)
	})
	if err != nil {
		return SessionState{}, err
	}
	return state, nil
}

// observeSessionStateResolved validates an existing state without creating a
// session, refreshing the active-session pointer, or serializing unchanged
// state. A passive event arriving before SessionStart is liveness only.
func observeSessionStateResolved(root, sessionID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	canonicalPath := sessionStatePath(root, sessionID)
	legacyPath := legacySessionStatePath(root, sessionID)
	present := false
	for _, path := range []string{canonicalPath, legacyPath} {
		if path == legacyPath && legacyPath == canonicalPath {
			continue
		}
		_, err := os.Stat(path)
		switch {
		case err == nil:
			present = true
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			return fmt.Errorf("inspect passive session state: %w", err)
		}
	}
	if !present {
		return nil
	}
	_, err := loadSessionStateWithLockResolved(root, sessionID)
	return err
}

// CleanupSessionState removes the mutable state file for one session
// (called at SessionEnd). The corresponding report file is preserved
// so post-session diagnostics remain available.
func CleanupSessionState(repoRoot, sessionID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	return cleanupSessionStateResolved(root, sessionID)
}

func cleanupSessionStateResolved(root, sessionID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	return withSessionLock(root, sessionID, func() error {
		state, err := loadSessionStateResolved(root, sessionID)
		if err != nil {
			return err
		}
		if _, err := loadCompleteSessionEvidence(root, state); err != nil {
			return fmt.Errorf("verify evidence chain before cleanup: %w", err)
		}
		taint, err := loadEvidenceTaint(root)
		if err != nil {
			return err
		}
		return withActiveSessionLock(root, func() error {
			activePath := activeSessionPath(root)
			activeID, err := readActiveSessionID(activePath)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			for _, statePath := range []string{sessionStatePath(root, sessionID), legacySessionStatePath(root, sessionID)} {
				if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove session state: %w", err)
				}
			}
			preDecisionPath := filepath.Join(projectDir(root), "pre-decisions", sessionFileKey(sessionID)+".json")
			if err := os.Remove(preDecisionPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove pre-decision cache: %w", err)
			}
			if taint == nil && !state.EvidenceOverflow {
				if err := os.RemoveAll(evidenceSegmentsDir(root, sessionID)); err != nil {
					return fmt.Errorf("remove completed evidence segments: %w", err)
				}
			}
			if activeID != sessionID {
				return nil
			}
			if err := os.Remove(activePath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove active session file: %w", err)
			}
			return nil
		})
	})
}

// ResolveActiveSessionID returns the last-known active session id for
// a repo, or "" if none is recorded. Used by `reconc hook claim` when
// the caller hasn't specified --session.
func ResolveActiveSessionID(repoRoot string) (string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return "", err
	}
	return resolveActiveSessionIDResolved(root)
}

func resolveActiveSessionIDResolved(root string) (string, error) {
	var sessionID string
	err := withActiveSessionLock(root, func() error {
		active, readErr := readActiveSessionID(activeSessionPath(root))
		sessionID = active
		return readErr
	})
	return sessionID, err
}

func readActiveSessionID(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read active session file: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxSessionIDBytes+2))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return "", fmt.Errorf("read active session file: %w", errors.Join(readErr, closeErr))
	}
	if len(data) > maxSessionIDBytes+1 {
		return "", fmt.Errorf("active session file exceeds %d bytes", maxSessionIDBytes+1)
	}
	sessionID := strings.TrimSuffix(string(data), "\n")
	if err := validateSessionID(sessionID); err != nil {
		return "", fmt.Errorf("invalid active session file: %w", err)
	}
	return sessionID, nil
}

func writeActiveSession(repoRoot, sessionID string) error {
	_, err := writeActiveSessionIfChanged(repoRoot, sessionID)
	return err
}

func writeActiveSessionIfChanged(repoRoot, sessionID string) (bool, error) {
	if err := validateSessionID(sessionID); err != nil {
		return false, err
	}
	current, err := activeSessionMatches(repoRoot, sessionID)
	if err != nil {
		return false, err
	}
	if current {
		return false, nil
	}
	var written bool
	err = withActiveSessionLock(repoRoot, func() error {
		changed, writeErr := writeActiveSessionLockedIfChanged(repoRoot, sessionID)
		written = changed
		return writeErr
	})
	return written, err
}

func writeActiveSessionLockedIfChanged(repoRoot, sessionID string) (bool, error) {
	if err := validateSessionID(sessionID); err != nil {
		return false, err
	}
	path := activeSessionPath(repoRoot)
	if err := ensurePrivateStateDir(filepath.Dir(path)); err != nil {
		return false, fmt.Errorf("mkdir active-session dir: %w", err)
	}
	current, err := activeSessionMatches(repoRoot, sessionID)
	if err != nil {
		return false, err
	}
	if current {
		return false, nil
	}
	written, err := atomicfile.WriteIfChanged(path, []byte(sessionID+"\n"), 0o600)
	if err != nil {
		return false, fmt.Errorf("write active session: %w", err)
	}
	return written, nil
}

func activeSessionMatches(repoRoot, sessionID string) (bool, error) {
	path := activeSessionPath(repoRoot)
	current, err := readActiveSessionID(path)
	if err != nil || current != sessionID {
		return false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("stat active session: %w", err)
	}
	return filepath.Separator == '\\' || info.Mode().Perm() == 0o600, nil
}

// --- state mutators -------------------------------------------------

// AppendReadPath adds one read path to the state (dedup, non-empty).
// Returns a NEW state; callers should save it explicitly.
func AppendReadPath(state SessionState, p string) SessionState {
	appendBoundedExactString(&state, &state.ReadPaths, p, maxPathEvidenceItems, maxPathEvidenceBytes, maxPathBytes, "read_paths")
	return state
}

// AppendWritePath adds one write path.
func AppendWritePath(state SessionState, p string) SessionState {
	appendBoundedExactString(&state, &state.WritePaths, p, maxPathEvidenceItems, maxPathEvidenceBytes, maxPathBytes, "write_paths")
	return state
}

// RecordWriteEvent advances the causal evidence clock once and records every
// path produced by that single tool event at the new epoch.
func RecordWriteEvent(state SessionState, paths []string) SessionState {
	if len(paths) == 0 {
		return state
	}
	if state.EvidenceEpoch < ^uint64(0)-1 {
		state.EvidenceEpoch++
	}
	if state.WriteEpochs == nil {
		state.WriteEpochs = map[string]uint64{}
	}
	for _, path := range paths {
		before := len(state.WritePaths)
		state = AppendWritePath(state, path)
		if path == "" {
			continue
		}
		if len(state.WritePaths) > before || containsString(state.WritePaths, path) {
			state.WriteEpochs[path] = state.EvidenceEpoch
		}
	}
	return state
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// AppendCommand adds one command string.
func AppendCommand(state SessionState, cmd string) SessionState {
	appendBoundedString(&state, &state.Commands, cmd, maxCommandEvidenceItems, maxCommandEvidenceBytes, maxCommandBytes, "commands")
	return state
}

// AppendClaim adds one explicit claim.
func AppendClaim(state SessionState, claim string) SessionState {
	appendBoundedString(&state, &state.Claims, claim, maxClaimEvidenceItems, maxClaimEvidenceBytes, maxClaimBytes, "claims")
	return state
}

// AppendCommandResult adds one command-execution outcome.
func AppendCommandResult(state SessionState, result CommandResult) SessionState {
	appendBoundedCommandResult(&state, result)
	return state
}

// RecordMaterialEvent advances the bounded semantic-progress clock for a
// write or command event. Reads intentionally do not count as progress.
func RecordMaterialEvent(state SessionState, signature string) SessionState {
	signature = strings.TrimSpace(signature)
	if signature == "" || signature == state.LastMaterialSignature {
		return state
	}
	state.LastMaterialSignature = signature
	if state.MaterialEvents < ^uint64(0) {
		state.MaterialEvents++
	}
	return state
}

// SaveSessionState exposes the tmp-file-rename writer. Public so the
// adapter handlers can persist their mutations.
func SaveSessionState(state SessionState) error {
	return saveSessionState(state)
}
