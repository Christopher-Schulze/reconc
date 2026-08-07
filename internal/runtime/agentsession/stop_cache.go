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

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/tasklifecycle"
)

const (
	stopPolicyFingerprintVersion = "stop-policy-report-v8"
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
	PolicySourceDigest string            `json:"policy_source_digest"`
	PolicySourceCount  int               `json:"policy_source_count"`
	TaskStateHash      string            `json:"task_state_hash"`
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
	// PolicyInputs binds the repository paths the compiled policy names but
	// Git does not report, most importantly gitignored require_evidence and
	// require_fresh_file targets.
	PolicyInputs []policyInputIdentity `json:"policy_inputs,omitempty"`
}

// policyInputIdentity is one policy-named repository path and its exact
// content identity at fingerprint time.
type policyInputIdentity struct {
	Path     string `json:"path"`
	Identity string `json:"identity"`
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
	Report        *runtime.CheckReport
	GitSnapshot   stopPolicyGitSnapshot
	TaskSnapshot  stopTaskSnapshot
	GenerationHit bool
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
	return runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(repoRoot, state, evaluator, nil, nil)
}

func runStopPolicyCheckWithSnapshotWithEvaluatorAndCache(
	repoRoot string,
	state SessionState,
	evaluator *runtime.Evaluator,
	cache *StopDecisionCache,
	taskSnapshot *stopTaskSnapshot,
) (stopPolicyCheckResult, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return stopPolicyCheckResult{}, err
	}
	if taskSnapshot == nil {
		captured, captureErr := captureStopTaskSnapshot(root)
		if captureErr != nil {
			return stopPolicyCheckResult{}, fmt.Errorf("capture TASK snapshot: %w", captureErr)
		}
		taskSnapshot = &captured
	}
	state, err = loadCompleteSessionEvidence(root, state)
	if err != nil {
		return stopPolicyCheckResult{}, fmt.Errorf("load evidence chain: %w", err)
	}
	return withStopPolicyReportLock(root, state.SessionID, func() (stopPolicyCheckResult, error) {
		current, loadErr := loadSessionStateWithLockResolved(root, state.SessionID)
		if loadErr != nil {
			return stopPolicyCheckResult{}, loadErr
		}
		current, loadErr = loadCompleteSessionEvidence(root, current)
		if loadErr != nil {
			return stopPolicyCheckResult{}, fmt.Errorf("reload evidence chain under Stop cache lock: %w", loadErr)
		}
		return runStopPolicyCheckLocked(root, current, evaluator, cache, *taskSnapshot)
	})
}

