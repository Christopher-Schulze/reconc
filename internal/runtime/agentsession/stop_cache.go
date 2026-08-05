package agentsession

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/runtime"
)

const (
	stopPolicyFingerprintVersion = "stop-policy-report-v6"
	stopPolicyUntrackedModeEnv   = "RECONC_STOP_FINGERPRINT_UNTRACKED"
	maxStopReportBytes           = 32 << 20
	maxGitControlFileBytes       = 1 << 20
	maxPackedRefsBytes           = 64 << 20
)

type stopPolicyFingerprintInput struct {
	Version            string            `json:"version"`
	RepoRoot           string            `json:"repo_root"`
	SessionID          string            `json:"session_id,omitempty"`
	PolicyLockHash     string            `json:"policy_lock_hash"`
	ReportFormat       string            `json:"report_format"`
	SchemaBase         string            `json:"schema_base"`
	ReadPaths          []string          `json:"read_paths"`
	WritePaths         []string          `json:"write_paths"`
	WriteEpochs        map[string]uint64 `json:"write_epochs"`
	Commands           []string          `json:"commands"`
	Claims             []string          `json:"claims"`
	CommandResults     []CommandResult   `json:"command_results"`
	GitHead            string            `json:"git_head"`
	GitStatusMode      string            `json:"git_status_mode"`
	GitStatusOK        bool              `json:"git_status_ok"`
	GitStatus          string            `json:"git_status"`
	GitDirtyFiles      []gitDirtyFile    `json:"git_dirty_files"`
	ReconcAuditNoCache string            `json:"reconc_audit_no_cache"`
}

type stopPolicyEvidenceInput struct {
	ReadPaths      []string          `json:"read_paths"`
	WritePaths     []string          `json:"write_paths"`
	WriteEpochs    map[string]uint64 `json:"write_epochs"`
	Commands       []string          `json:"commands"`
	Claims         []string          `json:"claims"`
	CommandResults []CommandResult   `json:"command_results"`
}

type gitDirtyFile struct {
	Path         string `json:"path"`
	WorktreeHash string `json:"worktree_hash"`
	IndexEntry   string `json:"index_entry"`
}

type stopPolicyGitSnapshot struct {
	Head       string
	Status     string
	StatusMode string
	StatusOK   bool
}

type stopPolicyCheckResult struct {
	Report      *runtime.CheckReport
	GitSnapshot stopPolicyGitSnapshot
}

// CompletionStateSnapshot is the read-only repository/session identity shared
// by the final completion gate and the latency-bounded Stop fingerprint. It
// deliberately contains hashes and normalized evidence, never raw report
// bytes, prompts, command output, or environment values.
type CompletionStateSnapshot struct {
	FormatVersion          string                  `json:"format_version"`
	RepoRoot               string                  `json:"repo_root"`
	Fingerprint            string                  `json:"fingerprint"`
	PolicyLockHash         string                  `json:"policy_lock_hash"`
	SessionID              string                  `json:"session_id,omitempty"`
	SessionEvidenceHash    string                  `json:"session_evidence_hash,omitempty"`
	SessionReportHash      string                  `json:"session_report_hash,omitempty"`
	SessionReportTrusted   bool                    `json:"session_report_trusted"`
	EvidenceEpoch          uint64                  `json:"evidence_epoch,omitempty"`
	EvidenceOverflow       bool                    `json:"evidence_overflow"`
	EvidenceOverflowReason string                  `json:"evidence_overflow_reason,omitempty"`
	EvidenceOverflowLimit  string                  `json:"evidence_overflow_limit,omitempty"`
	GitAvailable           bool                    `json:"git_available"`
	GitHead                string                  `json:"git_head,omitempty"`
	GitIndexHash           string                  `json:"git_index_hash,omitempty"`
	GitStatusMode          string                  `json:"git_status_mode,omitempty"`
	GitStatusOK            bool                    `json:"git_status_ok"`
	GitStatus              string                  `json:"git_status,omitempty"`
	WorktreeHash           string                  `json:"worktree_hash,omitempty"`
	WorktreeTrusted        bool                    `json:"worktree_trusted"`
	WorktreeMatchesIndex   bool                    `json:"worktree_matches_index"`
	DirtyPaths             []string                `json:"dirty_paths"`
	Inputs                 runtime.ExecutionInputs `json:"inputs"`
}

type completionStateFingerprintInput struct {
	Version                   string                     `json:"version"`
	StopPolicyFingerprint     stopPolicyFingerprintInput `json:"stop_policy_fingerprint"`
	SessionReportHash         string                     `json:"session_report_hash"`
	ExpectedSessionReportHash string                     `json:"expected_session_report_hash"`
	GitIndexHash              string                     `json:"git_index_hash,omitempty"`
}

