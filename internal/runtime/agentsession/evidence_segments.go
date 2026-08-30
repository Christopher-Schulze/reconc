package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
)

const (
	evidenceSegmentFormatVersion = "raw-evidence-v1"
	evidenceTaintFormatVersion   = "evidence-taint-v1"
	maxEvidenceSegments          = 64
	maxEvidenceSegmentBytes      = MaxSessionStateBytes
	maxCompleteEvidenceBytes     = 16 << 20
	maxEvidenceTaintBytes        = 16 * 1024
	mergedStringOverheadBytes    = 64
	mergedResultOverheadBytes    = 128
	mergedEpochOverheadBytes     = 48
)

type evidenceSegment struct {
	FormatVersion  string            `json:"format_version"`
	RepoRoot       string            `json:"repo_root"`
	SessionID      string            `json:"session_id"`
	Index          uint64            `json:"index"`
	PreviousDigest string            `json:"previous_digest,omitempty"`
	PolicyLockHash string            `json:"policy_lock_hash,omitempty"`
	ReadPaths      []string          `json:"read_paths"`
	WritePaths     []string          `json:"write_paths"`
	WriteEpochs    map[string]uint64 `json:"write_epochs,omitempty"`
	EvidenceEpoch  uint64            `json:"evidence_epoch,omitempty"`
	Commands       []string          `json:"commands"`
	Claims         []string          `json:"claims"`
	CommandResults []CommandResult   `json:"command_results"`
	MaterialEvents uint64            `json:"material_events,omitempty"`
	Digest         string            `json:"digest"`
}

type verifiedEvidenceSegment struct {
	identity   os.FileInfo
	generation string
	digest     string
}

type verifiedEvidencePrefix struct {
	count    uint64
	head     string
	bytes    int
	segments []verifiedEvidenceSegment
	snapshot evidenceSnapshot
}

type evidenceSnapshot struct {
	readPaths          []string
	writePaths         []string
	writeEpochs        map[string]uint64
	commands           []string
	claims             []string
	commandResults     []CommandResult
	commandResultBytes int64
}

// stopLoadedEvidence owns one complete evidence view for one Stop attempt.
// Reuse is authorized only after the raw session evidence revision is read
// again and matches; the value never escapes the synchronous attempt.
type stopLoadedEvidence struct {
	repoRoot   string
	sessionID  string
	revision   string
	complete   SessionState
	prefix     verifiedEvidencePrefix
	valid      bool
	chainLoads int
}

func (loaded *stopLoadedEvidence) load(
	repoRoot string,
	raw SessionState,
	cache *StopDecisionCache,
) (SessionState, string, error) {
	revision, err := stopPolicyEvidenceRevision(raw)
	if err != nil {
		return SessionState{}, "", err
	}
	complete, err := loaded.loadRevision(repoRoot, raw, revision, cache)
	return complete, revision, err
}

func (loaded *stopLoadedEvidence) loadRevision(
	repoRoot string,
	raw SessionState,
	revision string,
	cache *StopDecisionCache,
) (SessionState, error) {
	if loaded != nil && loaded.valid && loaded.repoRoot == repoRoot && loaded.sessionID == raw.SessionID && loaded.revision == revision &&
		(raw.EvidenceSegmentCount == 0 || verifiedEvidencePrefixMatches(loaded.prefix, repoRoot, raw.SessionID, defaultEvidenceSegmentLoadHooks())) {
		complete := raw
		applyLoadedStopEvidence(&complete, loaded.complete)
		return complete, nil
	}
	var prefix verifiedEvidencePrefix
	complete, err := loadCompleteSessionEvidenceWithCacheCapture(repoRoot, raw, cache, &prefix)
	if err != nil {
		return SessionState{}, err
	}
	if loaded != nil {
		loaded.capture(repoRoot, raw, revision, complete, prefix)
	}
	return complete, nil
}

func (loaded *stopLoadedEvidence) capture(
	repoRoot string,
	raw SessionState,
	revision string,
	complete SessionState,
	prefix verifiedEvidencePrefix,
) {
	if loaded == nil {
		return
	}
	loaded.repoRoot = repoRoot
	loaded.sessionID = raw.SessionID
	loaded.revision = revision
	loaded.complete = complete
	loaded.prefix = prefix
	loaded.valid = raw.EvidenceSegmentCount == 0 ||
		(prefix.count == raw.EvidenceSegmentCount && prefix.head == raw.EvidenceSegmentDigest && uint64(len(prefix.segments)) == prefix.count)
	loaded.chainLoads++
}