func runStopPolicyCheckLocked(
	repoRoot string,
	state SessionState,
	evaluator *runtime.Evaluator,
	cache *StopDecisionCache,
	taskSnapshot stopTaskSnapshot,
) (stopPolicyCheckResult, error) {
	gitSnapshot := stopPolicyGitSnapshotFor(repoRoot)
	if report, ok := cache.readStableReport(repoRoot, state, taskSnapshot, gitSnapshot); ok {
		return stopPolicyCheckResult{
			Report: report, GitSnapshot: gitSnapshot, TaskSnapshot: taskSnapshot, GenerationHit: true,
		}, nil
	}
	fingerprintInput := stopPolicyFingerprintInputForSnapshotWithGeneration(repoRoot, state, gitSnapshot, taskSnapshot, stopGenerationCapture{})
	cacheable := stopPolicyFingerprintCacheable(fingerprintInput)
	fingerprint := hashStopPolicyFingerprintInput(fingerprintInput)
	if cacheable && fingerprint != "" && state.StopPolicyFingerprint == fingerprint &&
		state.StopPolicyReportHash != "" && !stopPolicyReportExpired(state.StopPolicyExpiresAt) {
		report, reportHash, err := readLatestReport(repoRoot, state.SessionID)
		if err == nil && reportHash == state.StopPolicyReportHash {
			storeStopGenerationIfWorthwhile(cache, repoRoot, state, gitSnapshot, fingerprintInput)
			return stopPolicyCheckResult{Report: report, GitSnapshot: gitSnapshot, TaskSnapshot: taskSnapshot}, nil
		}
	}

	scopedWritePaths := stopScopeWritePathsToUncommitted(repoRoot, state.WritePaths, stopPolicyGitSnapshot{
		Head:       gitSnapshot.Head,
		Status:     gitSnapshot.Status,
		StatusMode: gitSnapshot.StatusMode,
		StatusOK:   gitSnapshot.StatusOK,
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
		expiresAt := stopPolicyReportExpiry(repoRoot, scanStopPolicyLockfile(repoRoot, sortedUniqueExact(state.WritePaths)).FreshFiles)
		updated, err := mutateSessionStateResolved(repoRoot, state.SessionID, func(current SessionState) SessionState {
			if stopPolicyEvidenceHash(current) == initialEvidenceHash {
				current.StopPolicyFingerprint = reportFingerprint
				current.StopPolicyEvidenceHash = initialEvidenceHash
				current.StopPolicyReportHash = reportHash
				current.StopPolicyExpiresAt = expiresAt
				current.ReportPath = sessionReportPath(repoRoot, state.SessionID)
			}
			return current
		})
		if err != nil {
			return stopPolicyCheckResult{}, fmt.Errorf("persist stop-policy cache metadata: %w", err)
		}
		state = updated
	}
	storeStopGenerationIfWorthwhile(cache, repoRoot, state, gitSnapshot, fingerprintInput)
	return stopPolicyCheckResult{Report: report, GitSnapshot: gitSnapshot, TaskSnapshot: taskSnapshot}, nil
}

func storeStopGenerationIfWorthwhile(
	cache *StopDecisionCache,
	root string,
	state SessionState,
	gitSnapshot stopPolicyGitSnapshot,
	fingerprintInput stopPolicyFingerprintInput,
) {
	if cache == nil || !stopPolicyFingerprintCacheable(fingerprintInput) || !stopGenerationWorthwhile(root, fingerprintInput.GitDirtyFiles) {
		cache.invalidate(root, state.SessionID)
		return
	}
	generation, ok := captureStopRepositoryGenerationWithIdentity(
		root,
		gitSnapshot,
		fingerprintInput.PolicySourceDigest,
		fingerprintInput.PolicySourceCount,
		fingerprintInput.TaskStateHash,
	)
	if !ok {
		cache.invalidate(root, state.SessionID)
		return
	}
	cache.store(root, state, generation.Fingerprint)
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
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return nil, false
	}
	taskSnapshot, err := captureStopTaskSnapshot(root)
	if err != nil {
		return nil, false
	}
	return cachedCleanStopPolicyReportForEvidenceAndSnapshot(
		root, state, evidenceHash, taskSnapshot, stopPolicyGitSnapshotFor(root),
	)
}

