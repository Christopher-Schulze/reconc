package agentsession

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
)

// compactSessionEvidenceLocked replaces a chain that reached its segment
// ceiling with a fresh deduplicated chain written after it. The session lock
// is held. The returned state references only the new chain; the old chain
// stays intact until that state is published, and reclaimStaleEvidenceSegments
// removes it afterwards. A crash before publication leaves the old chain
// authoritative and the new files after it, which the next rotation sets aside.
func compactSessionEvidenceLocked(repoRoot string, state SessionState) (SessionState, error) {
	merged, err := mergedSessionEvidence(repoRoot, state, nil, defaultEvidenceSegmentLoadHooks(), nil)
	if err != nil {
		return state, err
	}
	parts, err := compactedEvidenceParts(merged)
	if err != nil {
		return state, err
	}
	if len(parts) >= maxEvidenceSegments {
		return state, fmt.Errorf("compacted evidence needs %d segments; maximum is %d", len(parts), maxEvidenceSegments-1)
	}
	base := evidenceSegmentStart(state) + state.EvidenceSegmentCount
	lockHash := fileContentHash(filepath.Join(repoRoot, policyLockfilePath))
	head := ""
	for offset, part := range parts {
		segment := evidenceSegment{
			FormatVersion:  evidenceSegmentFormatVersion,
			RepoRoot:       repoRoot,
			SessionID:      state.SessionID,
			Index:          base + uint64(offset),
			PreviousDigest: head,
			PolicyLockHash: lockHash,
			ReadPaths:      part.ReadPaths,
			WritePaths:     part.WritePaths,
			WriteEpochs:    part.WriteEpochs,
			EvidenceEpoch:  merged.EvidenceEpoch,
			Commands:       part.Commands,
			Claims:         part.Claims,
			CommandResults: part.CommandResults,
			MaterialEvents: state.MaterialEvents,
		}
		digest, err := evidenceSegmentDigest(segment)
		if err != nil {
			return state, err
		}
		segment.Digest = digest
		body, err := marshalWithoutHTMLEscape(segment)
		if err != nil {
			return state, fmt.Errorf("marshal compacted evidence segment: %w", err)
		}
		if len(body) > maxEvidenceSegmentBytes {
			return state, fmt.Errorf("compacted evidence segment is %d bytes; maximum is %d", len(body), maxEvidenceSegmentBytes)
		}
		if err := publishEvidenceSegmentLocked(evidenceSegmentPath(repoRoot, state.SessionID, segment.Index), body); err != nil {
			return state, err
		}
		head = digest
	}
	clearSealedEvidence(&state)
	state.EvidenceSegmentCount = uint64(len(parts))
	state.EvidenceSegmentDigest = head
	state.EvidenceSegmentBase = base
	if len(parts) == 0 {
		state.EvidenceSegmentBase = 0
	}
	return state, nil
}

// compactedEvidenceParts splits deduplicated merged evidence into canonical
// segment-sized states. Each part is exactly what normalization keeps, so a
// part never exceeds a field budget or the segment byte ceiling, and merging
// the parts in order reproduces the merged evidence.
func compactedEvidenceParts(merged SessionState) ([]SessionState, error) {
	queue := compactionQueue{
		reads:    merged.ReadPaths,
		writes:   merged.WritePaths,
		commands: merged.Commands,
		claims:   merged.Claims,
		results:  merged.CommandResults,
	}
	parts := []SessionState{}
	for !queue.empty() {
		limits := compactionLimits{
			reads:    min(len(queue.reads), maxPathEvidenceItems),
			writes:   min(len(queue.writes), maxPathEvidenceItems),
			commands: min(len(queue.commands), maxCommandEvidenceItems),
			claims:   min(len(queue.claims), maxClaimEvidenceItems),
			results:  min(len(queue.results), maxCommandResultItems),
		}
		for {
			part := queue.take(limits, merged.WriteEpochs)
			normalized := normalizeSessionState(part)
			if compactionPartFits(part, normalized) {
				parts = append(parts, normalized)
				queue.advance(limits)
				break
			}
			if !limits.shrink(normalized.EvidenceOverflowReason) {
				return nil, errors.New("one evidence item exceeds a sealed segment")
			}
		}
	}
	return parts, nil
}

