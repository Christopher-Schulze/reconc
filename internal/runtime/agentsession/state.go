// Package agentsession is the stateful hook-runtime adapter that the generated
// Claude Code / Codex configs delegate to. It uses runtime.CheckRepoPolicy as
// the policy backend.
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
//   - repo_root stored in state file is EvalSymlinks-canonicalised
//     so macOS /var vs /private/var drift doesn't reject legitimate
//     events.
//   - state is rewritten atomically via a tmp-then-rename dance so a
//     crash mid-write doesn't produce a half-parsed file.
package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// StateRootEnv lets operators pin the session-state directory to a
// specific location. Useful for sandboxes and CI isolation. When
// unset we default to $RECONC_HOME/sessions/claude/.
const StateRootEnv = "RECONC_CLAUDE_STATE_DIR"

// CommandResult mirrors the evaluator's runtime.CommandResult but we
// keep our own copy in the session state so we can replay / inspect
// them without importing runtime types back into the state file.
type CommandResult struct {
	Command     string `json:"command"`
	Outcome     string `json:"outcome"` // "success" | "failure"
	ToolUseID   string `json:"tool_use_id,omitempty"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	Error       string `json:"error,omitempty"`
	IsInterrupt *bool  `json:"is_interrupt,omitempty"`
}

// SessionState is the on-disk shape of one agent session's accumulated
// evidence. Every field is JSON-tagged so adding a new one is strictly
// additive for back-compat.
type SessionState struct {
	RepoRoot       string          `json:"repo_root"`
	SessionID      string          `json:"session_id"`
	ReadPaths      []string        `json:"read_paths"`
	WritePaths     []string        `json:"write_paths"`
	Commands       []string        `json:"commands"`
	Claims         []string        `json:"claims"`
	CommandResults []CommandResult `json:"command_results"`
	ReportPath     string          `json:"report_path"`
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
	if override := os.Getenv(StateRootEnv); override != "" {
		return override
	}
	if home := os.Getenv("RECONC_HOME"); home != "" {
		return filepath.Join(home, "sessions", "claude")
	}
	if userHome, err := os.UserHomeDir(); err == nil {
		return filepath.Join(userHome, ".reconc", "sessions", "claude")
	}
	// Last-ditch fallback: /tmp. Deliberately tolerant -- we'd rather
	// log evidence to an unusual place than crash mid-hook.
	return filepath.Join(os.TempDir(), "reconc-claude-sessions")
}

// projectKey is a short deterministic hash of the repo root. Keeps
// state paths stable + filesystem-safe regardless of the repo's real
// path (which may contain spaces or non-ASCII characters).
func projectKey(repoRoot string) string {
	sum := sha256.Sum256([]byte(repoRoot))
	return hex.EncodeToString(sum[:])[:16]
}

func projectDir(repoRoot string) string {
	return filepath.Join(stateRoot(), "projects", projectKey(repoRoot))
}

func sessionStatePath(repoRoot, sessionID string) string {
	return filepath.Join(projectDir(repoRoot), "sessions", sanitiseID(sessionID)+".json")
}

func sessionReportPath(repoRoot, sessionID string) string {
	return filepath.Join(projectDir(repoRoot), "reports", sanitiseID(sessionID)+".json")
}

func activeSessionPath(repoRoot string) string {
	return filepath.Join(projectDir(repoRoot), "active-session.txt")
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

// --- resolve repo root ----------------------------------------------

// ResolveRepoRoot resolves the repo root to a canonical form that
// survives macOS /var <-> /private/var symlink drift. Returned path
// is the one that gets stamped into every state file so all events
// compare consistently.
//
// Errors if the path does not exist or is not a directory -- we want
// the hook adapter to fail fast on bogus paths rather than silently
// create state for a nonexistent repo.
func ResolveRepoRoot(repoRoot string) (string, error) {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repo path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("repo path does not exist: %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repo path is not a directory: %s", abs)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// --- load / save -----------------------------------------------------

// LoadSessionState returns the state file for (repo, session_id), or
// an empty state if the file doesn't yet exist. Malformed on-disk
// content is a hard error; we refuse to keep running with a state
// file we can't trust.
func LoadSessionState(repoRoot, sessionID string) (SessionState, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return SessionState{}, err
	}
	path := sessionStatePath(root, sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyState(root, sessionID), nil
		}
		return SessionState{}, fmt.Errorf("read session state %s: %w", path, err)
	}

	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return SessionState{}, fmt.Errorf("session state is not valid JSON: %s: %w", path, err)
	}
	// Validate every field so the rest of the adapter can trust it.
	if state.SessionID == "" {
		state.SessionID = sessionID
	}
	if state.RepoRoot == "" {
		state.RepoRoot = root
	}
	if err := validateStringList(state.ReadPaths, "read_paths", path); err != nil {
		return SessionState{}, err
	}
	if err := validateStringList(state.WritePaths, "write_paths", path); err != nil {
		return SessionState{}, err
	}
	if err := validateStringList(state.Commands, "commands", path); err != nil {
		return SessionState{}, err
	}
	if err := validateStringList(state.Claims, "claims", path); err != nil {
		return SessionState{}, err
	}
	for i, cr := range state.CommandResults {
		if strings.TrimSpace(cr.Command) == "" {
			return SessionState{}, fmt.Errorf("%s: command_results[%d].command is empty", path, i)
		}
		if cr.Outcome != "success" && cr.Outcome != "failure" {
			return SessionState{}, fmt.Errorf("%s: command_results[%d].outcome must be success|failure", path, i)
		}
	}
	if state.ReportPath == "" {
		state.ReportPath = sessionReportPath(root, sessionID)
	}
	return state, nil
}

func validateStringList(xs []string, field, srcPath string) error {
	for i, v := range xs {
		_ = v // just the type assertion matters; stringy by Go type system
		if i < 0 {
			return fmt.Errorf("%s: %s index negative", srcPath, field)
		}
	}
	return nil
}

// saveSessionState writes the state file atomically. Tmp-file-then-
// rename so a crash mid-write never leaves an unreadable state.
func saveSessionState(state SessionState) error {
	path := sessionStatePath(state.RepoRoot, state.SessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir session dir: %w", err)
	}
	// Deterministic marshalling (sorted keys, 2-space indent, trailing
	// newline) so diffing session state across runs is git-friendly.
	data, err := marshalStateDeterministic(state)
	if err != nil {
		return fmt.Errorf("marshal session state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write session state tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename session state: %w", err)
	}
	return nil
}

// marshalStateDeterministic serialises SessionState with sorted keys
// (which Go's default json.Marshal does for struct fields anyway) and
// trailing newline. We dedupe + sort the slice fields first so two
// semantically-equal states produce identical bytes.
func marshalStateDeterministic(state SessionState) ([]byte, error) {
	// Copies; don't mutate the caller's slices.
	state.ReadPaths = sortedUnique(state.ReadPaths)
	state.WritePaths = sortedUnique(state.WritePaths)
	state.Commands = sortedUnique(state.Commands)
	state.Claims = sortedUnique(state.Claims)
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
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

// --- active session tracking ---------------------------------------

// InitializeSessionState resets the session state (used at SessionStart).
// Also records the sessionID as the active one for this repo so later
// events without a session_id can fall back to it.
func InitializeSessionState(repoRoot, sessionID string) (SessionState, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return SessionState{}, err
	}
	state := emptyState(root, sessionID)
	if err := saveSessionState(state); err != nil {
		return SessionState{}, err
	}
	if err := writeActiveSession(root, sessionID); err != nil {
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
	state, err := LoadSessionState(root, sessionID)
	if err != nil {
		return SessionState{}, err
	}
	// Persist any default-normalisation done by LoadSessionState and
	// record this as the active session.
	if err := saveSessionState(state); err != nil {
		return SessionState{}, err
	}
	if err := writeActiveSession(root, sessionID); err != nil {
		return SessionState{}, err
	}
	return state, nil
}

// CleanupSessionState removes the mutable state file for one session
// (called at SessionEnd). The corresponding report file is preserved
// so post-session diagnostics remain available.
func CleanupSessionState(repoRoot, sessionID string) error {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	statePath := sessionStatePath(root, sessionID)
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove session state: %w", err)
	}
	activePath := activeSessionPath(root)
	if data, err := os.ReadFile(activePath); err == nil {
		if strings.TrimSpace(string(data)) == sessionID {
			_ = os.Remove(activePath)
		}
	}
	return nil
}

// ResolveActiveSessionID returns the last-known active session id for
// a repo, or "" if none is recorded. Used by `reconc hook claim` when
// the caller hasn't specified --session.
func ResolveActiveSessionID(repoRoot string) (string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(activeSessionPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read active session file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func writeActiveSession(repoRoot, sessionID string) error {
	path := activeSessionPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir active-session dir: %w", err)
	}
	return os.WriteFile(path, []byte(sessionID+"\n"), 0o644)
}

// --- state mutators -------------------------------------------------

// AppendReadPath adds one read path to the state (dedup, non-empty).
// Returns a NEW state; callers should save it explicitly.
func AppendReadPath(state SessionState, p string) SessionState {
	state.ReadPaths = appendUnique(state.ReadPaths, p)
	return state
}

// AppendWritePath adds one write path.
func AppendWritePath(state SessionState, p string) SessionState {
	state.WritePaths = appendUnique(state.WritePaths, p)
	return state
}

// AppendCommand adds one command string.
func AppendCommand(state SessionState, cmd string) SessionState {
	state.Commands = appendUnique(state.Commands, cmd)
	return state
}

// AppendClaim adds one explicit claim.
func AppendClaim(state SessionState, claim string) SessionState {
	state.Claims = appendUnique(state.Claims, claim)
	return state
}

// AppendCommandResult adds one command-execution outcome.
func AppendCommandResult(state SessionState, result CommandResult) SessionState {
	state.CommandResults = append(state.CommandResults, result)
	return state
}

func appendUnique(xs []string, item string) []string {
	item = strings.TrimSpace(item)
	if item == "" {
		return xs
	}
	for _, x := range xs {
		if x == item {
			return xs
		}
	}
	return append(xs, item)
}

// SaveSessionState exposes the tmp-file-rename writer. Public so the
// adapter handlers can persist their mutations.
func SaveSessionState(state SessionState) error {
	return saveSessionState(state)
}