func runStopPolicyCheck(repoRoot string, state SessionState) (*runtime.CheckReport, error) {
	result, err := runStopPolicyCheckWithSnapshot(repoRoot, state)
	return result.Report, err
}

func runStopPolicyCheckWithSnapshot(repoRoot string, state SessionState) (stopPolicyCheckResult, error) {
	return runStopPolicyCheckWithSnapshotWithEvaluator(repoRoot, state, runtime.NewEvaluator())
}

func runStopPolicyCheckWithSnapshotWithEvaluator(repoRoot string, state SessionState, evaluator *runtime.Evaluator) (stopPolicyCheckResult, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return stopPolicyCheckResult{}, err
	}
	state, err = loadCompleteSessionEvidence(root, state)
	if err != nil {
		return stopPolicyCheckResult{}, fmt.Errorf("load evidence chain: %w", err)
	}
	return withStopPolicyReportLock(root, state.SessionID, func() (stopPolicyCheckResult, error) {
		return runStopPolicyCheckLocked(root, state, evaluator)
	})
}

func runStopPolicyCheckLocked(repoRoot string, state SessionState, evaluator *runtime.Evaluator) (stopPolicyCheckResult, error) {
	fingerprintInput := stopPolicyFingerprintInputFor(repoRoot, state)
	cacheable := stopPolicyFingerprintCacheable(fingerprintInput)
	snapshot := stopPolicyGitSnapshot{
		Head: fingerprintInput.GitHead, Status: fingerprintInput.GitStatus,
		StatusMode: fingerprintInput.GitStatusMode, StatusOK: fingerprintInput.GitStatusOK,
	}
	fingerprint := hashStopPolicyFingerprintInput(fingerprintInput)
	if cacheable && fingerprint != "" && state.StopPolicyFingerprint == fingerprint && state.StopPolicyReportHash != "" {
		report, reportHash, err := readLatestReport(repoRoot, state.SessionID)
		if err == nil && reportHash == state.StopPolicyReportHash {
			return stopPolicyCheckResult{Report: report, GitSnapshot: snapshot}, nil
		}
	}

	scopedWritePaths := stopScopeWritePathsToUncommitted(repoRoot, state.WritePaths, stopPolicyGitSnapshot{
		Head:       fingerprintInput.GitHead,
		Status:     fingerprintInput.GitStatus,
		StatusMode: fingerprintInput.GitStatusMode,
		StatusOK:   fingerprintInput.GitStatusOK,
	})
	report, err := runCheckAndSaveWithEvaluator(evaluator, repoRoot, state.SessionID, state.ReadPaths,
		scopedWritePaths, filterWriteEpochs(state.WriteEpochs, scopedWritePaths), state.Commands, state.CommandResults, state.Claims)
	if err != nil {
		return stopPolicyCheckResult{}, err
	}
	reportHash := hashCheckReport(report)
	if cacheable && fingerprint != "" && reportHash != "" {
		initialEvidenceHash := stopPolicyEvidenceHash(state)
		fingerprintInput.PolicyLockHash = fileContentHash(filepath.Join(repoRoot, ".reconc", "policy.lock.json"))
		reportFingerprint := hashStopPolicyFingerprintInput(fingerprintInput)
		if _, err := mutateSessionStateResolved(repoRoot, state.SessionID, func(current SessionState) SessionState {
			if stopPolicyEvidenceHash(current) == initialEvidenceHash {
				current.StopPolicyFingerprint = reportFingerprint
				current.StopPolicyEvidenceHash = initialEvidenceHash
				current.StopPolicyReportHash = reportHash
				current.ReportPath = sessionReportPath(repoRoot, state.SessionID)
			}
			return current
		}); err != nil {
			return stopPolicyCheckResult{}, fmt.Errorf("persist stop-policy cache metadata: %w", err)
		}
	}
	return stopPolicyCheckResult{Report: report, GitSnapshot: snapshot}, nil
}

func filterWriteEpochs(epochs map[string]uint64, paths []string) map[string]uint64 {
	out := make(map[string]uint64, len(paths))
	for _, path := range paths {
		if epoch := epochs[path]; epoch > 0 {
			out[path] = epoch
		}
	}
	return out
}

// stopScopeWritePathsToUncommitted intersects this session's recorded writes
// with the exact Git status snapshot already used by the Stop fingerprint.
// A clean committed path was gated at commit time and no longer needs to
// trigger stop-time batch rules. If Git status or path normalization cannot be
// trusted, the original path is retained so scoping always fails closed.
func stopScopeWritePathsToUncommitted(repoRoot string, writePaths []string, snapshot stopPolicyGitSnapshot) []string {
	if len(writePaths) == 0 {
		return writePaths
	}
	if !snapshot.StatusOK {
		return writePaths
	}
	dirty := map[string]struct{}{}
	for _, path := range dirtyPathsFromStatus(snapshot.Status) {
		dirty[path] = struct{}{}
	}
	if len(dirty) == 0 {
		return nil
	}
	out := make([]string, 0, len(writePaths))
	for _, writePath := range writePaths {
		rel := stopRepoRelPosix(repoRoot, writePath)
		if rel == "" || stopPathDirtyOrUnderDirtyDir(rel, dirty) {
			out = append(out, writePath)
		}
	}
	return out
}

