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
	stopGenerationVersion       = "stop-generation-v1"
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
	Version            string                    `json:"version"`
	RepoRoot           string                    `json:"repo_root"`
	RootIdentity       string                    `json:"root_identity"`
	PolicyLockHash     string                    `json:"policy_lock_hash"`
	PolicySourceDigest string                    `json:"policy_source_digest"`
	PolicySourceCount  int                       `json:"policy_source_count"`
	TaskStateHash      string                    `json:"task_state_hash"`
	GitHead            string                    `json:"git_head"`
	GitStatus          string                    `json:"git_status"`
	GitStatusMode      string                    `json:"git_status_mode"`
	GitStatusOK        bool                      `json:"git_status_ok"`
	DirtyPaths         []stopDirtyPathGeneration `json:"dirty_paths"`
	SchemaBase         string                    `json:"schema_base"`
	AuditNoCache       string                    `json:"audit_no_cache"`
}

type stopDirtyPathGeneration struct {
	Path       string `json:"path"`
	IndexEntry string `json:"index_entry"`
	Worktree   string `json:"worktree"`
}

type stopGenerationCapture struct {
	Fingerprint        string
	PolicySourceDigest string
	PolicySourceCount  int
	TaskStateHash      string
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
) (stopGenerationCapture, bool) {
	policyDigest, policyCount, err := stopPolicySourceIdentity(root)
	if err != nil {
		return stopGenerationCapture{}, false
	}
	return captureStopRepositoryGenerationWithIdentity(
		root, gitSnapshot,
		policyDigest, policyCount, stopTaskSnapshotHash(taskSnapshot),
	)
}

func captureStopRepositoryGenerationWithIdentity(
	root string,
	gitSnapshot stopPolicyGitSnapshot,
	policyDigest string,
	policyCount int,
	taskStateHash string,
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
) (*runtime.CheckReport, bool) {
	entry, ok := cache.entry(root, state.SessionID)
	if !ok || entry.evidenceHash != stopPolicyEvidenceHash(state) ||
		entry.fingerprint != state.StopPolicyFingerprint || entry.reportHash != state.StopPolicyReportHash {
		return nil, false
	}
	generation, generationOK := captureStopRepositoryGeneration(root, gitSnapshot, taskSnapshot)
	if !generationOK || entry.generation != generation.Fingerprint {
		cache.invalidate(root, state.SessionID)
		return nil, false
	}
	report, reportHash, readErr := readLatestReport(root, state.SessionID)
	if readErr != nil || reportHash != entry.reportHash {
		cache.invalidate(root, state.SessionID)
		return nil, false
	}
	return report, true
}
