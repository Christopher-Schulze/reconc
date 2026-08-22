package agentsession

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/runtime"
)

const (
	stopPolicyFingerprintVersion = "stop-policy-report-v11"
	stopPolicyUntrackedModeEnv   = "RECONC_STOP_FINGERPRINT_UNTRACKED"
	maxStopReportBytes           = 32 << 20
	maxGitControlFileBytes       = 1 << 20
	maxPackedRefsBytes           = 64 << 20
	maxStopGitOutputBytes        = 32 << 20
	maxStopPolicyStabilityRuns   = 3
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

// policyInputIdentity is one policy-named repository path and its declared
// cache identity at fingerprint time. Trusted is explicit because bounded
// hashing can produce diagnostic identities that must never authorize report
// reuse. Opaque scripts remain responsible for the documented cache_inputs
// determinism contract.
type policyInputIdentity struct {
	Path     string `json:"path"`
	Identity string `json:"identity"`
	Trusted  bool   `json:"trusted"`
}

type stopPolicyEvidenceInput struct {
	ReadPaths      []string          `json:"read_paths"`
	WritePaths     []string          `json:"write_paths"`
	WriteEpochs    map[string]uint64 `json:"write_epochs"`
	Commands       []string          `json:"commands"`
	Claims         []string          `json:"claims"`
	CommandResults []CommandResult   `json:"command_results"`
}

type stopPolicyEvidenceRevisionInput struct {
	Evidence             stopPolicyEvidenceInput `json:"evidence"`
	EvidenceEpoch        uint64                  `json:"evidence_epoch"`
	EvidenceSegmentCount uint64                  `json:"evidence_segment_count"`
	EvidenceSegmentHash  string                  `json:"evidence_segment_hash"`
	EvidenceOverflow     bool                    `json:"evidence_overflow"`
	MaterialEvents       uint64                  `json:"material_events"`
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
	CompletionInputs          runtime.ExecutionInputs    `json:"completion_inputs"`
	CompletionPolicyInputs    []policyInputIdentity      `json:"completion_policy_inputs,omitempty"`
	CompletionFreshness       []completionFreshnessState `json:"completion_freshness,omitempty"`
	AssuranceInputIdentity    string                     `json:"assurance_input_identity,omitempty"`
	SessionReportHash         string                     `json:"session_report_hash"`
	ExpectedSessionReportHash string                     `json:"expected_session_report_hash"`
	GitIndexHash              string                     `json:"git_index_hash,omitempty"`
}

type completionFreshnessState struct {
	Path        string `json:"path"`
	MaxAgeHours int    `json:"max_age_hours"`
	Expired     bool   `json:"expired"`
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
	return runStopPolicyCheckLockedAttempt(repoRoot, state, evaluator, cache, taskSnapshot, 1)
}

func runStopPolicyCheckLockedAttempt(
	repoRoot string,
	state SessionState,
	evaluator *runtime.Evaluator,
	cache *StopDecisionCache,
	taskSnapshot stopTaskSnapshot,
	stabilityRun int,
) (stopPolicyCheckResult, error) {
	scanCache := &stopPolicyScanCache{}
	currentState, initialEvidenceRevision, err := loadCurrentStopPolicyStateWithRevision(repoRoot, state.SessionID)
	if err != nil {
		return stopPolicyCheckResult{}, err
	}
	state = currentState
	gitSnapshot := stopPolicyGitSnapshotFor(repoRoot)
	beforeSnapshot := captureStopPolicyAttemptSnapshot(
		repoRoot, state, initialEvidenceRevision, taskSnapshot, gitSnapshot, scanCache,
	)
	if report, ok := cache.readStableReportWithSnapshot(repoRoot, state, taskSnapshot, gitSnapshot, scanCache, &beforeSnapshot); ok {
		current, loadErr := loadCurrentStopPolicyState(repoRoot, state.SessionID)
		if loadErr != nil {
			return stopPolicyCheckResult{}, loadErr
		}
		currentTask, captureErr := captureStopTaskSnapshot(repoRoot)
		if captureErr != nil {
			return stopPolicyCheckResult{}, fmt.Errorf("recapture TASK snapshot for generation cache: %w", captureErr)
		}
		currentGit := stopPolicyGitSnapshotFor(repoRoot)
		currentSnapshot := captureStopPolicyAttemptSnapshot(
			repoRoot, current, "", currentTask, currentGit, scanCache,
		)
		currentGeneration, generationOK := captureStopRepositoryGenerationWithIdentityAndScan(
			repoRoot, currentGit, currentSnapshot.PolicyDigest, currentSnapshot.PolicyCount,
			stopTaskSnapshotHash(currentTask), current.WritePaths, scanCache,
		)
		entry, entryOK := cache.entry(repoRoot, state.SessionID)
		if stopPolicyCacheBindingMatches(current, state) && generationOK && entryOK &&
			entry.generation == currentGeneration.Fingerprint {
			return stopPolicyCheckResult{
				Report: report, GitSnapshot: currentGit, TaskSnapshot: currentTask, GenerationHit: true,
			}, nil
		}
		cache.invalidate(repoRoot, state.SessionID)
		state = current
		taskSnapshot = currentTask
		gitSnapshot = currentGit
	}
	fingerprintInput := stopPolicyFingerprintInputForSnapshotWithScan(repoRoot, state, gitSnapshot, taskSnapshot, beforeSnapshot.generationCapture(), scanCache)
	cacheable := stopPolicyFingerprintCacheableWithScan(fingerprintInput, scanCache)
	fingerprint := hashStopPolicyFingerprintInput(fingerprintInput)
	if cacheable && fingerprint != "" && state.StopPolicyFingerprint == fingerprint &&
		state.StopPolicyReportHash != "" && !stopPolicyReportExpired(state.StopPolicyExpiresAt) {
		report, reportHash, err := readLatestReport(repoRoot, state.SessionID)
		if err == nil && reportHash == state.StopPolicyReportHash {
			current, loadErr := loadCurrentStopPolicyState(repoRoot, state.SessionID)
			if loadErr != nil {
				return stopPolicyCheckResult{}, loadErr
			}
			currentTask, captureErr := captureStopTaskSnapshot(repoRoot)
			if captureErr != nil {
				return stopPolicyCheckResult{}, fmt.Errorf("recapture TASK snapshot for cached Stop: %w", captureErr)
			}
			currentGit := stopPolicyGitSnapshotFor(repoRoot)
			currentSnapshot := captureStopPolicyAttemptSnapshot(
				repoRoot, current, "", currentTask, currentGit, scanCache,
			)
			currentInput := stopPolicyFingerprintInputForSnapshotWithScan(
				repoRoot, current, currentGit, currentTask, currentSnapshot.generationCapture(), scanCache,
			)
			if stopPolicyCacheBindingMatches(current, state) &&
				stopPolicyFingerprintCacheableWithScan(currentInput, scanCache) &&
				hashStopPolicyFingerprintInput(currentInput) == fingerprint &&
				!stopPolicyReportExpired(current.StopPolicyExpiresAt) {
				storeStopGenerationIfWorthwhileWithScan(cache, repoRoot, current, currentInput, scanCache)
				return stopPolicyCheckResult{Report: report, GitSnapshot: currentGit, TaskSnapshot: currentTask}, nil
			}
			cache.invalidate(repoRoot, state.SessionID)
			state = current
			taskSnapshot = currentTask
			gitSnapshot = currentGit
			fingerprintInput = currentInput
			cacheable = stopPolicyFingerprintCacheableWithScan(fingerprintInput, scanCache)
			fingerprint = hashStopPolicyFingerprintInput(fingerprintInput)
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
	initialEvidenceHash := stopPolicyEvidenceHash(state)
	cacheInputsStable := false
	var postFingerprintInput stopPolicyFingerprintInput
	if cacheable && fingerprint != "" && reportHash != "" {
		if currentTaskSnapshot, captureErr := captureStopTaskSnapshot(repoRoot); captureErr == nil {
			postGitSnapshot := stopPolicyGitSnapshotFor(repoRoot)
			afterSnapshot := captureStopPolicyAttemptSnapshot(
				repoRoot, state, initialEvidenceRevision, currentTaskSnapshot, postGitSnapshot, scanCache,
			)
			postFingerprintInput = stopPolicyFingerprintInputForSnapshotWithScan(
				repoRoot, state, afterSnapshot.Git, afterSnapshot.Task, afterSnapshot.generationCapture(), scanCache,
			)
			cacheInputsStable = stopPolicyFingerprintCacheableWithScan(postFingerprintInput, scanCache) &&
				hashStopPolicyFingerprintInput(postFingerprintInput) == fingerprint &&
				scanCache.stable(repoRoot, state.WritePaths)
		}
	}
	cacheMetadataPublished := false
	evidenceStable := false
	completeEvaluatedState := state
	updated, err := mutateSessionStateResolved(repoRoot, state.SessionID, func(current SessionState) SessionState {
		evidenceStable = stopPolicyEvidenceRevision(current) == initialEvidenceRevision
		if cacheInputsStable && evidenceStable {
			current.StopPolicyFingerprint = fingerprint
			current.StopPolicyEvidenceHash = initialEvidenceHash
			current.StopPolicyReportHash = reportHash
			current.StopPolicyExpiresAt = stopPolicyReportExpiry(
				repoRoot,
				scanCache.get(repoRoot, state.WritePaths).FreshFiles,
			)
			cacheMetadataPublished = true
		} else {
			clearStopPolicyCacheMetadata(&current)
		}
		current.ReportPath = sessionReportPath(repoRoot, state.SessionID)
		return current
	})
	if err != nil {
		return stopPolicyCheckResult{}, fmt.Errorf("persist stop-policy cache metadata: %w", err)
	}
	state = updated
	inputsChangedDuringEvaluation := cacheable && !cacheInputsStable
	if !evidenceStable || inputsChangedDuringEvaluation {
		cache.invalidate(repoRoot, state.SessionID)
		if stabilityRun >= maxStopPolicyStabilityRuns {
			return stopPolicyCheckResult{}, fmt.Errorf(
				"stop inputs changed during %d consecutive policy evaluations",
				maxStopPolicyStabilityRuns,
			)
		}
		current, loadErr := loadCurrentStopPolicyState(repoRoot, state.SessionID)
		if loadErr != nil {
			return stopPolicyCheckResult{}, loadErr
		}
		currentTask, captureErr := captureStopTaskSnapshot(repoRoot)
		if captureErr != nil {
			return stopPolicyCheckResult{}, fmt.Errorf("recapture TASK snapshot after unstable Stop: %w", captureErr)
		}
		return runStopPolicyCheckLockedAttempt(
			repoRoot, current, evaluator, cache, currentTask, stabilityRun+1,
		)
	}
	if cacheMetadataPublished {
		completeEvaluatedState.StopPolicyFingerprint = updated.StopPolicyFingerprint
		completeEvaluatedState.StopPolicyEvidenceHash = updated.StopPolicyEvidenceHash
		completeEvaluatedState.StopPolicyReportHash = updated.StopPolicyReportHash
		completeEvaluatedState.StopPolicyExpiresAt = updated.StopPolicyExpiresAt
		completeEvaluatedState.ReportPath = updated.ReportPath
		storeStopGenerationIfWorthwhileWithScan(cache, repoRoot, completeEvaluatedState, postFingerprintInput, scanCache)
	} else {
		cache.invalidate(repoRoot, state.SessionID)
	}
	return stopPolicyCheckResult{Report: report, GitSnapshot: gitSnapshot, TaskSnapshot: taskSnapshot}, nil
}

func loadCurrentStopPolicyState(repoRoot, sessionID string) (SessionState, error) {
	current, _, err := loadCurrentStopPolicyStateWithRevision(repoRoot, sessionID)
	return current, err
}

func loadCurrentStopPolicyStateWithRevision(repoRoot, sessionID string) (SessionState, string, error) {
	current, err := loadSessionStateWithLockResolved(repoRoot, sessionID)
	if err != nil {
		return SessionState{}, "", fmt.Errorf("reload session state for Stop revalidation: %w", err)
	}
	revision := stopPolicyEvidenceRevision(current)
	current, err = loadCompleteSessionEvidence(repoRoot, current)
	if err != nil {
		return SessionState{}, "", fmt.Errorf("reload evidence chain for Stop revalidation: %w", err)
	}
	if current.EvidenceOverflow {
		return SessionState{}, "", errors.New("session evidence overflowed during Stop evaluation")
	}
	return current, revision, nil
}

func stopPolicyCacheBindingMatches(current, expected SessionState) bool {
	return stopPolicyEvidenceHash(current) == stopPolicyEvidenceHash(expected) &&
		current.StopPolicyFingerprint == expected.StopPolicyFingerprint &&
		current.StopPolicyEvidenceHash == expected.StopPolicyEvidenceHash &&
		current.StopPolicyReportHash == expected.StopPolicyReportHash &&
		current.StopPolicyExpiresAt == expected.StopPolicyExpiresAt
}

func clearStopPolicyCacheMetadata(state *SessionState) {
	state.StopPolicyFingerprint = ""
	state.StopPolicyEvidenceHash = ""
	state.StopPolicyReportHash = ""
	state.StopPolicyExpiresAt = 0
}

func storeStopGenerationIfWorthwhileWithScan(
	cache *StopDecisionCache,
	root string,
	state SessionState,
	fingerprintInput stopPolicyFingerprintInput,
	scanCache *stopPolicyScanCache,
) {
	if cache == nil || !stopPolicyFingerprintCacheableWithScan(fingerprintInput, scanCache) || !stopGenerationWorthwhile(root, fingerprintInput.GitDirtyFiles) {
		cache.invalidate(root, state.SessionID)
		return
	}
	taskBefore, err := captureStopTaskSnapshot(root)
	if err != nil {
		cache.invalidate(root, state.SessionID)
		return
	}
	gitBefore := stopPolicyGitSnapshotFor(root)
	generationBefore, ok := captureStopRepositoryGenerationWithIdentityAndScan(
		root, gitBefore, fingerprintInput.PolicySourceDigest, fingerprintInput.PolicySourceCount,
		stopTaskSnapshotHash(taskBefore), state.WritePaths, scanCache,
	)
	if !ok {
		cache.invalidate(root, state.SessionID)
		return
	}
	currentInput := stopPolicyFingerprintInputForSnapshotWithScan(
		root, state, gitBefore, taskBefore, stopGenerationCapture{}, scanCache,
	)
	if !stopPolicyFingerprintCacheableWithScan(currentInput, scanCache) ||
		hashStopPolicyFingerprintInput(currentInput) != state.StopPolicyFingerprint {
		cache.invalidate(root, state.SessionID)
		return
	}
	taskAfter, err := captureStopTaskSnapshot(root)
	if err != nil {
		cache.invalidate(root, state.SessionID)
		return
	}
	gitAfter := stopPolicyGitSnapshotFor(root)
	generationAfter, ok := captureStopRepositoryGenerationWithScan(root, gitAfter, taskAfter, state.WritePaths, scanCache)
	if !ok || generationBefore.Fingerprint != generationAfter.Fingerprint || !scanCache.stable(root, state.WritePaths) {
		cache.invalidate(root, state.SessionID)
		return
	}
	cache.store(root, state, generationAfter.Fingerprint)
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
	scanCache := &stopPolicyScanCache{}
	if evidenceHash == "" || state.StopPolicyEvidenceHash != evidenceHash || state.StopPolicyReportHash == "" {
		return nil, false
	}
	fingerprintInput := stopPolicyFingerprintInputForSnapshotWithScan(
		repoRoot, state, gitSnapshot, taskSnapshot, stopGenerationCapture{}, scanCache,
	)
	if !stopPolicyFingerprintCacheableWithScan(fingerprintInput, scanCache) {
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
	current, err := loadCurrentStopPolicyState(repoRoot, state.SessionID)
	if err != nil || !stopPolicyCacheBindingMatches(current, state) {
		return nil, false
	}
	currentTask, err := captureStopTaskSnapshot(repoRoot)
	if err != nil {
		return nil, false
	}
	currentInput := stopPolicyFingerprintInputForSnapshotWithScan(
		repoRoot, current, stopPolicyGitSnapshotFor(repoRoot), currentTask, stopGenerationCapture{}, scanCache,
	)
	if !stopPolicyFingerprintCacheableWithScan(currentInput, scanCache) ||
		hashStopPolicyFingerprintInput(currentInput) != current.StopPolicyFingerprint ||
		stopPolicyReportExpired(current.StopPolicyExpiresAt) || !scanCache.stable(repoRoot, current.WritePaths) {
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
	scanCache := &stopPolicyScanCache{}
	entry, generationAvailable := cache.entry(root, state.SessionID)
	if generationAvailable && entry.evidenceHash == evidenceHash &&
		entry.fingerprint == state.StopPolicyFingerprint && entry.reportHash == state.StopPolicyReportHash {
		report, ok := cache.readStableReport(root, state, taskSnapshot, gitSnapshot, scanCache)
		if ok && len(blockingViolations(report)) == 0 {
			current, loadErr := loadCurrentStopPolicyState(root, state.SessionID)
			currentTask, taskErr := captureStopTaskSnapshot(root)
			currentGit := stopPolicyGitSnapshotFor(root)
			currentGeneration, generationOK := captureStopRepositoryGenerationWithScan(
				root, currentGit, currentTask, current.WritePaths, scanCache,
			)
			if loadErr == nil && taskErr == nil && generationOK &&
				stopPolicyCacheBindingMatches(current, state) &&
				currentGeneration.Fingerprint == entry.generation {
				return report, true
			}
			cache.invalidate(root, state.SessionID)
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
	unlock, err := filelock.LockContext(context.Background(), file, agentSessionLockTimeout)
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