func stopPathDirtyOrUnderDirtyDir(rel string, dirty map[string]struct{}) bool {
	if _, ok := dirty[rel]; ok {
		return true
	}
	segments := strings.Split(rel, "/")
	for i := 1; i < len(segments); i++ {
		if _, ok := dirty[strings.Join(segments[:i], "/")+"/"]; ok {
			return true
		}
	}
	return false
}

func stopRepoRelPosix(repoRoot, raw string) string {
	path := raw
	if path == "" {
		return ""
	}
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) {
		cleaned := filepath.Clean(path)
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return ""
		}
		return filepath.ToSlash(cleaned)
	}
	root, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return ""
	}
	cleaned, err := pathidentity.ResolveProspective(filepath.Clean(path))
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

func cachedCleanStopPolicyReportForEvidence(repoRoot string, state SessionState, evidenceHash string) (*runtime.CheckReport, bool) {
	if evidenceHash == "" || state.StopPolicyEvidenceHash != evidenceHash || state.StopPolicyReportHash == "" {
		return nil, false
	}
	fingerprintInput := stopPolicyFingerprintInputFor(repoRoot, state)
	if !stopPolicyFingerprintCacheable(fingerprintInput) {
		return nil, false
	}
	currentFingerprint := hashStopPolicyFingerprintInput(fingerprintInput)
	if currentFingerprint == "" || currentFingerprint != state.StopPolicyFingerprint {
		return nil, false
	}
	report, reportHash, err := readLatestReport(repoRoot, state.SessionID)
	if err != nil || reportHash != state.StopPolicyReportHash {
		return nil, false
	}
	if len(blockingViolations(report)) != 0 {
		return nil, false
	}
	return report, true
}

func withStopPolicyReportLock(repoRoot, sessionID string, fn func() (stopPolicyCheckResult, error)) (stopPolicyCheckResult, error) {
	if err := validateSessionID(sessionID); err != nil {
		return stopPolicyCheckResult{}, err
	}
	lockPath := filepath.Join(projectDir(repoRoot), "locks", sessionFileKey(sessionID)+".stop-policy.lock")
	if err := ensurePrivateStateDir(filepath.Dir(lockPath)); err != nil {
		return stopPolicyCheckResult{}, fmt.Errorf("create stop-policy lock dir: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return stopPolicyCheckResult{}, fmt.Errorf("open stop-policy lock: %w", err)
	}
	unlock, err := filelock.Lock(file)
	if err != nil {
		closeErr := file.Close()
		return stopPolicyCheckResult{}, errors.Join(fmt.Errorf("lock stop-policy report: %w", err), wrapOperationError("close stop-policy lock", closeErr))
	}
	result, fnErr := fn()
	unlockErr := unlock()
	closeErr := file.Close()
	if err := errors.Join(fnErr, wrapOperationError("unlock stop-policy report", unlockErr), wrapOperationError("close stop-policy lock", closeErr)); err != nil {
		return stopPolicyCheckResult{}, err
	}
	return result, nil
}

func stopPolicyFingerprint(repoRoot string, state SessionState) string {
	return hashStopPolicyFingerprintInput(stopPolicyFingerprintInputFor(repoRoot, state))
}

func stopPolicyFingerprintInputFor(repoRoot string, state SessionState) stopPolicyFingerprintInput {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		root = repoRoot
	}
	return stopPolicyFingerprintInputForSnapshot(root, state, stopPolicyGitSnapshotFor(root))
}

func stopPolicyFingerprintInputForSnapshot(root string, state SessionState, gitSnapshot stopPolicyGitSnapshot) stopPolicyFingerprintInput {
	dirtyFiles := []gitDirtyFile{}
	if gitSnapshot.StatusOK {
		dirtyFiles = gitDirtyFiles(root, gitSnapshot.Status)
	}
	return stopPolicyFingerprintInput{
		Version:            stopPolicyFingerprintVersion,
		RepoRoot:           root,
		SessionID:          state.SessionID,
		PolicyLockHash:     fileContentHash(filepath.Join(root, ".reconc", "policy.lock.json")),
		ReportFormat:       runtime.CheckReportFormatVersion,
		SchemaBase:         os.Getenv("RECONC_SCHEMA_BASE_URL"),
		ReadPaths:          sortedUniqueExact(state.ReadPaths),
		WritePaths:         sortedUniqueExact(state.WritePaths),
		WriteEpochs:        cloneWriteEpochs(state.WriteEpochs),
		Commands:           sortedUnique(state.Commands),
		Claims:             sortedUnique(state.Claims),
		CommandResults:     append([]CommandResult{}, state.CommandResults...),
		GitHead:            gitSnapshot.Head,
		GitStatusMode:      gitSnapshot.StatusMode,
		GitStatusOK:        gitSnapshot.StatusOK,
		GitStatus:          gitSnapshot.Status,
		GitDirtyFiles:      dirtyFiles,
		ReconcAuditNoCache: os.Getenv("RECONC_AUDIT_NO_CACHE"),
	}
}