func applyLoadedStopEvidence(state *SessionState, complete SessionState) {
	state.ReadPaths = complete.ReadPaths
	state.WritePaths = complete.WritePaths
	state.WriteEpochs = complete.WriteEpochs
	state.Commands = complete.Commands
	state.Claims = complete.Claims
	state.CommandResults = complete.CommandResults
	state.CommandResultBytes = complete.CommandResultBytes
}

type evidenceSegmentLoadHooks struct {
	readSnapshot func(string, int64) ([]byte, os.FileInfo, error)
	lstat        func(string) (os.FileInfo, error)
	generation   func(string, os.FileInfo) (string, bool)
}

type evidenceIntegrityError struct {
	cause error
}

func (err *evidenceIntegrityError) Error() string { return err.cause.Error() }
func (err *evidenceIntegrityError) Unwrap() error { return err.cause }

type evidenceTaint struct {
	FormatVersion string `json:"format_version"`
	RepoRoot      string `json:"repo_root"`
	SessionID     string `json:"session_id"`
	Field         string `json:"field"`
	Limit         string `json:"limit"`
	SegmentCount  uint64 `json:"segment_count,omitempty"`
	SegmentDigest string `json:"segment_digest,omitempty"`
}

type EvidenceTaintStatus struct {
	Present       bool   `json:"present"`
	Token         string `json:"token,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	Field         string `json:"field,omitempty"`
	Limit         string `json:"limit,omitempty"`
	SegmentCount  uint64 `json:"segment_count,omitempty"`
	SegmentDigest string `json:"segment_digest,omitempty"`
}

type evidenceTaintResolution struct {
	FormatVersion string        `json:"format_version"`
	Token         string        `json:"token"`
	Reason        string        `json:"reason"`
	Taint         evidenceTaint `json:"taint"`
}

func evidenceSegmentsDir(repoRoot, sessionID string) string {
	return filepath.Join(projectDir(repoRoot), "evidence", sessionFileKey(sessionID))
}

func evidenceSegmentPath(repoRoot, sessionID string, index uint64) string {
	return filepath.Join(evidenceSegmentsDir(repoRoot, sessionID), fmt.Sprintf("%08d.json", index))
}

func evidenceTaintPath(repoRoot string) string {
	return filepath.Join(projectDir(repoRoot), "evidence-taint.json")
}

func evidenceTaintResolutionPath(repoRoot, token string) string {
	return filepath.Join(projectDir(repoRoot), "evidence-taint-resolutions", token+".json")
}

func evidenceFieldRotatable(field string) bool {
	switch strings.TrimSpace(field) {
	case "read_paths", "write_paths", "commands", "claims", "command_results":
		return true
	default:
		return false
	}
}

func sessionHasEvidence(state SessionState) bool {
	return len(state.ReadPaths) > 0 || len(state.WritePaths) > 0 || len(state.Commands) > 0 ||
		len(state.Claims) > 0 || len(state.CommandResults) > 0
}

func rotateSessionEvidenceLocked(repoRoot string, state SessionState) (SessionState, error) {
	if state.EvidenceSegmentCount >= maxEvidenceSegments {
		return state, fmt.Errorf("evidence segment count exceeds %d", maxEvidenceSegments)
	}
	index := state.EvidenceSegmentCount + 1
	segment := evidenceSegment{
		FormatVersion:  evidenceSegmentFormatVersion,
		RepoRoot:       repoRoot,
		SessionID:      state.SessionID,
		Index:          index,
		PreviousDigest: state.EvidenceSegmentDigest,
		PolicyLockHash: fileContentHash(filepath.Join(repoRoot, policyLockfilePath)),
		ReadPaths:      append([]string{}, state.ReadPaths...),
		WritePaths:     append([]string{}, state.WritePaths...),
		WriteEpochs:    cloneWriteEpochs(state.WriteEpochs),
		EvidenceEpoch:  state.EvidenceEpoch,
		Commands:       append([]string{}, state.Commands...),
		Claims:         append([]string{}, state.Claims...),
		CommandResults: append([]CommandResult{}, state.CommandResults...),
		MaterialEvents: state.MaterialEvents,
	}
	digest, err := evidenceSegmentDigest(segment)
	if err != nil {
		return state, err
	}
	segment.Digest = digest
	body, err := json.MarshalIndent(segment, "", "  ")
	if err != nil {
		return state, fmt.Errorf("marshal evidence segment: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxEvidenceSegmentBytes {
		return state, fmt.Errorf("evidence segment is %d bytes; maximum is %d", len(body), maxEvidenceSegmentBytes)
	}
	path := evidenceSegmentPath(repoRoot, state.SessionID, index)
	if err := ensurePrivateStateDir(filepath.Dir(path)); err != nil {
		return state, fmt.Errorf("mkdir evidence segment dir: %w", err)
	}
	if existing, readErr := boundedio.ReadRegularFile(path, maxEvidenceSegmentBytes); readErr == nil {
		if string(existing) != string(body) {
			return state, fmt.Errorf("evidence segment %d already exists with different content", index)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return state, fmt.Errorf("inspect evidence segment: %w", readErr)
	} else if _, err := atomicfile.WritePrivateIfChanged(path, body, 0o600); err != nil {
		return state, fmt.Errorf("write evidence segment: %w", err)
	}
	state.ReadPaths = []string{}
	state.WritePaths = []string{}
	state.WriteEpochs = map[string]uint64{}
	state.Commands = []string{}
	state.Claims = []string{}
	state.CommandResults = []CommandResult{}
	state.CommandResultBytes = 0
	state.EvidenceSegmentCount = index
	state.EvidenceSegmentDigest = digest
	state.EvidenceOverflow = false
	state.EvidenceOverflowReason = ""
	state.EvidenceOverflowLimit = ""
	return state, nil
}

func loadCompleteSessionEvidence(repoRoot string, state SessionState) (SessionState, error) {
	return loadCompleteSessionEvidenceWithCache(repoRoot, state, nil)
}

func loadCompleteSessionEvidenceWithCache(repoRoot string, state SessionState, cache *StopDecisionCache) (SessionState, error) {
	return loadCompleteSessionEvidenceWithCacheCapture(repoRoot, state, cache, nil)
}

func loadCompleteSessionEvidenceWithCacheCapture(
	repoRoot string,
	state SessionState,
	cache *StopDecisionCache,
	captured *verifiedEvidencePrefix,
) (SessionState, error) {
	return loadCompleteSessionEvidenceWithHooksCapture(repoRoot, state, cache, defaultEvidenceSegmentLoadHooks(), captured)
}

func defaultEvidenceSegmentLoadHooks() evidenceSegmentLoadHooks {
	return evidenceSegmentLoadHooks{
		readSnapshot: boundedio.ReadRegularFileSnapshot,
		lstat:        os.Lstat,
		generation:   platformFileGeneration,
	}
}

func loadCompleteSessionEvidenceWithHooks(
	repoRoot string,
	state SessionState,
	cache *StopDecisionCache,
	hooks evidenceSegmentLoadHooks,
) (SessionState, error) {
	return loadCompleteSessionEvidenceWithHooksCapture(repoRoot, state, cache, hooks, nil)
}

func loadCompleteSessionEvidenceWithHooksCapture(
	repoRoot string,
	state SessionState,
	cache *StopDecisionCache,
	hooks evidenceSegmentLoadHooks,
	captured *verifiedEvidencePrefix,
) (SessionState, error) {
	if captured != nil {
		*captured = verifiedEvidencePrefix{}
	}
	if state.EvidenceSegmentCount == 0 {
		cache.dropVerifiedEvidencePrefix(repoRoot, state.SessionID)
		return state, nil
	}
	if state.EvidenceSegmentCount > maxEvidenceSegments {
		return SessionState{}, evidenceLoadFailure(cache, repoRoot, state, &evidenceIntegrityError{
			cause: fmt.Errorf("evidence segment count exceeds %d", maxEvidenceSegments),
		})
	}
	complete := state
	clearMergedEvidence(&complete)
	merger := newEvidenceMerger(&complete)
	verified := make([]verifiedEvidenceSegment, 0, state.EvidenceSegmentCount)
	previousDigest := ""
	startIndex := uint64(1)
	if cached, ok := cache.verifiedEvidencePrefix(repoRoot, state.SessionID); ok &&
		cached.count <= state.EvidenceSegmentCount && cached.count == uint64(len(cached.segments)) &&
		cached.head != "" && verifiedEvidencePrefixMatches(cached, repoRoot, state.SessionID, hooks) {
		cached.snapshot.apply(&complete)
		merger = newEvidenceMerger(&complete)
		previousDigest = cached.head
		startIndex = cached.count + 1
		verified = append(verified, cached.segments...)
	}
	for index := startIndex; index <= state.EvidenceSegmentCount; index++ {
		path := evidenceSegmentPath(repoRoot, state.SessionID, index)
		body, identity, err := hooks.readSnapshot(path, maxEvidenceSegmentBytes)
		if err != nil {
			return SessionState{}, evidenceLoadFailure(cache, repoRoot, state,
				fmt.Errorf("read evidence segment %d: %w", index, err))
		}
		generation, stable, _ := stableEvidenceSegmentGeneration(path, identity, hooks)
		if !stable {
			return SessionState{}, evidenceLoadFailure(cache, repoRoot, state,
				fmt.Errorf("evidence segment %d changed identity during load", index))
		}
		segment, err := decodeEvidenceSegment(repoRoot, state.SessionID, index, body)
		if err != nil {
			return SessionState{}, evidenceLoadFailure(cache, repoRoot, state, err)
		}
		if segment.PreviousDigest != previousDigest {
			return SessionState{}, evidenceLoadFailure(cache, repoRoot, state, &evidenceIntegrityError{
				cause: fmt.Errorf("evidence segment %d previous digest mismatch", index),
			})
		}
		if err := merger.merge(segment.ReadPaths, segment.WritePaths, segment.WriteEpochs,
			segment.Commands, segment.Claims, segment.CommandResults); err != nil {
			return SessionState{}, evidenceCapacityFailure(cache, repoRoot, state, err)
		}
		previousDigest = segment.Digest
		verified = append(verified, verifiedEvidenceSegment{identity: identity, generation: generation, digest: segment.Digest})
	}
	if previousDigest != state.EvidenceSegmentDigest {
		return SessionState{}, evidenceLoadFailure(cache, repoRoot, state, &evidenceIntegrityError{
			cause: errors.New("evidence segment chain head does not match session state"),
		})
	}
	stable, cacheable := revalidateEvidenceSegments(verified, repoRoot, state.SessionID, hooks)
	if !stable {
		return SessionState{}, evidenceLoadFailure(cache, repoRoot, state,
			errors.New("evidence segment identity changed during complete-chain load"))
	}
	if cacheable {
		prefixSnapshot := snapshotEvidence(complete)
		prefix := verifiedEvidencePrefix{
			count: state.EvidenceSegmentCount, head: state.EvidenceSegmentDigest,
			bytes: max(merger.retainedBytes, 1), segments: verified, snapshot: prefixSnapshot,
		}
		if captured != nil {
			*captured = prefix
		}
		cache.storeVerifiedEvidencePrefix(repoRoot, state.SessionID, prefix)
	} else {
		cache.dropVerifiedEvidencePrefix(repoRoot, state.SessionID)
	}
	if err := merger.merge(state.ReadPaths, state.WritePaths, state.WriteEpochs,
		state.Commands, state.Claims, state.CommandResults); err != nil {
		return SessionState{}, evidenceCapacityFailure(cache, repoRoot, state, err)
	}
	return complete, nil
}

func evidenceLoadFailure(cache *StopDecisionCache, repoRoot string, state SessionState, cause error) error {
	cache.dropVerifiedEvidencePrefix(repoRoot, state.SessionID)
	var integrity *evidenceIntegrityError
	if errors.As(cause, &integrity) {
		return persistEvidenceChainFailure(repoRoot, state, cause)
	}
	return cause
}

func evidenceCapacityFailure(cache *StopDecisionCache, repoRoot string, state SessionState, cause error) error {
	cache.dropVerifiedEvidencePrefix(repoRoot, state.SessionID)
	state.EvidenceOverflow = true
	state.EvidenceOverflowReason = "evidence_segments"
	state.EvidenceOverflowLimit = "aggregate_bytes"
	if err := persistEvidenceTaint(repoRoot, state); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func stableEvidenceSegmentGeneration(path string, expected os.FileInfo, hooks evidenceSegmentLoadHooks) (string, bool, bool) {
	current, err := hooks.lstat(path)
	if err != nil || expected == nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(expected, current) || expected.Mode() != current.Mode() || expected.Size() != current.Size() {
		return "", false, false
	}
	generation, generationOK := hooks.generation(path, current)
	after, err := hooks.lstat(path)
	if err != nil || !os.SameFile(current, after) || current.Mode() != after.Mode() || current.Size() != after.Size() {
		return "", false, false
	}
	afterGeneration, afterGenerationOK := hooks.generation(path, after)
	if !generationOK || !afterGenerationOK {
		return "", true, false
	}
	return generation, generation == afterGeneration, generation == afterGeneration
}

func verifiedEvidencePrefixMatches(prefix verifiedEvidencePrefix, repoRoot, sessionID string, hooks evidenceSegmentLoadHooks) bool {
	stable, cacheable := revalidateEvidenceSegments(prefix.segments, repoRoot, sessionID, hooks)
	return stable && cacheable
}

func revalidateEvidenceSegments(segments []verifiedEvidenceSegment, repoRoot, sessionID string, hooks evidenceSegmentLoadHooks) (bool, bool) {
	cacheable := true
	for index, cached := range segments {
		path := evidenceSegmentPath(repoRoot, sessionID, uint64(index+1))
		generation, stable, reliable := stableEvidenceSegmentGeneration(path, cached.identity, hooks)
		if !stable {
			return false, false
		}
		if !reliable || generation != cached.generation || cached.generation == "" || cached.digest == "" {
			cacheable = false
		}
	}
	return true, cacheable
}

func (cache *StopDecisionCache) verifiedEvidencePrefix(repoRoot, sessionID string) (verifiedEvidencePrefix, bool) {
	if cache == nil {
		return verifiedEvidencePrefix{}, false
	}
	key := stopDecisionCacheKey(repoRoot, sessionID)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	prefix, ok := cache.evidence[key]
	prefix.segments = append([]verifiedEvidenceSegment(nil), prefix.segments...)
	prefix.snapshot = prefix.snapshot.clone()
	return prefix, ok
}

func (cache *StopDecisionCache) storeVerifiedEvidencePrefix(repoRoot, sessionID string, prefix verifiedEvidencePrefix) {
	if cache == nil || prefix.count == 0 || uint64(len(prefix.segments)) != prefix.count || prefix.head == "" ||
		prefix.bytes <= 0 || prefix.bytes > maxCompleteEvidenceBytes {
		return
	}
	key := stopDecisionCacheKey(repoRoot, sessionID)
	prefix.segments = append([]verifiedEvidenceSegment(nil), prefix.segments...)
	prefix.snapshot = prefix.snapshot.clone()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.evidence == nil {
		cache.evidence = make(map[string]verifiedEvidencePrefix)
	}
	cache.removeVerifiedEvidencePrefixLocked(key)
	if prefix.bytes > maxVerifiedEvidenceBytes {
		return
	}
	cache.evidenceOrder = append(cache.evidenceOrder, key)
	for len(cache.evidenceOrder) > maxStopDecisionCacheEntries || cache.evidenceBytes+prefix.bytes > maxVerifiedEvidenceBytes {
		oldest := cache.evidenceOrder[0]
		cache.removeVerifiedEvidencePrefixLocked(oldest)
	}
	cache.evidence[key] = prefix
	cache.evidenceBytes += prefix.bytes
}

func (cache *StopDecisionCache) dropVerifiedEvidencePrefix(repoRoot, sessionID string) {
	if cache == nil {
		return
	}
	key := stopDecisionCacheKey(repoRoot, sessionID)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.removeVerifiedEvidencePrefixLocked(key)
}

func (cache *StopDecisionCache) removeVerifiedEvidencePrefixLocked(key string) {
	if previous, exists := cache.evidence[key]; exists {
		cache.evidenceBytes -= previous.bytes
		delete(cache.evidence, key)
	}
	for index, candidate := range cache.evidenceOrder {
		if candidate == key {
			cache.evidenceOrder = append(cache.evidenceOrder[:index], cache.evidenceOrder[index+1:]...)
			break
		}
	}
}

func persistEvidenceChainFailure(repoRoot string, state SessionState, cause error) error {
	state.EvidenceOverflow = true
	state.EvidenceOverflowReason = "evidence_segments"
	state.EvidenceOverflowLimit = "chain_integrity"
	if err := persistEvidenceTaint(repoRoot, state); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func readEvidenceSegment(repoRoot, sessionID string, index uint64) (evidenceSegment, error) {
	path := evidenceSegmentPath(repoRoot, sessionID, index)
	body, err := boundedio.ReadRegularFile(path, maxEvidenceSegmentBytes)
	if err != nil {
		return evidenceSegment{}, fmt.Errorf("read evidence segment %d: %w", index, err)
	}
	return decodeEvidenceSegment(repoRoot, sessionID, index, body)
}

func decodeEvidenceSegment(repoRoot, sessionID string, index uint64, body []byte) (evidenceSegment, error) {
	var segment evidenceSegment
	if err := json.Unmarshal(body, &segment); err != nil {
		return evidenceSegment{}, &evidenceIntegrityError{cause: fmt.Errorf("evidence segment %d is not valid JSON: %w", index, err)}
	}
	if segment.FormatVersion != evidenceSegmentFormatVersion || segment.RepoRoot != repoRoot ||
		segment.SessionID != sessionID || segment.Index != index {
		return evidenceSegment{}, &evidenceIntegrityError{cause: fmt.Errorf("evidence segment %d identity mismatch", index)}
	}
	if err := validateEvidenceSegmentShape(segment); err != nil {
		return evidenceSegment{}, &evidenceIntegrityError{cause: fmt.Errorf("evidence segment %d shape mismatch: %w", index, err)}
	}
	digest, err := evidenceSegmentDigest(segment)
	if err != nil {
		return evidenceSegment{}, err
	}
	if digest != segment.Digest {
		return evidenceSegment{}, &evidenceIntegrityError{cause: fmt.Errorf("evidence segment %d digest mismatch", index)}
	}
	return segment, nil
}

func validateEvidenceSegmentShape(segment evidenceSegment) error {
	if segment.ReadPaths == nil || segment.WritePaths == nil || segment.Commands == nil ||
		segment.Claims == nil || segment.CommandResults == nil {
		return errors.New("evidence collections must be arrays")
	}
	state := SessionState{
		ReadPaths: segment.ReadPaths, WritePaths: segment.WritePaths, WriteEpochs: segment.WriteEpochs,
		EvidenceEpoch: segment.EvidenceEpoch, Commands: segment.Commands, Claims: segment.Claims,
		CommandResults: segment.CommandResults,
	}
	normalized := normalizeSessionState(state)
	if normalized.EvidenceOverflow || !reflect.DeepEqual(normalized.ReadPaths, segment.ReadPaths) ||
		!reflect.DeepEqual(normalized.WritePaths, segment.WritePaths) ||
		!reflect.DeepEqual(normalized.Commands, segment.Commands) ||
		!reflect.DeepEqual(normalized.Claims, segment.Claims) ||
		!reflect.DeepEqual(normalized.CommandResults, segment.CommandResults) ||
		!equalWriteEpochs(normalized.WriteEpochs, segment.WriteEpochs) {
		return errors.New("evidence collections are not canonical and bounded")
	}
	for index, result := range segment.CommandResults {
		if result.Outcome != "success" && result.Outcome != "failure" {
			return fmt.Errorf("command_results[%d].outcome must be success|failure", index)
		}
	}
	return nil
}

func equalWriteEpochs(left, right map[string]uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for path, epoch := range left {
		if right[path] != epoch {
			return false
		}
	}
	return true
}

func evidenceSegmentDigest(segment evidenceSegment) (string, error) {
	segment.Digest = ""
	body, err := json.Marshal(segment)
	if err != nil {
		return "", fmt.Errorf("marshal evidence segment digest: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

type evidenceMerger struct {
	state         *SessionState
	reads         map[string]struct{}
	writes        map[string]struct{}
	commands      map[string]struct{}
	claims        map[string]struct{}
	results       map[commandResultKey]struct{}
	retainedBytes int
}

func newEvidenceMerger(state *SessionState) *evidenceMerger {
	merger := &evidenceMerger{
		state:    state,
		reads:    makeStringSet(state.ReadPaths),
		writes:   makeStringSet(state.WritePaths),
		commands: makeStringSet(state.Commands),
		claims:   makeStringSet(state.Claims),
		results:  make(map[commandResultKey]struct{}, len(state.CommandResults)),
	}
	for _, result := range state.CommandResults {
		merger.results[commandResultIdentity(result)] = struct{}{}
	}
	merger.retainedBytes = mergedEvidenceBytes(*state)
	return merger
}

func (m *evidenceMerger) merge(
	reads, writes []string,
	writeEpochs map[string]uint64,
	commands, claims []string,
	results []CommandResult,
) error {
	if !m.appendStrings(&m.state.ReadPaths, reads, m.reads) ||
		!m.appendStrings(&m.state.WritePaths, writes, m.writes) {
		return fmt.Errorf("complete evidence exceeds %d merged bytes", maxCompleteEvidenceBytes)
	}
	for path, epoch := range writeEpochs {
		if _, exists := m.state.WriteEpochs[path]; !exists {
			charge := len(path) + mergedEpochOverheadBytes
			if m.retainedBytes+charge > maxCompleteEvidenceBytes {
				return fmt.Errorf("complete evidence exceeds %d merged bytes", maxCompleteEvidenceBytes)
			}
			m.retainedBytes += charge
		}
		if epoch > m.state.WriteEpochs[path] {
			m.state.WriteEpochs[path] = epoch
		}
	}
	if !m.appendStrings(&m.state.Commands, commands, m.commands) ||
		!m.appendStrings(&m.state.Claims, claims, m.claims) {
		return fmt.Errorf("complete evidence exceeds %d merged bytes", maxCompleteEvidenceBytes)
	}
	for _, result := range results {
		key := commandResultIdentity(result)
		if _, found := m.results[key]; found {
			continue
		}
		encodedBytes := commandResultEncodedBytes(result)
		resultBytes := encodedBytes + mergedResultOverheadBytes
		if encodedBytes <= 0 || m.retainedBytes+resultBytes > maxCompleteEvidenceBytes {
			return fmt.Errorf("complete evidence exceeds %d merged bytes", maxCompleteEvidenceBytes)
		}
		m.results[key] = struct{}{}
		m.state.CommandResults = append(m.state.CommandResults, result)
		m.state.CommandResultBytes += int64(encodedBytes)
		m.retainedBytes += resultBytes
	}
	return nil
}

func (m *evidenceMerger) appendStrings(target *[]string, added []string, seen map[string]struct{}) bool {
	for _, value := range added {
		if _, found := seen[value]; found {
			continue
		}
		charge := len(value) + mergedStringOverheadBytes
		if m.retainedBytes+charge > maxCompleteEvidenceBytes {
			return false
		}
		seen[value] = struct{}{}
		*target = append(*target, value)
		m.retainedBytes += charge
	}
	return true
}

func makeStringSet(values []string) map[string]struct{} {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	return seen
}

func clearMergedEvidence(state *SessionState) {
	state.ReadPaths = []string{}
	state.WritePaths = []string{}
	state.WriteEpochs = map[string]uint64{}
	state.Commands = []string{}
	state.Claims = []string{}
	state.CommandResults = []CommandResult{}
	state.CommandResultBytes = 0
}

func snapshotEvidence(state SessionState) evidenceSnapshot {
	return evidenceSnapshot{
		readPaths: append([]string(nil), state.ReadPaths...), writePaths: append([]string(nil), state.WritePaths...),
		writeEpochs: cloneWriteEpochs(state.WriteEpochs), commands: append([]string(nil), state.Commands...),
		claims: append([]string(nil), state.Claims...), commandResults: append([]CommandResult(nil), state.CommandResults...),
		commandResultBytes: state.CommandResultBytes,
	}
}

func (snapshot evidenceSnapshot) clone() evidenceSnapshot {
	state := SessionState{}
	snapshot.apply(&state)
	return snapshotEvidence(state)
}

func (snapshot evidenceSnapshot) apply(state *SessionState) {
	state.ReadPaths = append([]string(nil), snapshot.readPaths...)
	state.WritePaths = append([]string(nil), snapshot.writePaths...)
	state.WriteEpochs = cloneWriteEpochs(snapshot.writeEpochs)
	state.Commands = append([]string(nil), snapshot.commands...)
	state.Claims = append([]string(nil), snapshot.claims...)
	state.CommandResults = append([]CommandResult(nil), snapshot.commandResults...)
	state.CommandResultBytes = snapshot.commandResultBytes
}

func mergedEvidenceBytes(state SessionState) int {
	stringCount := len(state.ReadPaths) + len(state.WritePaths) + len(state.Commands) + len(state.Claims)
	total := stringBytes(state.ReadPaths) + stringBytes(state.WritePaths) + stringBytes(state.Commands) + stringBytes(state.Claims) +
		stringCount*mergedStringOverheadBytes + len(state.WriteEpochs)*mergedEpochOverheadBytes
	for _, result := range state.CommandResults {
		total += commandResultEncodedBytes(result) + mergedResultOverheadBytes
	}
	return total
}

func persistEvidenceTaint(repoRoot string, state SessionState) error {
	taint := evidenceTaint{
		FormatVersion: evidenceTaintFormatVersion,
		RepoRoot:      repoRoot,
		SessionID:     state.SessionID,
		Field:         strings.TrimSpace(state.EvidenceOverflowReason),
		Limit:         strings.TrimSpace(state.EvidenceOverflowLimit),
		SegmentCount:  state.EvidenceSegmentCount,
		SegmentDigest: state.EvidenceSegmentDigest,
	}
	body, err := json.MarshalIndent(taint, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal evidence taint: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxEvidenceTaintBytes {
		return fmt.Errorf("evidence taint exceeds %d bytes", maxEvidenceTaintBytes)
	}
	path := evidenceTaintPath(repoRoot)
	if err := ensurePrivateStateDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("mkdir evidence taint dir: %w", err)
	}
	if _, err := atomicfile.WritePrivateIfChanged(path, body, 0o600); err != nil {
		return fmt.Errorf("write evidence taint: %w", err)
	}
	return nil
}

func loadEvidenceTaint(repoRoot string) (*evidenceTaint, error) {
	path := evidenceTaintPath(repoRoot)
	body, err := boundedio.ReadRegularFile(path, maxEvidenceTaintBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read evidence taint: %w", err)
	}
	var taint evidenceTaint
	if err := json.Unmarshal(body, &taint); err != nil {
		return nil, fmt.Errorf("evidence taint is not valid JSON: %w", err)
	}
	if taint.FormatVersion != evidenceTaintFormatVersion || taint.RepoRoot != repoRoot ||
		strings.TrimSpace(taint.SessionID) == "" || strings.TrimSpace(taint.Field) == "" {
		return nil, errors.New("evidence taint identity is invalid")
	}
	if taint.SegmentCount > maxEvidenceSegments {
		return nil, fmt.Errorf("evidence taint segment count %s exceeds %d", strconv.FormatUint(taint.SegmentCount, 10), maxEvidenceSegments)
	}
	return &taint, nil
}

func ReadEvidenceTaintStatus(repoRoot string) (EvidenceTaintStatus, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return EvidenceTaintStatus{}, err
	}
	taint, err := loadEvidenceTaint(root)
	if err != nil {
		return EvidenceTaintStatus{}, err
	}
	if taint == nil {
		active, activeErr := resolveActiveSessionIDResolved(root)
		if activeErr != nil {
			return EvidenceTaintStatus{}, activeErr
		}
		if active != "" {
			if _, loadErr := loadSessionStateWithLockResolved(root, active); loadErr != nil {
				return EvidenceTaintStatus{}, loadErr
			}
			taint, err = loadEvidenceTaint(root)
			if err != nil {
				return EvidenceTaintStatus{}, err
			}
		}
	}
	if taint == nil {
		return EvidenceTaintStatus{}, nil
	}
	token, err := evidenceTaintToken(*taint)
	if err != nil {
		return EvidenceTaintStatus{}, err
	}
	return EvidenceTaintStatus{
		Present: true, Token: token, SessionID: taint.SessionID,
		Field: taint.Field, Limit: taint.Limit,
		SegmentCount: taint.SegmentCount, SegmentDigest: taint.SegmentDigest,
	}, nil
}

func ResolveEvidenceTaint(repoRoot, expectedToken, reason string) (EvidenceTaintStatus, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return EvidenceTaintStatus{}, err
	}
	expectedToken = strings.TrimSpace(expectedToken)
	reason = strings.TrimSpace(reason)
	if expectedToken == "" {
		return EvidenceTaintStatus{}, errors.New("expected taint token must be non-empty")
	}
	if reason == "" || len(reason) > 512 {
		return EvidenceTaintStatus{}, errors.New("resolution reason must contain 1..512 bytes")
	}
	active, err := resolveActiveSessionIDResolved(root)
	if err != nil {
		return EvidenceTaintStatus{}, err
	}
	if active != "" {
		return EvidenceTaintStatus{}, fmt.Errorf("active session %q must end before evidence taint can be resolved", active)
	}
	taint, err := loadEvidenceTaint(root)
	if err != nil {
		return EvidenceTaintStatus{}, err
	}
	if taint == nil {
		return EvidenceTaintStatus{}, errors.New("no persisted evidence taint exists")
	}
	token, err := evidenceTaintToken(*taint)
	if err != nil {
		return EvidenceTaintStatus{}, err
	}
	if token != expectedToken {
		return EvidenceTaintStatus{}, errors.New("evidence taint token changed; inspect status and retry with the exact current token")
	}
	resolution := evidenceTaintResolution{
		FormatVersion: "evidence-taint-resolution-v1",
		Token:         token,
		Reason:        reason,
		Taint:         *taint,
	}
	body, err := json.MarshalIndent(resolution, "", "  ")
	if err != nil {
		return EvidenceTaintStatus{}, fmt.Errorf("marshal evidence taint resolution: %w", err)
	}
	body = append(body, '\n')
	path := evidenceTaintResolutionPath(root, token)
	if err := ensurePrivateStateDir(filepath.Dir(path)); err != nil {
		return EvidenceTaintStatus{}, fmt.Errorf("mkdir evidence taint resolution dir: %w", err)
	}
	if _, err := atomicfile.WritePrivateIfChanged(path, body, 0o600); err != nil {
		return EvidenceTaintStatus{}, fmt.Errorf("write evidence taint resolution: %w", err)
	}
	if err := os.Remove(evidenceTaintPath(root)); err != nil {
		return EvidenceTaintStatus{}, fmt.Errorf("remove resolved evidence taint: %w", err)
	}
	return EvidenceTaintStatus{
		Present: true, Token: token, SessionID: taint.SessionID,
		Field: taint.Field, Limit: taint.Limit,
		SegmentCount: taint.SegmentCount, SegmentDigest: taint.SegmentDigest,
	}, nil
}

func evidenceTaintToken(taint evidenceTaint) (string, error) {
	body, err := json.Marshal(taint)
	if err != nil {
		return "", fmt.Errorf("marshal evidence taint token: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func applyEvidenceTaint(state *SessionState, taint evidenceTaint) {
	state.EvidenceOverflow = true
	state.EvidenceOverflowReason = taint.Field
	state.EvidenceOverflowLimit = taint.Limit
}