func cachedCleanStopPolicyReportForEvidenceAndSnapshot(
	repoRoot string,
	state SessionState,
	evidenceHash string,
	taskSnapshot stopTaskSnapshot,
	gitSnapshot stopPolicyGitSnapshot,
) (*runtime.CheckReport, bool) {
	if evidenceHash == "" || state.StopPolicyEvidenceHash != evidenceHash || state.StopPolicyReportHash == "" {
		return nil, false
	}
	fingerprintInput := stopPolicyFingerprintInputForSnapshotWithGeneration(
		repoRoot, state, gitSnapshot, taskSnapshot, stopGenerationCapture{},
	)
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

func cachedCleanStopPolicyReportForEvidenceWithCache(
	repoRoot string,
	state SessionState,
	evidenceHash string,
	cache *StopDecisionCache,
	taskSnapshot stopTaskSnapshot,
) (*runtime.CheckReport, bool) {
	if evidenceHash == "" || state.StopPolicyEvidenceHash != evidenceHash || state.StopPolicyReportHash == "" {
		return nil, false
	}
	root := repoRoot
	if state.RepoRoot != "" {
		root = state.RepoRoot
	}
	gitSnapshot := stopPolicyGitSnapshotFor(root)
	entry, generationAvailable := cache.entry(root, state.SessionID)
	if generationAvailable && entry.evidenceHash == evidenceHash &&
		entry.fingerprint == state.StopPolicyFingerprint && entry.reportHash == state.StopPolicyReportHash {
		report, ok := cache.readStableReport(root, state, taskSnapshot, gitSnapshot)
		if ok && len(blockingViolations(report)) == 0 {
			return report, true
		}
	}
	return cachedCleanStopPolicyReportForEvidenceAndSnapshot(root, state, evidenceHash, taskSnapshot, gitSnapshot)
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
	taskSnapshot, err := captureStopTaskSnapshot(root)
	if err != nil {
		return stopPolicyFingerprintInput{
			Version: stopPolicyFingerprintVersion, RepoRoot: root, SessionID: state.SessionID,
			PolicyLockHash:     fileContentHash(filepath.Join(root, ".reconc", "policy.lock.json")),
			PolicySourceDigest: "error:" + err.Error(), TaskStateHash: "error:" + err.Error(),
			GitHead: gitSnapshot.Head, GitStatus: gitSnapshot.Status,
			GitStatusMode: gitSnapshot.StatusMode, GitStatusOK: gitSnapshot.StatusOK,
		}
	}
	return stopPolicyFingerprintInputForSnapshotWithGeneration(root, state, gitSnapshot, taskSnapshot, stopGenerationCapture{})
}

func stopPolicyFingerprintInputForSnapshotWithGeneration(
	root string,
	state SessionState,
	gitSnapshot stopPolicyGitSnapshot,
	taskSnapshot stopTaskSnapshot,
	generation stopGenerationCapture,
) stopPolicyFingerprintInput {
	policyDigest := generation.PolicySourceDigest
	policyCount := generation.PolicySourceCount
	if policyDigest == "" {
		var err error
		policyDigest, policyCount, err = stopPolicySourceIdentity(root)
		if err != nil {
			policyDigest = "error:" + err.Error()
		}
	}
	taskHash := generation.TaskStateHash
	if taskHash == "" {
		taskHash = stopTaskSnapshotHash(taskSnapshot)
	}
	dirtyFiles := []gitDirtyFile{}
	if gitSnapshot.StatusOK {
		dirtyFiles = gitDirtyFiles(root, gitSnapshot.Status)
	}
	policyScan := scanStopPolicyLockfile(root, sortedUniqueExact(state.WritePaths))
	return stopPolicyFingerprintInput{
		Version:            stopPolicyFingerprintVersion,
		RepoRoot:           root,
		SessionID:          state.SessionID,
		PolicyLockHash:     fileContentHash(filepath.Join(root, ".reconc", "policy.lock.json")),
		PolicySourceDigest: policyDigest,
		PolicySourceCount:  policyCount,
		TaskStateHash:      taskHash,
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
		PolicyInputs:       stopPolicyInputIdentities(root, policyScan.Paths, dirtyFiles),
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

// stopPolicyWritePaths is the exact set require_script rules match their
// when_paths against, so a rule this Stop cannot trigger cannot affect the
// report it would reuse.
func stopPolicyWritePaths(input stopPolicyFingerprintInput) []string {
	return input.WritePaths
}

func stopPolicyFingerprintCacheable(input stopPolicyFingerprintInput) bool {
	if !(input.GitStatusOK &&
		!strings.HasPrefix(input.GitHead, "error:") &&
		!strings.HasPrefix(input.PolicyLockHash, "error:") &&
		!strings.HasPrefix(input.PolicySourceDigest, "error:") &&
		!strings.HasPrefix(input.TaskStateHash, "error:") &&
		completionDirtyFilesTrusted(input.GitDirtyFiles)) {
		return false
	}
	// A policy path the compiler cannot name statically cannot be bound into
	// the fingerprint, so the report it produced cannot be revalidated.
	return scanStopPolicyLockfile(input.RepoRoot, stopPolicyWritePaths(input)).Cacheable
}

// stopPolicyLockScan is the bounded static view of the compiled policy that
// Stop caching needs: which repository paths the rules read, and which of them
// can turn a clean report stale from wall-clock time alone.
//
// It binds what the policy names. A require_script body is opaque, so it must
// declare the files it reads through `cache_inputs`; a script that declares
// none keeps its plan off the warm path entirely.
type stopPolicyLockScan struct {
	// Cacheable is false when the lock cannot be read or decoded, or when a
	// policy path is template-generated and therefore not enumerable here.
	Cacheable bool
	// Paths are the repository-relative paths the policy names, sorted and
	// deduplicated.
	Paths []string
	// FreshFiles are the age-bounded requirements, used to give a stored
	// report the expiry its inputs imply.
	FreshFiles []stopPolicyFreshFile
}

type stopPolicyFreshFile struct {
	Path        string
	MaxAgeHours int
}

// scanStopPolicyLockfile decodes the compiled policy instead of scanning it for
// tokens: a rule message that quotes a kind must not change caching, and a
// check nested in an all_of / any_of / not rule must.
func scanStopPolicyLockfile(repoRoot string, writePaths []string) stopPolicyLockScan {
	if repoRoot == "" {
		// Fingerprint unit tests exercise cacheability without a repo root;
		// production Stop always supplies a resolved root before caching.
		return stopPolicyLockScan{Cacheable: true}
	}
	body, err := boundedio.ReadFile(filepath.Join(repoRoot, ".reconc", "policy.lock.json"), stopPolicyLockfileScanBound)
	if err != nil {
		// Unreadable lock already fails evaluation; treat uncertainty as
		// non-cacheable so we never reuse a report we cannot revalidate.
		return stopPolicyLockScan{}
	}
	var lock struct {
		Rules []stopPolicyLockRule `json:"rules"`
	}
	if err := json.Unmarshal(body, &lock); err != nil {
		// A lock we cannot decode is a lock we cannot reason about.
		return stopPolicyLockScan{}
	}
	scan := stopPolicyLockScan{Cacheable: true}
	paths := map[string]struct{}{}
	collect := func(path string, maxAgeHours int, fresh bool) {
		if path == "" {
			return
		}
		if runtime.HasTemplateVars(path) {
			scan.Cacheable = false
			return
		}
		paths[path] = struct{}{}
		if fresh && maxAgeHours > 0 {
			scan.FreshFiles = append(scan.FreshFiles, stopPolicyFreshFile{Path: path, MaxAgeHours: maxAgeHours})
		}
	}
	undeclaredScript := false
	for _, rule := range lock.Rules {
		// A require_script rule matches its when_paths against the session's
		// write paths. One this Stop cannot trigger runs no script, so it can
		// neither contribute to the report nor invalidate its reuse.
		if !stopPolicyRuleReachable(rule.WhenPaths, writePaths) {
			continue
		}
		rule.collectInto(collect, &undeclaredScript)
		for _, check := range rule.Checks {
			// Composite sub-checks inherit the parent rule's trigger surface.
			check.collectInto(collect, &undeclaredScript)
		}
	}
	if undeclaredScript {
		scan.Cacheable = false
	}
	// Paths stay bound even when the plan is not cacheable. The same
	// fingerprint identifies the completion candidate, and a candidate must not
	// survive a change to a policy-named input just because some other rule in
	// the same policy keeps the plan off the Stop warm path.
	scan.Paths = sortedKeys(paths)
	sort.Slice(scan.FreshFiles, func(i, j int) bool {
		if scan.FreshFiles[i].Path == scan.FreshFiles[j].Path {
			return scan.FreshFiles[i].MaxAgeHours < scan.FreshFiles[j].MaxAgeHours
		}
		return scan.FreshFiles[i].Path < scan.FreshFiles[j].Path
	})
	return scan
}

// stopPolicyLockRule decodes both the list form used by rules and the inline
// form used by composite checks, so one type covers every place a policy names
// a repository path.
type stopPolicyLockRule struct {
	Kind          string               `json:"kind"`
	Path          string               `json:"path"`
	File          string               `json:"file"`
	Script        string               `json:"script"`
	WhenPaths     []string             `json:"when_paths"`
	CacheInputs   []string             `json:"cache_inputs"`
	MaxAgeHours   int                  `json:"max_age_hours"`
	RequiredFiles []stopPolicyLockFile `json:"required_files"`
	Evidence      []stopPolicyLockFile `json:"evidence"`
	Checks        []stopPolicyLockRule `json:"checks"`
}

type stopPolicyLockFile struct {
	Path        string `json:"path"`
	File        string `json:"file"`
	MaxAgeHours int    `json:"max_age_hours"`
}

func (r stopPolicyLockRule) collectInto(collect func(path string, maxAgeHours int, fresh bool), undeclaredScript *bool) {
	fresh := r.Kind == string(policy.KindRequireFreshFile)
	collect(r.Path, r.MaxAgeHours, fresh)
	collect(r.File, 0, false)
	// A require_script target is an input by definition. Git binds it only
	// while it is tracked: a gitignored check script could otherwise be
	// rewritten and the stored report would still be served.
	collect(r.Script, 0, false)
	if r.Kind == string(policy.KindRequireScript) {
		// The script body itself is opaque. Only the inputs its author
		// declares can be bound, so an undeclared script plan is not a
		// function of the fingerprint and must not reuse a report.
		if len(r.CacheInputs) == 0 {
			*undeclaredScript = true
		}
		for _, input := range r.CacheInputs {
			collect(input, 0, false)
		}
	}
	for _, required := range r.RequiredFiles {
		collect(required.Path, required.MaxAgeHours, fresh)
	}
	for _, evidence := range r.Evidence {
		collect(evidence.File, 0, false)
		collect(evidence.Path, 0, false)
	}
}

// stopPolicyInputIdentities binds every repository path the policy names.
//
// Git alone does not cover them: `git status` never lists ignored files, so a
// gitignored evidence file can be deleted or rewritten without moving any
// fingerprint field. Paths already carried by the dirty set are skipped
// because their exact content identity is bound there.
func stopPolicyInputIdentities(repoRoot string, paths []string, dirty []gitDirtyFile) []policyInputIdentity {
	if len(paths) == 0 {
		return nil
	}
	bound := make(map[string]struct{}, len(dirty))
	for _, file := range dirty {
		bound[file.Path] = struct{}{}
	}
	identities := make([]policyInputIdentity, 0, len(paths))
	for _, path := range paths {
		if _, ok := bound[path]; ok {
			continue
		}
		identities = append(identities, policyInputIdentity{
			Path:     path,
			Identity: worktreePathHash(repoRoot, path, ""),
		})
	}
	if len(identities) == 0 {
		return nil
	}
	return identities
}

// stopPolicyReportExpiry is the instant a stored report stops describing its
// inputs because an age requirement elapses. Zero means the report has no
// wall-clock dependence. A missing required file is already a violation, and
// its later appearance moves a bound identity, so it needs no expiry.
func stopPolicyReportExpiry(repoRoot string, freshFiles []stopPolicyFreshFile) int64 {
	expiry := int64(0)
	for _, fresh := range freshFiles {
		info, err := os.Lstat(filepath.Join(repoRoot, filepath.FromSlash(fresh.Path)))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		candidate := info.ModTime().Add(time.Duration(fresh.MaxAgeHours) * time.Hour).Unix()
		if expiry == 0 || candidate < expiry {
			expiry = candidate
		}
	}
	return expiry
}

// stopPolicyReportExpired reports whether a stored report has outlived the age
// requirement that produced it. Zero means the policy has no wall-clock
// dependence, so the report never expires on time alone.
func stopPolicyReportExpired(expiresAt int64) bool {
	return expiresAt != 0 && time.Now().Unix() >= expiresAt
}

// stopPolicyRuleReachable reports whether any recorded write path can trigger a
// rule. It fails toward reachable: a rule without when_paths, a templated
// pattern, or a match error is treated as triggerable so uncertainty never
// admits a report the rule might have changed.
func stopPolicyRuleReachable(whenPaths, writePaths []string) bool {
	if len(whenPaths) == 0 {
		return true
	}
	for _, pattern := range whenPaths {
		if runtime.HasTemplateVars(pattern) {
			return true
		}
	}
	_, _, matched, err := runtime.MatchAnyPath(whenPaths, writePaths)
	if err != nil {
		return true
	}
	return matched
}

func sortedKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
	taskSnapshot, err := captureStopTaskSnapshot(root)
	if err != nil {
		taskSnapshot = stopTaskSnapshot{State: tasklifecycle.RunState{
			Disposition: tasklifecycle.RunInvalid,
			Blocker:     err.Error(),
		}}
	}
	policyDigest, policyCount, err := stopPolicySourceIdentity(root)
	if err != nil {
		policyDigest = "error:" + err.Error()
		policyCount = 0
	}
	fingerprintInput := stopPolicyFingerprintInputForSnapshotWithGeneration(root, state, gitSnapshot, taskSnapshot, stopGenerationCapture{
		PolicySourceDigest: policyDigest,
		PolicySourceCount:  policyCount,
		TaskStateHash:      stopTaskSnapshotHash(taskSnapshot),
	})
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
	// Rooting and escape are decided before cleaning and without asking the
	// running platform: `filepath.IsAbs` calls a POSIX root relative on Windows,
	// which would resolve `/etc/passwd` against the git directory there and
	// refuse it everywhere else.
	if pathidentity.Rooted(ref) || pathidentity.EscapesLexically(ref) {
		return "", fmt.Errorf("unsafe HEAD ref")
	}
	cleanRef := filepath.Clean(filepath.FromSlash(ref))
	if cleanRef == "." || pathidentity.Rooted(cleanRef) || pathidentity.EscapesLexically(cleanRef) {
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
		case strings.HasPrefix(hash, "dir:"):
			hash = strings.TrimPrefix(hash, "dir:")
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
		return stopDirectoryContentHash(fullPath)
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

// stopPolicyLockfileScanBound matches the runtime's own lockfile read bound so
// a policy the evaluator would still load can never be silently treated as
// unreadable by the cache-eligibility scan.
const stopPolicyLockfileScanBound int64 = 16 << 20

func hashFileContent(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	return hashFileContentExpected(path, info)
}

func hashFileContentExpected(path string, expected os.FileInfo) (string, error) {
	if !expected.Mode().IsRegular() {
		return "", fmt.Errorf("hash content: %s is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	info, statErr := file.Stat()
	if statErr != nil {
		closeErr := file.Close()
		return "", errors.Join(statErr, closeErr)
	}
	if !info.Mode().IsRegular() || !os.SameFile(expected, info) {
		return "", errors.Join(fmt.Errorf("hash content: %s changed before open", path), file.Close())
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
	after, statErr := file.Stat()
	closeErr := file.Close()
	if err := errors.Join(readErr, statErr, closeErr); err != nil {
		return "", err
	}
	if !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return "", fmt.Errorf("hash content: %s changed while reading", path)
	}
	beforeGeneration, beforeOK := stopPathMetadataGeneration(path, info)
	afterGeneration, afterOK := stopPathMetadataGeneration(path, after)
	if beforeOK != afterOK || (beforeOK && beforeGeneration != afterGeneration) {
		return "", fmt.Errorf("hash content: %s changed while reading", path)
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