func hashStopPolicyFingerprintInput(input stopPolicyFingerprintInput) string {
	body, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func stopPolicyFingerprintCacheable(input stopPolicyFingerprintInput) bool {
	for _, file := range input.GitDirtyFiles {
		if strings.HasPrefix(file.WorktreeHash, "oversized:") {
			return false
		}
	}
	return true
}

func stopPolicyGitStatus(repoRoot string) (status string, mode string) {
	snapshot := stopPolicyGitSnapshotFor(repoRoot)
	return snapshot.Status, snapshot.StatusMode
}

func stopPolicyGitSnapshotFor(repoRoot string) stopPolicyGitSnapshot {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(stopPolicyUntrackedModeEnv)))
	switch mode {
	case "all", "no", "normal":
	default:
		mode = "normal"
	}
	raw, err := gitCommandOutput(repoRoot, "status", "--porcelain=v1", "-z", "--untracked-files="+mode)
	status := filterStopPolicyGitStatus(raw)
	if err != nil {
		status = "error:" + err.Error() + "\n" + status
	}
	return stopPolicyGitSnapshot{
		Head:       gitHeadFingerprint(repoRoot),
		Status:     status,
		StatusMode: mode,
		StatusOK:   err == nil,
	}
}

func completionPolicyGitSnapshotFor(repoRoot string) stopPolicyGitSnapshot {
	raw, err := gitCommandOutput(repoRoot, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	status := filterStopPolicyGitStatus(raw)
	if err != nil {
		status = "error:" + err.Error() + "\n" + status
	}
	return stopPolicyGitSnapshot{
		Head: gitHeadFingerprint(repoRoot), Status: status, StatusMode: "all", StatusOK: err == nil,
	}
}

func stopPolicyEvidenceHash(state SessionState) string {
	input := stopPolicyEvidenceInput{
		ReadPaths:      sortedUniqueExact(state.ReadPaths),
		WritePaths:     sortedUniqueExact(state.WritePaths),
		WriteEpochs:    cloneWriteEpochs(state.WriteEpochs),
		Commands:       sortedUnique(state.Commands),
		Claims:         sortedUnique(state.Claims),
		CommandResults: append([]CommandResult{}, state.CommandResults...),
	}
	body, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	return hashBytes(body)
}

// CaptureCompletionState returns a stable, content-bound snapshot without
// evaluating policy or mutating repository/session state. Callers must capture
// again after evaluation and require the fingerprints to match, which closes
// the candidate-mutation race across HEAD, dirty index/worktree entries,
// policy bytes, active-session evidence, and the saved session report.
func CaptureCompletionState(repoRoot string) (CompletionStateSnapshot, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return CompletionStateSnapshot{}, err
	}
	state := SessionState{
		RepoRoot: root, ReadPaths: []string{}, WritePaths: []string{},
		WriteEpochs: map[string]uint64{}, Commands: []string{}, Claims: []string{},
		CommandResults: []CommandResult{},
	}
	sessionID, err := resolveActiveSessionIDResolved(root)
	if err != nil {
		return CompletionStateSnapshot{}, err
	}
	if sessionID != "" {
		state, err = loadSessionStateWithLockResolved(root, sessionID)
		if err != nil {
			return CompletionStateSnapshot{}, fmt.Errorf("load active session %q: %w", sessionID, err)
		}
		state, err = loadCompleteSessionEvidence(root, state)
		if err != nil {
			return CompletionStateSnapshot{}, fmt.Errorf("load active session %q evidence chain: %w", sessionID, err)
		}
	} else if taint, taintErr := loadEvidenceTaint(root); taintErr != nil {
		return CompletionStateSnapshot{}, taintErr
	} else if taint != nil {
		applyEvidenceTaint(&state, *taint)
	}

	gitSnapshot := completionPolicyGitSnapshotFor(root)
	gitAvailable := gitSnapshot.StatusOK || gitMetadataPresent(root)
	gitIndexHash := ""
	if gitAvailable {
		gitIndexHash, err = gitIndexFingerprint(root)
		if err != nil {
			return CompletionStateSnapshot{}, fmt.Errorf("fingerprint Git index: %w", err)
		}
	}
	fingerprintInput := stopPolicyFingerprintInputForSnapshot(root, state, gitSnapshot)
	dirtyFiles := fingerprintInput.GitDirtyFiles
	worktreeBody, err := json.Marshal(struct {
		Status     string         `json:"status"`
		DirtyFiles []gitDirtyFile `json:"dirty_files"`
	}{Status: gitSnapshot.Status, DirtyFiles: dirtyFiles})
	if err != nil {
		return CompletionStateSnapshot{}, fmt.Errorf("marshal completion worktree identity: %w", err)
	}

	reportHash := ""
	reportTrusted := true
	if sessionID != "" {
		if _, statErr := os.Stat(state.ReportPath); statErr == nil {
			_, reportHash, err = readLatestReport(root, sessionID)
			if err != nil {
				return CompletionStateSnapshot{}, fmt.Errorf("read active session report: %w", err)
			}
			reportTrusted = state.StopPolicyReportHash != "" && state.StopPolicyReportHash == reportHash
		} else if errors.Is(statErr, os.ErrNotExist) {
			reportTrusted = state.StopPolicyReportHash == ""
		} else {
			return CompletionStateSnapshot{}, fmt.Errorf("inspect active session report: %w", statErr)
		}
	}
	completionInput := completionStateFingerprintInput{
		Version:                   "completion-state-v3",
		StopPolicyFingerprint:     fingerprintInput,
		SessionReportHash:         reportHash,
		ExpectedSessionReportHash: state.StopPolicyReportHash,
		GitIndexHash:              gitIndexHash,
	}
	completionBody, err := json.Marshal(completionInput)
	if err != nil {
		return CompletionStateSnapshot{}, fmt.Errorf("marshal completion state identity: %w", err)
	}
	inputs := executionInputs(
		filterRepoScopedReadPaths(root, state.ReadPaths),
		append([]string{}, state.WritePaths...), cloneWriteEpochs(state.WriteEpochs),
		append([]string{}, state.Commands...), append([]CommandResult{}, state.CommandResults...),
		append([]string{}, state.Claims...),
	)
	dirtyPaths := []string{}
	worktreeMatchesIndex := false
	if gitSnapshot.StatusOK {
		dirtyPaths = dirtyPathsFromStatus(gitSnapshot.Status)
		worktreeMatchesIndex = gitWorktreeMatchesIndex(gitSnapshot.Status)
	}
	return CompletionStateSnapshot{
		FormatVersion: "3", RepoRoot: root, Fingerprint: hashBytes(completionBody),
		PolicyLockHash: fingerprintInput.PolicyLockHash,
		SessionID:      sessionID, SessionEvidenceHash: stopPolicyEvidenceHash(state),
		SessionReportHash: reportHash, SessionReportTrusted: reportTrusted,
		EvidenceEpoch:    state.EvidenceEpoch,
		EvidenceOverflow: state.EvidenceOverflow, EvidenceOverflowReason: state.EvidenceOverflowReason,
		EvidenceOverflowLimit: state.EvidenceOverflowLimit,
		GitAvailable:          gitAvailable,
		GitHead:               gitSnapshot.Head, GitIndexHash: gitIndexHash, GitStatusMode: gitSnapshot.StatusMode,
		GitStatusOK: gitSnapshot.StatusOK, GitStatus: gitSnapshot.Status,
		WorktreeHash: hashBytes(worktreeBody), WorktreeTrusted: completionDirtyFilesTrusted(dirtyFiles),
		WorktreeMatchesIndex: worktreeMatchesIndex,
		DirtyPaths:           dirtyPaths, Inputs: inputs,
	}, nil
}

