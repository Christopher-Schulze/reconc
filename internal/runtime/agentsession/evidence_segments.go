package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	maxEvidenceTaintBytes        = 16 * 1024
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
		PolicyLockHash: fileContentHash(filepath.Join(repoRoot, ".reconc", "policy.lock.json")),
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
	if state.EvidenceSegmentCount == 0 {
		return state, nil
	}
	complete := state
	complete.ReadPaths = []string{}
	complete.WritePaths = []string{}
	complete.WriteEpochs = map[string]uint64{}
	complete.Commands = []string{}
	complete.Claims = []string{}
	complete.CommandResults = []CommandResult{}
	complete.CommandResultBytes = 0
	merger := newEvidenceMerger(&complete)
	previousDigest := ""
	for index := uint64(1); index <= state.EvidenceSegmentCount; index++ {
		segment, err := readEvidenceSegment(repoRoot, state.SessionID, index)
		if err != nil {
			return SessionState{}, persistEvidenceChainFailure(repoRoot, state, err)
		}
		if segment.PreviousDigest != previousDigest {
			return SessionState{}, persistEvidenceChainFailure(repoRoot, state,
				fmt.Errorf("evidence segment %d previous digest mismatch", index))
		}
		merger.merge(segment.ReadPaths, segment.WritePaths, segment.WriteEpochs,
			segment.Commands, segment.Claims, segment.CommandResults)
		previousDigest = segment.Digest
	}
	if previousDigest != state.EvidenceSegmentDigest {
		return SessionState{}, persistEvidenceChainFailure(repoRoot, state,
			errors.New("evidence segment chain head does not match session state"))
	}
	merger.merge(state.ReadPaths, state.WritePaths, state.WriteEpochs,
		state.Commands, state.Claims, state.CommandResults)
	return complete, nil
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
	var segment evidenceSegment
	if err := json.Unmarshal(body, &segment); err != nil {
		return evidenceSegment{}, fmt.Errorf("evidence segment %d is not valid JSON: %w", index, err)
	}
	if segment.FormatVersion != evidenceSegmentFormatVersion || segment.RepoRoot != repoRoot ||
		segment.SessionID != sessionID || segment.Index != index {
		return evidenceSegment{}, fmt.Errorf("evidence segment %d identity mismatch", index)
	}
	digest, err := evidenceSegmentDigest(segment)
	if err != nil {
		return evidenceSegment{}, err
	}
	if digest != segment.Digest {
		return evidenceSegment{}, fmt.Errorf("evidence segment %d digest mismatch", index)
	}
	return segment, nil
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
	state    *SessionState
	reads    map[string]struct{}
	writes   map[string]struct{}
	commands map[string]struct{}
	claims   map[string]struct{}
	results  map[commandResultKey]struct{}
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
	return merger
}

func (m *evidenceMerger) merge(
	reads, writes []string,
	writeEpochs map[string]uint64,
	commands, claims []string,
	results []CommandResult,
) {
	appendUniqueStrings(&m.state.ReadPaths, reads, m.reads)
	appendUniqueStrings(&m.state.WritePaths, writes, m.writes)
	for path, epoch := range writeEpochs {
		if epoch > m.state.WriteEpochs[path] {
			m.state.WriteEpochs[path] = epoch
		}
	}
	appendUniqueStrings(&m.state.Commands, commands, m.commands)
	appendUniqueStrings(&m.state.Claims, claims, m.claims)
	for _, result := range results {
		key := commandResultIdentity(result)
		if _, found := m.results[key]; found {
			continue
		}
		m.results[key] = struct{}{}
		m.state.CommandResults = append(m.state.CommandResults, result)
		m.state.CommandResultBytes += int64(commandResultEncodedBytes(result))
	}
}

func makeStringSet(values []string) map[string]struct{} {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	return seen
}

func appendUniqueStrings(target *[]string, added []string, seen map[string]struct{}) {
	for _, value := range added {
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		*target = append(*target, value)
	}
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
