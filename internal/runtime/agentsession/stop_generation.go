package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/tasklifecycle"
)

const (
	stopGenerationVersion       = "stop-generation-v2"
	maxStopDecisionCacheEntries = 64
)

// StopDecisionCache is the session-owned worker's conservative unchanged-state
// oracle. One-shot hook processes pass nil and retain the exact fingerprint
// path. Entries are memory-only: durable report ownership remains in the
// atomic session state and report files.
type StopDecisionCache struct {
	mu      sync.Mutex
	entries map[string]stopDecisionCacheEntry
	order   []string
}

type stopDecisionCacheEntry struct {
	generation   string
	fingerprint  string
	reportHash   string
	evidenceHash string
}

type stopTaskSnapshot struct {
	Config tasklifecycle.Config   `json:"config"`
	State  tasklifecycle.RunState `json:"state"`
}

type stopRepositoryGeneration struct {
	Version            string                     `json:"version"`
	RepoRoot           string                     `json:"repo_root"`
	RootIdentity       string                     `json:"root_identity"`
	PolicyLockHash     string                     `json:"policy_lock_hash"`
	PolicySourceDigest string                     `json:"policy_source_digest"`
	PolicySourceCount  int                        `json:"policy_source_count"`
	TaskStateHash      string                     `json:"task_state_hash"`
	GitHead            string                     `json:"git_head"`
	GitStatus          string                     `json:"git_status"`
	GitStatusMode      string                     `json:"git_status_mode"`
	GitStatusOK        bool                       `json:"git_status_ok"`
	DirtyPaths         []stopDirtyPathGeneration  `json:"dirty_paths"`
	PolicyInputs       []stopPolicyPathGeneration `json:"policy_inputs,omitempty"`
	SchemaBase         string                     `json:"schema_base"`
	AuditNoCache       string                     `json:"audit_no_cache"`
}

type stopDirtyPathGeneration struct {
	Path       string `json:"path"`
	IndexEntry string `json:"index_entry"`
	Worktree   string `json:"worktree"`
}

type stopPolicyPathGeneration struct {
	Path       string `json:"path"`
	Generation string `json:"generation"`
}

type stopGenerationCapture struct {
	Fingerprint        string
	PolicySourceDigest string
	PolicySourceCount  int
	TaskStateHash      string
}

// stopPolicyAttemptSnapshot is the immutable before/evaluation/after view
// owned by one Stop attempt. Expensive identities are captured once for that
// phase and consumers compare complete snapshots rather than mixing fields
// observed at different times.
type stopPolicyAttemptSnapshot struct {
	State            SessionState
	EvidenceRevision string
	Git              stopPolicyGitSnapshot
	Task             stopTaskSnapshot
	PolicyDigest     string
	PolicyCount      int
	Scan             stopPolicyLockScan
}

func captureStopPolicyAttemptSnapshot(
	root string,
	state SessionState,
	evidenceRevision string,
	taskSnapshot stopTaskSnapshot,
	gitSnapshot stopPolicyGitSnapshot,
	scanCache *stopPolicyScanCache,
) stopPolicyAttemptSnapshot {
	digest, count, err := stopPolicySourceIdentity(root)
	if err != nil {
		digest = "error:" + err.Error()
		count = 0
	}
	return stopPolicyAttemptSnapshot{
		State: state, EvidenceRevision: evidenceRevision, Git: gitSnapshot,
		Task: taskSnapshot, PolicyDigest: digest, PolicyCount: count,
		Scan: scanCache.get(root, state.WritePaths),
	}
}

func (s stopPolicyAttemptSnapshot) generationCapture() stopGenerationCapture {
	return stopGenerationCapture{
		PolicySourceDigest: s.PolicyDigest,
		PolicySourceCount:  s.PolicyCount,
		TaskStateHash:      stopTaskSnapshotHash(s.Task),
	}
}