func gitMetadataPresent(repoRoot string) bool {
	for dir := filepath.Clean(repoRoot); ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil || !errors.Is(err, os.ErrNotExist) {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
	}
}

func gitIndexFingerprint(repoRoot string) (string, error) {
	entries, err := gitCommandOutput(repoRoot, "ls-files", "-s", "-z")
	if err != nil {
		return "", fmt.Errorf("git ls-files --stage: %w", err)
	}
	return hashBytes([]byte(entries)), nil
}

func gitWorktreeMatchesIndex(status string) bool {
	records := strings.Split(status, "\x00")
	for index := 0; index < len(records); index++ {
		record := records[index]
		if record == "" {
			continue
		}
		if len(record) < 4 || record[2] != ' ' {
			return false
		}
		if record[0] == '?' || record[1] == '?' || record[1] != ' ' {
			return false
		}
		if isRenameOrCopyStatus(record[0], record[1]) && index+1 < len(records) {
			index++
		}
	}
	return true
}

func readLatestReport(repoRoot, sessionID string) (*runtime.CheckReport, string, error) {
	path := sessionReportPath(repoRoot, sessionID)
	body, err := readBoundedFile(path, maxStopReportBytes)
	if err != nil {
		return nil, "", err
	}
	var report runtime.CheckReport
	if err := json.Unmarshal(body, &report); err != nil {
		return nil, "", fmt.Errorf("unmarshal report %s: %w", path, err)
	}
	return &report, hashCheckReport(&report), nil
}