type compactionQueue struct {
	reads, writes, commands, claims []string
	results                         []CommandResult
}

type compactionLimits struct {
	reads, writes, commands, claims, results int
}

func (queue compactionQueue) empty() bool {
	return len(queue.reads) == 0 && len(queue.writes) == 0 && len(queue.commands) == 0 &&
		len(queue.claims) == 0 && len(queue.results) == 0
}

func (queue compactionQueue) take(limits compactionLimits, epochs map[string]uint64) SessionState {
	part := SessionState{
		ReadPaths:      append([]string{}, queue.reads[:limits.reads]...),
		WritePaths:     append([]string{}, queue.writes[:limits.writes]...),
		WriteEpochs:    map[string]uint64{},
		Commands:       append([]string{}, queue.commands[:limits.commands]...),
		Claims:         append([]string{}, queue.claims[:limits.claims]...),
		CommandResults: append([]CommandResult{}, queue.results[:limits.results]...),
	}
	for _, path := range part.WritePaths {
		if epoch := epochs[path]; epoch > 0 {
			part.WriteEpochs[path] = epoch
		}
	}
	return part
}

func (queue *compactionQueue) advance(limits compactionLimits) {
	queue.reads = queue.reads[limits.reads:]
	queue.writes = queue.writes[limits.writes:]
	queue.commands = queue.commands[limits.commands:]
	queue.claims = queue.claims[limits.claims:]
	queue.results = queue.results[limits.results:]
}

// shrink halves the field that overflowed, or every field when the whole
// segment was too large. It reports false when no field can shrink further.
func (limits *compactionLimits) shrink(field string) bool {
	targets := map[string]*int{
		"read_paths": &limits.reads, "write_paths": &limits.writes, "commands": &limits.commands,
		"claims": &limits.claims, "command_results": &limits.results,
	}
	if target, ok := targets[strings.TrimSpace(field)]; ok && *target > 1 {
		*target /= 2
		return true
	}
	shrunk := false
	for _, target := range targets {
		if *target > 1 {
			*target /= 2
			shrunk = true
		}
	}
	return shrunk
}

func compactionPartFits(part, normalized SessionState) bool {
	if normalized.EvidenceOverflow ||
		len(normalized.ReadPaths) != len(part.ReadPaths) || len(normalized.WritePaths) != len(part.WritePaths) ||
		len(normalized.Commands) != len(part.Commands) || len(normalized.Claims) != len(part.Claims) ||
		len(normalized.CommandResults) != len(part.CommandResults) {
		return false
	}
	segment := evidenceSegment{
		ReadPaths: normalized.ReadPaths, WritePaths: normalized.WritePaths, WriteEpochs: normalized.WriteEpochs,
		Commands: normalized.Commands, Claims: normalized.Claims, CommandResults: normalized.CommandResults,
	}
	body, err := marshalWithoutHTMLEscape(segment)
	// Identity fields and the digest add a few hundred bytes; the state budget
	// keeps the margin the segment ceiling reserves for them.
	return err == nil && len(body) <= MaxSessionStateBytes
}

// reclaimStaleEvidenceSegments removes sealed segments before the published
// chain's start after a compaction. Leftover files are inert: they are never
// part of the chain, retention bounds them with the session, and the next
// compaction retries the removal.
func reclaimStaleEvidenceSegments(repoRoot string, state SessionState) error {
	start := evidenceSegmentStart(state)
	directory := evidenceSegmentsDir(repoRoot, state.SessionID)
	entries, err := boundedio.ReadDirNoSymlink(directory, 2*maxEvidenceSegments+8)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var removeErr error
	for _, entry := range entries {
		index, ok := evidenceSegmentFileIndex(entry.Name())
		if !ok || index >= start || !entry.Type().IsRegular() {
			continue
		}
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeErr = errors.Join(removeErr, err)
		}
	}
	return removeErr
}

// evidenceSegmentFileIndex parses a sealed segment file name `%08d.json`.
func evidenceSegmentFileIndex(name string) (uint64, bool) {
	if len(name) != 13 || !strings.HasSuffix(name, ".json") {
		return 0, false
	}
	index, err := strconv.ParseUint(name[:8], 10, 64)
	if err != nil || index == 0 || fmt.Sprintf("%08d.json", index) != name {
		return 0, false
	}
	return index, true
}