// NewStopDecisionCache returns one isolated cache owner for a persistent hook
// worker. It has no process-global state and opens no watcher or background
// lifecycle.
func NewStopDecisionCache() *StopDecisionCache {
	return &StopDecisionCache{entries: make(map[string]stopDecisionCacheEntry)}
}

func captureStopTaskSnapshot(root string) (stopTaskSnapshot, error) {
	config, err := tasklifecycle.LoadConfig(root)
	if err != nil {
		return stopTaskSnapshot{}, err
	}
	state, err := tasklifecycle.InspectRunStateResolved(root)
	if err != nil {
		return stopTaskSnapshot{}, err
	}
	return stopTaskSnapshot{Config: config, State: state}, nil
}

func stopTaskSnapshotHash(snapshot stopTaskSnapshot) string {
	body, err := json.Marshal(snapshot)
	if err != nil {
		return "error:" + err.Error()
	}
	return hashBytes(body)
}

func stopPolicySourceIdentity(root string) (string, int, error) {
	bundle, err := ingest.LoadPolicySources(root)
	if err != nil {
		return "", 0, err
	}
	digest, err := compiler.ComputeSourceDigest(bundle)
	if err != nil {
		return "", 0, err
	}
	return digest, len(bundle.Sources), nil
}

func captureStopRepositoryGeneration(
	root string,
	gitSnapshot stopPolicyGitSnapshot,
	taskSnapshot stopTaskSnapshot,
	writePaths []string,
) (stopGenerationCapture, bool) {
	return captureStopRepositoryGenerationWithScan(root, gitSnapshot, taskSnapshot, writePaths, nil)
}

func captureStopRepositoryGenerationWithScan(
	root string,
	gitSnapshot stopPolicyGitSnapshot,
	taskSnapshot stopTaskSnapshot,
	writePaths []string,
	scanCache *stopPolicyScanCache,
) (stopGenerationCapture, bool) {
	policyDigest, policyCount, err := stopPolicySourceIdentity(root)
	if err != nil {
		return stopGenerationCapture{}, false
	}
	return captureStopRepositoryGenerationWithIdentityAndScan(
		root, gitSnapshot,
		policyDigest, policyCount, stopTaskSnapshotHash(taskSnapshot), writePaths,
		scanCache,
	)
}

func captureStopRepositoryGenerationWithIdentity(
	root string,
	gitSnapshot stopPolicyGitSnapshot,
	policyDigest string,
	policyCount int,
	taskStateHash string,
	writePaths []string,
) (stopGenerationCapture, bool) {
	return captureStopRepositoryGenerationWithIdentityAndScan(root, gitSnapshot, policyDigest, policyCount, taskStateHash, writePaths, nil)
}