func hashCheckReport(report *runtime.CheckReport) string {
	if report == nil {
		return ""
	}
	body, err := json.Marshal(report)
	if err != nil {
		return ""
	}
	return hashBytes(body)
}

func hashBlockingViolations(violations []runtime.Violation) string {
	body, err := json.Marshal(violations)
	if err != nil {
		return ""
	}
	return hashBytes(body)
}

func hashBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func fileContentHash(path string) string {
	hash, err := hashFileContent(path)
	if err != nil {
		return "error:" + err.Error()
	}
	return hash
}

func gitCommandOutput(repoRoot string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoRoot}, args...)...)
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return string(out), fmt.Errorf("git %s timed out", strings.Join(args, " "))
	}
	return string(out), err
}

// gitHeadFingerprint reads Git's HEAD and referenced object directly. Stop
// already pays for one bounded status process; spawning a second Git process
// just to resolve HEAD added measurable hook latency. Worktree gitdirs and
// packed refs are supported, and any unreadable state is encoded fail-closed
// into the fingerprint instead of being ignored.
func gitHeadFingerprint(repoRoot string) string {
	gitDir, err := resolveGitDir(repoRoot)
	if err != nil {
		return "error:" + err.Error()
	}
	headBody, err := readBoundedFile(filepath.Join(gitDir, "HEAD"), maxGitControlFileBytes)
	if err != nil {
		return "error:" + err.Error()
	}
	head := strings.TrimSpace(string(headBody))
	if !strings.HasPrefix(head, "ref: ") {
		return "detached:" + head
	}
	ref := strings.TrimSpace(strings.TrimPrefix(head, "ref: "))
	cleanRef, err := cleanGitRefPath(ref)
	if err != nil {
		return "error:" + err.Error()
	}
	objectID, found, err := gitRefObjectID(gitDir, cleanRef, ref)
	if err != nil {
		return "error:" + err.Error()
	}
	if found {
		return ref + "\n" + objectID
	}
	// Alternate ref backends such as reftable do not expose loose or packed
	// refs. Pay for rev-parse only on that exceptional path.
	if head, commandErr := gitCommandOutput(repoRoot, "rev-parse", "HEAD"); commandErr == nil {
		return ref + "\n" + strings.TrimSpace(head)
	}
	return ref + "\nmissing"
}

func cleanGitRefPath(ref string) (string, error) {
	cleanRef := filepath.Clean(filepath.FromSlash(ref))
	if filepath.IsAbs(cleanRef) || cleanRef == ".." || strings.HasPrefix(cleanRef, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe HEAD ref")
	}
	return cleanRef, nil
}

func gitRefObjectID(gitDir, cleanRef, ref string) (string, bool, error) {
	commonDir, err := gitCommonDir(gitDir)
	if err != nil {
		return "", false, err
	}
	roots := sortedUnique([]string{gitDir, commonDir})
	if objectID, found, err := readLooseGitRef(roots, cleanRef); found || err != nil {
		return objectID, found, err
	}
	return readPackedGitRef(roots, ref)
}

func readLooseGitRef(roots []string, cleanRef string) (string, bool, error) {
	for _, root := range roots {
		body, err := readBoundedFile(filepath.Join(root, cleanRef), maxGitControlFileBytes)
		if err == nil {
			return strings.TrimSpace(string(body)), true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
	}
	return "", false, nil
}

func readPackedGitRef(roots []string, ref string) (string, bool, error) {
	for _, root := range roots {
		body, err := readBoundedFile(filepath.Join(root, "packed-refs"), maxPackedRefsBytes)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", false, err
		}
		for _, line := range strings.Split(string(body), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == ref {
				return fields[0], true, nil
			}
		}
	}
	return "", false, nil
}

func resolveGitDir(repoRoot string) (string, error) {
	dotGit := filepath.Join(repoRoot, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return "", fmt.Errorf("stat .git: %w", err)
	}
	if info.IsDir() {
		return dotGit, nil
	}
	body, err := readBoundedFile(dotGit, maxGitControlFileBytes)
	if err != nil {
		return "", fmt.Errorf("read .git: %w", err)
	}
	value := strings.TrimSpace(string(body))
	if !strings.HasPrefix(value, "gitdir:") {
		return "", fmt.Errorf("invalid .git file")
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(value, "gitdir:"))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoRoot, gitDir)
	}
	return filepath.Clean(gitDir), nil
}

func gitCommonDir(gitDir string) (string, error) {
	body, err := readBoundedFile(filepath.Join(gitDir, "commondir"), maxGitControlFileBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return gitDir, nil
		}
		return "", fmt.Errorf("read git commondir: %w", err)
	}
	commonDir := strings.TrimSpace(string(body))
	if commonDir == "" {
		return "", fmt.Errorf("git commondir is empty")
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	return filepath.Clean(commonDir), nil
}

func gitDirtyFiles(repoRoot string, status string) []gitDirtyFile {
	paths := dirtyPathsFromStatus(status)
	indexEntries := gitIndexEntries(repoRoot, paths)
	files := make([]gitDirtyFile, 0, len(paths))
	for _, path := range paths {
		indexEntry := indexEntries[path]
		files = append(files, gitDirtyFile{
			Path:         path,
			WorktreeHash: worktreePathHash(repoRoot, path, indexEntry),
			IndexEntry:   indexEntry,
		})
	}
	return files
}

func completionDirtyFilesTrusted(files []gitDirtyFile) bool {
	for _, file := range files {
		if strings.HasPrefix(file.IndexEntry, "error:") {
			return false
		}
		hash := file.WorktreeHash
		switch {
		case hash == "missing":
			continue
		case strings.HasPrefix(hash, "symlink:"):
			hash = strings.TrimPrefix(hash, "symlink:")
		case strings.HasPrefix(hash, "submodule:"):
			hash = strings.TrimPrefix(hash, "submodule:")
		}
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != sha256.Size {
			return false
		}
	}
	return true
}

// dirtyPathsFromStatus parses `git status --porcelain=v1 -z` records.
// Each record is "XY <path>"; rename/copy records are followed by the
// origin path as a separate NUL field WITHOUT an XY prefix, and that
// origin is dirty too. Path bytes are verbatim (-z never quotes), so
// nothing is trimmed: leading/trailing spaces are part of the name.
func dirtyPathsFromStatus(status string) []string {
	records := strings.Split(status, "\x00")
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(records))
	add := func(path string) {
		path = filepath.ToSlash(path)
		if path == "" || stopPolicyRuntimeStateRecord(path) {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for i := 0; i < len(records); i++ {
		record := records[i]
		if record == "" {
			continue
		}
		if len(record) >= 4 && record[2] == ' ' {
			add(record[3:])
			if isRenameOrCopyStatus(record[0], record[1]) && i+1 < len(records) {
				i++
				add(records[i])
			}
			continue
		}
		// Defensive fallback for a record that does not match the
		// XY-prefix shape; keep its bytes verbatim.
		add(record)
	}
	sort.Strings(paths)
	return paths
}

func isRenameOrCopyStatus(x, y byte) bool {
	return x == 'R' || x == 'C' || y == 'R' || y == 'C'
}

// gitIndexBatchBytes bounds the path arguments passed to a single
// `git ls-files` invocation. Path arguments are appended to argv, and a
// large session can accumulate thousands of multi-kilobyte paths whose
// combined size exceeds the platform ARG_MAX (about 2 MiB on Linux), which
// would fail the spawn with E2BIG. Batching by total argument bytes keeps
// every invocation well under that limit while merging all index entries.
const gitIndexBatchBytes = 128 * 1024

func gitIndexEntries(repoRoot string, paths []string) map[string]string {
	entries := map[string]string{}
	if len(paths) == 0 {
		return entries
	}
	batch := []string{}
	batchBytes := 0
	flush := func() {
		if len(batch) == 0 {
			return
		}
		mergeGitIndexEntries(entries, repoRoot, batch)
		batch = nil
		batchBytes = 0
	}
	for _, path := range paths {
		if len(batch) > 0 && batchBytes+len(path) > gitIndexBatchBytes {
			flush()
		}
		batch = append(batch, path)
		batchBytes += len(path)
	}
	flush()
	return entries
}

func mergeGitIndexEntries(entries map[string]string, repoRoot string, paths []string) {
	args := append([]string{"ls-files", "-s", "-z", "--"}, paths...)
	out, err := gitCommandOutput(repoRoot, args...)
	if err != nil {
		for _, path := range paths {
			if _, seen := entries[path]; !seen {
				entries[path] = "error:" + err.Error()
			}
		}
		return
	}
	for _, record := range strings.Split(out, "\x00") {
		if record == "" {
			continue
		}
		tab := strings.IndexByte(record, '\t')
		if tab < 0 || tab == len(record)-1 {
			continue
		}
		path := filepath.ToSlash(record[tab+1:])
		entries[path] = record[:tab]
	}
}

func worktreePathHash(repoRoot string, path, indexEntry string) string {
	fullPath := filepath.Join(repoRoot, filepath.FromSlash(path))
	info, err := os.Lstat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing"
		}
		return "error:" + err.Error()
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(fullPath)
		if err != nil {
			return "symlink-error:" + err.Error()
		}
		return "symlink:" + hashBytes([]byte(target))
	}
	if info.IsDir() {
		if strings.HasPrefix(indexEntry, "160000 ") {
			return submoduleWorktreeHash(fullPath)
		}
		return "dir"
	}
	if !info.Mode().IsRegular() {
		return "mode:" + info.Mode().String()
	}
	hash, err := hashFileContent(fullPath)
	if err != nil {
		return "error:" + err.Error()
	}
	return hash
}