func captureStopRepositoryGenerationWithIdentityAndScan(
	root string,
	gitSnapshot stopPolicyGitSnapshot,
	policyDigest string,
	policyCount int,
	taskStateHash string,
	writePaths []string,
	scanCache *stopPolicyScanCache,
) (stopGenerationCapture, bool) {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return stopGenerationCapture{}, false
	}
	rootIdentity, ok := stopPathMetadataGeneration(root, rootInfo)
	if !ok || !gitSnapshot.StatusOK || strings.HasPrefix(gitSnapshot.Head, "error:") ||
		strings.HasPrefix(policyDigest, "error:") || strings.HasPrefix(taskStateHash, "error:") {
		return stopGenerationCapture{}, false
	}
	paths := dirtyPathsFromStatus(gitSnapshot.Status)
	indexEntries := gitIndexEntries(root, paths)
	dirty := make([]stopDirtyPathGeneration, 0, len(paths))
	for _, path := range paths {
		indexEntry := indexEntries[path]
		if strings.HasPrefix(indexEntry, "error:") {
			return stopGenerationCapture{}, false
		}
		worktree, reliable := stopWorktreeGeneration(root, path, indexEntry)
		if !reliable {
			return stopGenerationCapture{}, false
		}
		dirty = append(dirty, stopDirtyPathGeneration{Path: path, IndexEntry: indexEntry, Worktree: worktree})
	}
	policyScan := scanCache.get(root, writePaths)
	if !policyScan.Cacheable {
		return stopGenerationCapture{}, false
	}
	policyInputs, ok := stopPolicyPathGenerations(root, policyScan.Paths)
	if !ok {
		return stopGenerationCapture{}, false
	}
	generation := stopRepositoryGeneration{
		Version:            stopGenerationVersion,
		RepoRoot:           root,
		RootIdentity:       rootIdentity,
		PolicyLockHash:     fileContentHash(filepath.Join(root, ".reconc", "policy.lock.json")),
		PolicySourceDigest: policyDigest,
		PolicySourceCount:  policyCount,
		TaskStateHash:      taskStateHash,
		GitHead:            gitSnapshot.Head,
		GitStatus:          gitSnapshot.Status,
		GitStatusMode:      gitSnapshot.StatusMode,
		GitStatusOK:        gitSnapshot.StatusOK,
		DirtyPaths:         dirty,
		PolicyInputs:       policyInputs,
		SchemaBase:         os.Getenv("RECONC_SCHEMA_BASE_URL"),
		AuditNoCache:       os.Getenv("RECONC_AUDIT_NO_CACHE"),
	}
	if strings.HasPrefix(generation.PolicyLockHash, "error:") || strings.HasPrefix(generation.TaskStateHash, "error:") {
		return stopGenerationCapture{}, false
	}
	body, err := json.Marshal(generation)
	if err != nil {
		return stopGenerationCapture{}, false
	}
	return stopGenerationCapture{
		Fingerprint:        hashBytes(body),
		PolicySourceDigest: policyDigest,
		PolicySourceCount:  policyCount,
		TaskStateHash:      generation.TaskStateHash,
	}, true
}

func stopPolicyPathGenerations(root string, paths []string) ([]stopPolicyPathGeneration, bool) {
	generations := make([]stopPolicyPathGeneration, 0, len(paths))
	for _, path := range paths {
		resolved, err := resolveStopPolicyInputPath(root, path)
		if err != nil {
			return nil, false
		}
		info, err := os.Lstat(resolved)
		if os.IsNotExist(err) {
			generations = append(generations, stopPolicyPathGeneration{Path: path, Generation: "missing"})
			continue
		}
		if err != nil {
			return nil, false
		}
		var generation string
		var reliable bool
		switch {
		case info.IsDir():
			generation, reliable = stopPolicyDirectoryGeneration(resolved)
		case info.Mode().IsRegular():
			generation, reliable = stopPathMetadataGeneration(resolved, info)
		}
		if !reliable {
			return nil, false
		}
		generations = append(generations, stopPolicyPathGeneration{Path: path, Generation: generation})
	}
	return generations, true
}