func submoduleWorktreeHash(root string) string {
	head, err := gitCommandOutput(root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "submodule-error:" + err.Error()
	}
	indexHash, err := gitIndexFingerprint(root)
	if err != nil {
		return "submodule-error:" + err.Error()
	}
	rawStatus, err := gitCommandOutput(root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return "submodule-error:" + err.Error()
	}
	status := filterStopPolicyGitStatus(rawStatus)
	dirtyFiles := gitDirtyFiles(root, status)
	if !completionDirtyFilesTrusted(dirtyFiles) {
		return "submodule-error:dirty content could not be bound safely"
	}
	body, err := json.Marshal(struct {
		Head       string         `json:"head"`
		IndexHash  string         `json:"index_hash"`
		Status     string         `json:"status"`
		DirtyFiles []gitDirtyFile `json:"dirty_files"`
	}{
		Head: strings.TrimSpace(head), IndexHash: indexHash, Status: status,
		DirtyFiles: dirtyFiles,
	})
	if err != nil {
		return "submodule-error:" + err.Error()
	}
	return "submodule:" + hashBytes(body)
}

func readBoundedFile(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	return body, nil
}

// stopPolicyContentHashBound caps the bytes a single file contributes to the
// stop-policy fingerprint. Hashing multi-gigabyte dirty files fully would
// stall the stop hook until the platform timeout, degrading toward fail-open
// wherever failure-allow applies. Files above the bound are bound by size
// and modification time instead. That metadata identifies the input for
// diagnostics, but any oversized dirty file makes the stop-policy report
// cache ineligible and the completion candidate untrusted. This preserves
// bounded hook latency without ever reusing a report for content that was not
// hashed exactly. The policy lockfile stays far below the bound, so its hash
// is always content-exact.
const stopPolicyContentHashBound = 64 * 1024 * 1024

func hashFileContent(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	info, statErr := file.Stat()
	if statErr != nil {
		closeErr := file.Close()
		return "", errors.Join(statErr, closeErr)
	}
	if info.Size() > stopPolicyContentHashBound {
		closeErr := file.Close()
		if closeErr != nil {
			return "", closeErr
		}
		return oversizedFileFingerprint(info), nil
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, io.LimitReader(file, stopPolicyContentHashBound))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func oversizedFileFingerprint(info os.FileInfo) string {
	return fmt.Sprintf("oversized:%d:%s", info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano))
}

func filterStopPolicyGitStatus(raw string) string {
	parts := strings.Split(raw, "\x00")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || stopPolicyRuntimeStateRecord(statusRecordPath(part)) {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\x00") + "\x00"
}

// statusRecordPath strips the two-character "XY " status prefix from a
// porcelain -z record so the path can be matched by prefix. Records without
// the prefix (rename origins) are returned verbatim.
func statusRecordPath(record string) string {
	if len(record) >= 3 && record[2] == ' ' {
		return record[3:]
	}
	return record
}

// stopPolicyRuntimeStateRecord reports whether a repo-relative path is
// Reconc-owned runtime state that must not influence the stop-policy
// fingerprint. Matching is prefix-based on the path: a substring match would
// wrongly drop user files such as "src/x.reconc/run/data.txt" whose name
// merely contains a runtime marker, leaving the stop cache stale when they
// change.
func stopPolicyRuntimeStateRecord(path string) bool {
	return strings.HasPrefix(filepath.ToSlash(path), ".reconc/")
}

func recordStopBlockAndRepeated(repoRoot, sessionID string, violations []runtime.Violation) (bool, string) {
	violationHash := hashBlockingViolations(violations)
	if violationHash == "" {
		return false, ""
	}
	repeated := false
	_, err := mutateSessionStateResolved(repoRoot, sessionID, func(state SessionState) SessionState {
		repeated = state.LastStopBlockViolationHash == violationHash
		state.LastStopBlockViolationHash = violationHash
		return state
	})
	return err == nil && repeated, "RB-" + violationHash[:12]
}

func reportPathForStop(repoRoot, sessionID string) string {
	path := sessionReportPath(repoRoot, sessionID)
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return path
}