func (cache *StopDecisionCache) entry(root, sessionID string) (stopDecisionCacheEntry, bool) {
	if cache == nil {
		return stopDecisionCacheEntry{}, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, ok := cache.entries[stopDecisionCacheKey(root, sessionID)]
	return entry, ok
}

func (cache *StopDecisionCache) store(root string, state SessionState, generation string) {
	if cache == nil || generation == "" || state.StopPolicyFingerprint == "" || state.StopPolicyReportHash == "" {
		return
	}
	entry := stopDecisionCacheEntry{
		generation:   generation,
		fingerprint:  state.StopPolicyFingerprint,
		reportHash:   state.StopPolicyReportHash,
		evidenceHash: stopPolicyEvidenceHash(state),
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.entries == nil {
		cache.entries = make(map[string]stopDecisionCacheEntry)
	}
	key := stopDecisionCacheKey(root, state.SessionID)
	if _, exists := cache.entries[key]; !exists {
		cache.order = append(cache.order, key)
	}
	cache.entries[key] = entry
	for len(cache.order) > maxStopDecisionCacheEntries {
		oldest := cache.order[0]
		cache.order = cache.order[1:]
		delete(cache.entries, oldest)
	}
}

func (cache *StopDecisionCache) invalidate(root, sessionID string) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	key := stopDecisionCacheKey(root, sessionID)
	delete(cache.entries, key)
	for index, candidate := range cache.order {
		if candidate != key {
			continue
		}
		cache.order = append(cache.order[:index], cache.order[index+1:]...)
		break
	}
}

func stopDecisionCacheKey(root, sessionID string) string {
	sum := sha256.Sum256([]byte(root + "\x00" + sessionID))
	return hex.EncodeToString(sum[:])
}

func (cache *StopDecisionCache) readStableReport(
	root string,
	state SessionState,
	taskSnapshot stopTaskSnapshot,
	gitSnapshot stopPolicyGitSnapshot,
	scanCaches ...*stopPolicyScanCache,
) (*runtime.CheckReport, bool) {
	return cache.readStableReportWithSnapshot(root, state, taskSnapshot, gitSnapshot, firstStopPolicyScanCache(scanCaches), nil)
}

func firstStopPolicyScanCache(values []*stopPolicyScanCache) *stopPolicyScanCache {
	var scanCache *stopPolicyScanCache
	if len(values) > 0 {
		scanCache = values[0]
	}
	return scanCache
}

func (cache *StopDecisionCache) readStableReportWithSnapshot(
	root string,
	state SessionState,
	taskSnapshot stopTaskSnapshot,
	gitSnapshot stopPolicyGitSnapshot,
	scanCache *stopPolicyScanCache,
	beforeSnapshot *stopPolicyAttemptSnapshot,
) (*runtime.CheckReport, bool) {
	entry, ok := cache.entry(root, state.SessionID)
	if !ok || entry.evidenceHash != stopPolicyEvidenceHash(state) ||
		entry.fingerprint != state.StopPolicyFingerprint || entry.reportHash != state.StopPolicyReportHash {
		return nil, false
	}
	// The warm path must honour the same age boundary as the session-state
	// path; otherwise a persistent worker keeps serving a report whose
	// require_fresh_file window has closed.
	if stopPolicyReportExpired(state.StopPolicyExpiresAt) {
		cache.invalidate(root, state.SessionID)
		return nil, false
	}
	var generationBefore stopGenerationCapture
	var generationOK bool
	if beforeSnapshot != nil {
		generationBefore, generationOK = captureStopRepositoryGenerationWithIdentityAndScan(
			root, gitSnapshot, beforeSnapshot.PolicyDigest, beforeSnapshot.PolicyCount,
			stopTaskSnapshotHash(taskSnapshot), state.WritePaths, scanCache,
		)
	} else {
		generationBefore, generationOK = captureStopRepositoryGenerationWithScan(root, gitSnapshot, taskSnapshot, state.WritePaths, scanCache)
	}
	if !generationOK || entry.generation != generationBefore.Fingerprint {
		cache.invalidate(root, state.SessionID)
		return nil, false
	}
	report, reportHash, readErr := readLatestReport(root, state.SessionID)
	if readErr != nil || reportHash != entry.reportHash {
		cache.invalidate(root, state.SessionID)
		return nil, false
	}
	taskAfter, taskErr := captureStopTaskSnapshot(root)
	if taskErr != nil {
		cache.invalidate(root, state.SessionID)
		return nil, false
	}
	generationAfter, generationOK := captureStopRepositoryGenerationWithScan(
		root, stopPolicyGitSnapshotFor(root), taskAfter, state.WritePaths, scanCache,
	)
	if !generationOK || generationBefore.Fingerprint != generationAfter.Fingerprint || !scanCache.stable(root, state.WritePaths) {
		cache.invalidate(root, state.SessionID)
		return nil, false
	}
	return report, true
}
