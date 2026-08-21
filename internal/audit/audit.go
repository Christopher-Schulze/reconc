// Package audit implements the optional policy-decision audit log (W29).
//
// When enabled, every non-trivial enforcement decision (check, ci,
// assert, can) appends one JSONL record to .reconc/audit.jsonl. The
// log is append-only, cross-process serialized, and self-rotating through a
// fixed archive ring once it reaches the configured size cap.
//
// Design notes:
//   - Disabled by default. Opt-in via RECONC_AUDIT=1 env var or
//     `audit.enabled: true` in .reconc.yml.
//   - One record per JSON object per line (JSONL), never a JSON array.
//     Append-only means readers can always tail the file.
//   - Records are deliberately small (~200 bytes typical) so a full
//     day of hook-driven checks fits comfortably in a few MB.
//   - No external deps: stdlib json + os only.
package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/jsonl"
	"reconc.dev/reconc/internal/privatefs"
)

// Relative path under the repo root where the log lives.
const AuditFileRelative = ".reconc/audit.jsonl"
const AuditHeadRelative = ".reconc/audit.head.json"

const (
	auditChainVersion = "audit-chain-v1"
	auditHeadMaxBytes = 16 * 1024
	// DefaultMaxSizeBytes bounds each live/archive file. Together with the
	// two-file ring, audit storage is capped at 6 MiB per repository.
	DefaultMaxSizeBytes = 2 * 1024 * 1024
	MaxArchiveFiles     = 2
	maxRecordBytes      = 32 * 1024
	maxEntryListItems   = 128
	maxEntryListBytes   = 16 * 1024
)

// Entry is one audit record. Zero-value fields serialise to omitempty so
// small checks stay small on disk.
type Entry struct {
	ChainVersion   string   `json:"chain_version"`
	Sequence       uint64   `json:"sequence"`
	PreviousDigest string   `json:"previous_digest,omitempty"`
	Digest         string   `json:"digest"`
	Timestamp      string   `json:"ts"`
	Event          string   `json:"event"` // check | ci | assert | can | hook
	Decision       string   `json:"decision"`
	OK             bool     `json:"ok"`
	RuleIDs        []string `json:"rule_ids,omitempty"`
	ViolationCount int      `json:"violation_count"`
	BlockingCount  int      `json:"blocking_count"`
	WritePaths     []string `json:"write_paths,omitempty"`
	ReadPaths      []string `json:"read_paths,omitempty"`
	Commands       []string `json:"commands,omitempty"`
	Claims         []string `json:"claims,omitempty"`
	RepoRoot       string   `json:"repo_root,omitempty"`
	LockfileDigest string   `json:"lockfile_digest,omitempty"`
	ReconcVersion  string   `json:"reconc_version,omitempty"`
	DurationMs     int64    `json:"duration_ms,omitempty"`
	Agent          string   `json:"agent,omitempty"`
}

type chainHead struct {
	ChainVersion  string `json:"chain_version"`
	FirstSequence uint64 `json:"first_sequence"`
	FirstDigest   string `json:"first_digest"`
	LastSequence  uint64 `json:"last_sequence"`
	LastDigest    string `json:"last_digest"`
	EntryCount    int    `json:"entry_count"`
}

// VerificationReport is the detached-head and hash-chain proof returned by
// Verify. FirstSequence may be greater than one after bounded retention.
type VerificationReport struct {
	Valid         bool   `json:"valid"`
	Entries       int    `json:"entries"`
	FirstSequence uint64 `json:"first_sequence,omitempty"`
	LastSequence  uint64 `json:"last_sequence,omitempty"`
	LastDigest    string `json:"last_digest,omitempty"`
}

// Enabled reports whether audit logging is active for the given repo.
// The env var RECONC_AUDIT always wins (1/true/on enables, 0/false/off
// disables). Otherwise we defer to an explicit flag passed in by the
// caller (typically parsed from .reconc.yml once that config key is
// added). Default: disabled.
func Enabled(repoRoot string, configEnabled bool) bool {
	if v, ok := os.LookupEnv("RECONC_AUDIT"); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "on", "yes":
			return true
		case "0", "false", "off", "no":
			return false
		}
	}
	return configEnabled
}

// Append writes one Entry as a JSONL line. Creates the
// .reconc/audit.jsonl file (and its parent dir) on first use. If the
// file is larger than maxSizeBytes after this write, it triggers
// rotation. Silent no-op if repoRoot is empty.
//
// The bounded JSONL writer serializes concurrent processes through a file
// lock and rotates before append, so files never overshoot the cap.
func Append(repoRoot string, entry Entry, maxSizeBytes int64) error {
	if repoRoot == "" {
		return nil
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	entry = normalizeEntry(entry)
	if maxSizeBytes <= 0 {
		maxSizeBytes = DefaultMaxSizeBytes
	}
	path := filepath.Join(repoRoot, AuditFileRelative)
	layout, err := prepareAuditLayout(repoRoot)
	if err != nil {
		return err
	}
	policy := jsonl.Policy{MaxBytes: maxSizeBytes, MaxArchives: MaxArchiveFiles}
	var line []byte
	var prepared bool
	var rebuildHead bool
	var previousHead *chainHead
	prepare := func() ([]byte, error) {
		head, last, err := loadAppendCheckpoint(repoRoot)
		if err != nil {
			return nil, err
		}
		if last == nil {
			if head != nil {
				return nil, errors.New("audit head exists without retained records")
			}
			entry.Sequence = 1
			entry.PreviousDigest = ""
		} else {
			if last.Sequence == ^uint64(0) {
				return nil, errors.New("audit sequence is exhausted")
			}
			entry.Sequence = last.Sequence + 1
			entry.PreviousDigest = last.Digest
		}
		entry.ChainVersion = auditChainVersion
		entry.Digest = ""
		digest, err := entryDigest(entry)
		if err != nil {
			return nil, err
		}
		entry.Digest = digest
		line, err = json.Marshal(entry)
		if err != nil {
			return nil, fmt.Errorf("marshal chained audit entry: %w", err)
		}
		if len(line)+1 > maxRecordBytes {
			return nil, fmt.Errorf("bounded record is %d bytes; maximum is %d", len(line)+1, maxRecordBytes)
		}
		rotation, err := appendRequiresRotation(path, len(line)+1, maxSizeBytes)
		if err != nil {
			return nil, err
		}
		if rotation {
			if _, _, err := loadVerifiedSnapshot(repoRoot); err != nil {
				return nil, err
			}
		}
		previousHead = head
		rebuildHead = rotation
		prepared = true
		return line, nil
	}
	commit := func() error {
		if !prepared || rebuildHead {
			return rebuildChainHead(repoRoot)
		}
		return advanceChainHead(repoRoot, previousHead, entry)
	}
	if err := jsonl.AppendTransactionWithLayout(path, policy, layout, prepare, commit); err != nil {
		return fmt.Errorf("audit: append: %w", err)
	}
	return nil
}

// EnforceRetention verifies the writer-owned bounded ring without rewriting
// evidence. Append owns rotation and detached-head publication; a generic
// JSONL compactor cannot safely rewrite this chained format.
func EnforceRetention(repoRoot string) (jsonl.EnforceResult, error) {
	if _, err := Verify(repoRoot); err != nil {
		return jsonl.EnforceResult{}, err
	}
	return jsonl.EnforceResult{}, nil
}

// InspectRetention validates the private audit layout and reports any generic
// JSONL cleanup that would be possible, while holding the audit lock for the
// complete validated snapshot and inspection.
func InspectRetention(repoRoot string) (jsonl.EnforceResult, error) {
	if err := recoverPendingAppend(repoRoot); err != nil {
		return jsonl.EnforceResult{}, err
	}
	var result jsonl.EnforceResult
	err := withAuditLock(repoRoot, func() error {
		if _, _, err := loadVerifiedSnapshot(repoRoot); err != nil {
			return err
		}
		var err error
		result, err = jsonl.Inspect(filepath.Join(repoRoot, AuditFileRelative), jsonl.Policy{
			MaxBytes: DefaultMaxSizeBytes, MaxArchives: MaxArchiveFiles,
		})
		return err
	})
	return result, err
}

// TailOptions controls what Tail reads.
type TailOptions struct {
	// N is the maximum number of records to return. 0 = unlimited.
	N int
	// RuleID filters: only entries that include this rule id in
	// RuleIDs. Empty = no filter.
	RuleID string
	// Since filters to entries with ts >= this value (RFC3339). Empty = no filter.
	Since string
	// Decision filters to entries whose Decision matches (pass/warn/block).
	Decision string
}

// Tail returns the last N records matching the filter. The file is
// scanned fully (simple linear read); the fixed ring caps the scan at
// 6 MiB. We don't maintain an index because
// append-only JSONL is self-describing and the cost-of-read is bounded
// by the rotation cap.
func Tail(repoRoot string, opts TailOptions) ([]Entry, error) {
	if repoRoot == "" {
		return nil, nil
	}
	if err := recoverPendingAppend(repoRoot); err != nil {
		return nil, err
	}
	var since *time.Time
	if strings.TrimSpace(opts.Since) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, opts.Since)
		if err != nil {
			return nil, fmt.Errorf("audit: since must be RFC3339: %w", err)
		}
		since = &parsed
	}
	all, _, err := readVerifiedSnapshot(repoRoot)
	if err != nil {
		return nil, err
	}
	filtered := make([]Entry, 0, len(all))
	for _, entry := range all {
		if matchesFilters(entry, opts, since) {
			filtered = append(filtered, entry)
		}
	}

	if opts.N > 0 && len(filtered) > opts.N {
		filtered = filtered[len(filtered)-opts.N:]
	}
	return filtered, nil
}

func matchesFilters(e Entry, opts TailOptions, since *time.Time) bool {
	if opts.RuleID != "" {
		hit := false
		for _, rid := range e.RuleIDs {
			if rid == opts.RuleID {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if opts.Decision != "" && e.Decision != opts.Decision {
		return false
	}
	if since != nil {
		timestamp, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err != nil || timestamp.Before(*since) {
			return false
		}
	}
	return true
}

// StatsReport is the summary returned by Stats.
type StatsReport struct {
	TotalEntries           int            `json:"total_entries"`
	FirstTS                string         `json:"first_ts,omitempty"`
	LastTS                 string         `json:"last_ts,omitempty"`
	LatestDecision         string         `json:"latest_decision,omitempty"`
	LatestBlockingCount    int            `json:"latest_blocking_count"`
	EntriesLastHour        int            `json:"entries_last_hour"`
	BlockingEntriesLast24h int            `json:"blocking_entries_last_24h"`
	ByDecision             map[string]int `json:"by_decision"`
	ByEvent                map[string]int `json:"by_event"`
	TopRules               []RuleCount    `json:"top_rules"`
	BlockingFires          int            `json:"blocking_fires"`
}

// RuleCount pairs a rule id with how many entries triggered it.
type RuleCount struct {
	RuleID string `json:"rule_id"`
	Count  int    `json:"count"`
}

// Stats scans the full log and aggregates a StatsReport. Deterministic
// ordering (top rules sorted by count desc, then id asc).
func Stats(repoRoot string) (*StatsReport, error) {
	entries, err := Tail(repoRoot, TailOptions{})
	if err != nil {
		return nil, err
	}
	out := &StatsReport{
		TotalEntries: len(entries),
		ByDecision:   map[string]int{},
		ByEvent:      map[string]int{},
	}
	ruleCounts := map[string]int{}
	now := time.Now().UTC()
	hourAgo := now.Add(-time.Hour)
	dayAgo := now.Add(-24 * time.Hour)
	for i, e := range entries {
		if i == 0 {
			out.FirstTS = e.Timestamp
		}
		out.LastTS = e.Timestamp
		out.LatestDecision = e.Decision
		out.LatestBlockingCount = e.BlockingCount
		out.ByDecision[e.Decision]++
		out.ByEvent[e.Event]++
		if e.BlockingCount > 0 {
			out.BlockingFires++
		}
		if ts, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
			if !ts.Before(hourAgo) {
				out.EntriesLastHour++
			}
			if e.BlockingCount > 0 && !ts.Before(dayAgo) {
				out.BlockingEntriesLast24h++
			}
		}
		for _, rid := range e.RuleIDs {
			ruleCounts[rid]++
		}
	}
	out.TopRules = make([]RuleCount, 0, len(ruleCounts))
	for id, c := range ruleCounts {
		out.TopRules = append(out.TopRules, RuleCount{RuleID: id, Count: c})
	}
	sort.Slice(out.TopRules, func(i, j int) bool {
		if out.TopRules[i].Count != out.TopRules[j].Count {
			return out.TopRules[i].Count > out.TopRules[j].Count
		}
		return out.TopRules[i].RuleID < out.TopRules[j].RuleID
	})
	if len(out.TopRules) > 20 {
		out.TopRules = out.TopRules[:20]
	}
	return out, nil
}

// ExportJSONL writes the full log to w as-is. Useful for CSV export
// tooling or cross-repo aggregation.
func ExportJSONL(repoRoot string, w io.Writer) error {
	if err := recoverPendingAppend(repoRoot); err != nil {
		return err
	}
	return withAuditLock(repoRoot, func() error {
		if _, _, err := loadVerifiedSnapshot(repoRoot); err != nil {
			return err
		}
		path := filepath.Join(repoRoot, AuditFileRelative)
		sources, err := jsonl.PathsOldestFirst(path, MaxArchiveFiles)
		if err != nil {
			return fmt.Errorf("audit: enumerate archive ring: %w", err)
		}
		for _, source := range sources {
			body, err := boundedio.ReadRegularFile(source, DefaultMaxSizeBytes)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return err
			}
			written, err := w.Write(body)
			if err != nil {
				return err
			}
			if written != len(body) {
				return io.ErrShortWrite
			}
		}
		return nil
	})
}

// Verify validates every retained record, the linear sequence and digest
// links, and the detached first/last head anchors.
func Verify(repoRoot string) (VerificationReport, error) {
	if repoRoot == "" {
		return VerificationReport{Valid: true}, nil
	}
	if err := recoverPendingAppend(repoRoot); err != nil {
		return VerificationReport{}, err
	}
	var report VerificationReport
	err := withAuditLock(repoRoot, func() error {
		entries, _, err := loadVerifiedSnapshot(repoRoot)
		if err != nil {
			return err
		}
		report.Valid = true
		report.Entries = len(entries)
		if len(entries) > 0 {
			report.FirstSequence = entries[0].Sequence
			report.LastSequence = entries[len(entries)-1].Sequence
			report.LastDigest = entries[len(entries)-1].Digest
		}
		return nil
	})
	return report, err
}

func loadAppendCheckpoint(repoRoot string) (*chainHead, *Entry, error) {
	head, err := readChainHead(repoRoot)
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(repoRoot, AuditFileRelative)
	last, exists, err := readLastAuditEntry(path)
	if err != nil {
		return nil, nil, err
	}
	if !exists {
		sources, err := jsonl.PathsOldestFirst(path, MaxArchiveFiles)
		if err != nil {
			return nil, nil, err
		}
		if len(sources) == 0 {
			if head != nil {
				return nil, nil, errors.New("audit: detached head exists without retained records")
			}
			return nil, nil, nil
		}
		entries, verifiedHead, err := loadVerifiedSnapshot(repoRoot)
		if err != nil {
			return nil, nil, err
		}
		if len(entries) == 0 {
			return nil, nil, errors.New("audit: retained files contain no records")
		}
		lastEntry := entries[len(entries)-1]
		return verifiedHead, &lastEntry, nil
	}
	if err := verifyAppendCheckpoint(*last, head); err != nil {
		return nil, nil, err
	}
	return head, last, nil
}

func readLastAuditEntry(path string) (*Entry, bool, error) {
	var entry Entry
	found := false
	err := boundedio.WithRegularFileSnapshot(path, DefaultMaxSizeBytes, func(file *os.File, info os.FileInfo) error {
		if info.Size() == 0 {
			return nil
		}
		readBytes := int64(maxRecordBytes)
		if info.Size() < readBytes {
			readBytes = info.Size()
		}
		if _, err := file.Seek(-readBytes, io.SeekEnd); err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, readBytes))
		if readErr != nil {
			return readErr
		}
		if int64(len(data)) != readBytes {
			return errors.New("audit: live log changed while reading its tail")
		}
		if data[len(data)-1] != '\n' {
			return errors.New("audit: live log contains a truncated tail record")
		}
		lineStart := bytes.LastIndexByte(data[:len(data)-1], '\n') + 1
		line := data[lineStart : len(data)-1]
		if len(line) == 0 {
			return errors.New("audit: live log ends with an empty record")
		}
		if err := decodeStrictJSON(line, &entry); err != nil {
			return fmt.Errorf("audit: live tail contains malformed JSON: %w", err)
		}
		found = true
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("audit: read live tail: %w", err)
	}
	if !found {
		return nil, false, nil
	}
	return &entry, found, nil
}

func verifyAppendCheckpoint(last Entry, head *chainHead) error {
	if head == nil {
		return errors.New("audit: retained records have no detached head")
	}
	if err := verifyEntryChain([]Entry{last}); err != nil {
		return err
	}
	if head.ChainVersion != auditChainVersion || head.EntryCount <= 0 || head.FirstSequence == 0 || head.LastSequence < head.FirstSequence {
		return errors.New("audit: detached head has invalid chain metadata")
	}
	if head.LastSequence-head.FirstSequence+1 != uint64(head.EntryCount) {
		return errors.New("audit: detached head entry count is not contiguous")
	}
	for _, digest := range []string{head.FirstDigest, head.LastDigest} {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size {
			return errors.New("audit: detached head contains an invalid digest")
		}
	}
	if head.LastSequence != last.Sequence || head.LastDigest != last.Digest {
		return errors.New("audit: detached head does not match the live tail")
	}
	if head.EntryCount == 1 && (head.FirstSequence != last.Sequence || head.FirstDigest != last.Digest) {
		return errors.New("audit: single-entry detached head does not match the live tail")
	}
	return nil
}

func appendRequiresRotation(path string, recordBytes int, maxSizeBytes int64) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Size()+int64(recordBytes) > maxSizeBytes, nil
}

func rebuildChainHead(repoRoot string) error {
	entries, err := readAuditEntries(filepath.Join(repoRoot, AuditFileRelative))
	if err != nil {
		return err
	}
	if err := verifyEntryChain(entries); err != nil {
		return err
	}
	return writeChainHead(repoRoot, entries)
}

func advanceChainHead(repoRoot string, previous *chainHead, entry Entry) error {
	head := chainHead{
		ChainVersion:  auditChainVersion,
		FirstSequence: entry.Sequence,
		FirstDigest:   entry.Digest,
		LastSequence:  entry.Sequence,
		LastDigest:    entry.Digest,
		EntryCount:    1,
	}
	if previous != nil {
		head.FirstSequence = previous.FirstSequence
		head.FirstDigest = previous.FirstDigest
		head.EntryCount = previous.EntryCount + 1
	}
	return writeChainHeadValue(repoRoot, head)
}

func recoverPendingAppend(repoRoot string) error {
	path := filepath.Join(repoRoot, AuditFileRelative)
	layout, err := prepareAuditLayout(repoRoot)
	if err != nil {
		return err
	}
	commit := func() error {
		entries, err := readAuditEntries(path)
		if err != nil {
			return err
		}
		if err := verifyEntryChain(entries); err != nil {
			return err
		}
		return writeChainHead(repoRoot, entries)
	}
	err = jsonl.RecoverWithLayout(path, layout, commit)
	if err == nil || !strings.Contains(err.Error(), "belongs to a different layout") {
		return err
	}
	// A pre-private audit journal used the generic JSONL layout. Recover it
	// explicitly once, then publish the detached head under the private layout.
	return jsonl.Recover(path, commit)
}

func readVerifiedSnapshot(repoRoot string) ([]Entry, *chainHead, error) {
	var entries []Entry
	var head *chainHead
	err := withAuditLock(repoRoot, func() error {
		var err error
		entries, head, err = loadVerifiedSnapshot(repoRoot)
		return err
	})
	return entries, head, err
}

func loadVerifiedSnapshot(repoRoot string) ([]Entry, *chainHead, error) {
	path := filepath.Join(repoRoot, AuditFileRelative)
	layout, err := auditLayoutForRead(repoRoot)
	if err != nil {
		return nil, nil, err
	}
	if err := validateAuditContentFiles(path, layout); err != nil {
		return nil, nil, err
	}
	entries, err := readAuditEntries(path)
	if err != nil {
		return nil, nil, err
	}
	head, err := readChainHead(repoRoot)
	if err != nil {
		return nil, nil, err
	}
	if err := verifyEntryChain(entries); err != nil {
		return nil, nil, err
	}
	if err := verifyChainHead(entries, head); err != nil {
		return nil, nil, err
	}
	return entries, head, nil
}

func readAuditEntries(path string) ([]Entry, error) {
	sources, err := jsonl.PathsOldestFirst(path, MaxArchiveFiles)
	if err != nil {
		return nil, fmt.Errorf("audit: enumerate archive ring: %w", err)
	}
	entries := []Entry{}
	for _, source := range sources {
		data, err := boundedio.ReadRegularFile(source, DefaultMaxSizeBytes)
		if err != nil {
			return nil, fmt.Errorf("audit: read %s: %w", source, err)
		}
		if len(data) > 0 && data[len(data)-1] != '\n' {
			return nil, fmt.Errorf("audit: %s contains a truncated record without a final newline", source)
		}
		for index, line := range bytes.Split(data, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			var entry Entry
			if err := decodeStrictJSON(line, &entry); err != nil {
				return nil, fmt.Errorf("audit: %s:%d contains malformed JSON: %w", source, index+1, err)
			}
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func verifyEntryChain(entries []Entry) error {
	for index, entry := range entries {
		if entry.ChainVersion != auditChainVersion {
			return fmt.Errorf("audit: entry %d has unsupported or missing chain_version", index+1)
		}
		if entry.Sequence == 0 {
			return fmt.Errorf("audit: entry %d has invalid sequence", index+1)
		}
		if _, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err != nil {
			return fmt.Errorf("audit: entry %d has invalid timestamp: %w", index+1, err)
		}
		expected, err := entryDigest(entry)
		if err != nil {
			return fmt.Errorf("audit: entry %d digest: %w", index+1, err)
		}
		if entry.Digest != expected {
			return fmt.Errorf("audit: entry %d digest does not match its contents", index+1)
		}
		if index == 0 {
			continue
		}
		previous := entries[index-1]
		if entry.Sequence != previous.Sequence+1 {
			return fmt.Errorf("audit: entry %d sequence is not contiguous", index+1)
		}
		if entry.PreviousDigest != previous.Digest {
			return fmt.Errorf("audit: entry %d previous_digest does not match entry %d", index+1, index)
		}
	}
	return nil
}

func entryDigest(entry Entry) (string, error) {
	entry.Digest = ""
	body, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return fmt.Sprintf("%x", digest[:]), nil
}

func verifyChainHead(entries []Entry, head *chainHead) error {
	if len(entries) == 0 {
		if head != nil {
			return errors.New("audit: detached head exists without retained records")
		}
		return nil
	}
	if head == nil {
		return errors.New("audit: retained records have no detached head")
	}
	first := entries[0]
	last := entries[len(entries)-1]
	if head.ChainVersion != auditChainVersion ||
		head.EntryCount != len(entries) ||
		head.FirstSequence != first.Sequence ||
		head.FirstDigest != first.Digest ||
		head.LastSequence != last.Sequence ||
		head.LastDigest != last.Digest {
		return errors.New("audit: detached head does not match the retained chain")
	}
	return nil
}

func readChainHead(repoRoot string) (*chainHead, error) {
	path := filepath.Join(repoRoot, AuditHeadRelative)
	if err := validateAuditHead(path); err != nil {
		return nil, fmt.Errorf("audit: validate detached head security: %w", err)
	}
	data, err := boundedio.ReadRegularFile(path, auditHeadMaxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("audit: read detached head: %w", err)
	}
	var head chainHead
	if err := decodeStrictJSON(data, &head); err != nil {
		return nil, fmt.Errorf("audit: detached head is malformed: %w", err)
	}
	return &head, nil
}

func decodeStrictJSON(data []byte, target interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func writeChainHead(repoRoot string, entries []Entry) error {
	if len(entries) == 0 {
		return errors.New("audit: cannot write a detached head for an empty chain")
	}
	first := entries[0]
	last := entries[len(entries)-1]
	head := chainHead{
		ChainVersion:  auditChainVersion,
		FirstSequence: first.Sequence,
		FirstDigest:   first.Digest,
		LastSequence:  last.Sequence,
		LastDigest:    last.Digest,
		EntryCount:    len(entries),
	}
	return writeChainHeadValue(repoRoot, head)
}

func writeChainHeadValue(repoRoot string, head chainHead) error {
	body, err := json.MarshalIndent(head, "", "  ")
	if err != nil {
		return fmt.Errorf("audit: marshal detached head: %w", err)
	}
	body = append(body, '\n')
	if _, err := privatefs.WritePrivateIfChanged(filepath.Join(repoRoot, AuditHeadRelative), body, 0o600); err != nil {
		return fmt.Errorf("audit: write detached head: %w", err)
	}
	return nil
}

func withAuditLock(repoRoot string, fn func() error) error {
	layout, err := prepareAuditLayout(repoRoot)
	if err != nil {
		return err
	}
	lock, err := privatefs.OpenLock(layout.LockPath)
	if err != nil {
		return err
	}
	defer lock.Close()
	unlock, err := filelock.LockContext(context.Background(), lock, layout.LockTimeout)
	if err != nil {
		return err
	}
	fnErr := fn()
	unlockErr := unlock()
	if fnErr != nil {
		return fnErr
	}
	if unlockErr != nil {
		return fmt.Errorf("audit: unlock: %w", unlockErr)
	}
	return nil
}

func normalizeEntry(entry Entry) Entry {
	entry.Event = truncateRunes(strings.TrimSpace(entry.Event), 128)
	entry.Decision = truncateRunes(strings.TrimSpace(entry.Decision), 128)
	entry.RuleIDs = boundedStrings(entry.RuleIDs)
	entry.WritePaths = boundedStrings(entry.WritePaths)
	entry.ReadPaths = boundedStrings(entry.ReadPaths)
	entry.Claims = boundedStrings(entry.Claims)
	commands := make([]string, 0, len(entry.Commands))
	for _, command := range entry.Commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		if !auditVerbose() {
			command = strings.Fields(command)[0]
		}
		commands = append(commands, truncateRunes(command, 4096))
	}
	entry.Commands = boundedStrings(commands)
	entry.RepoRoot = truncateRunes(strings.TrimSpace(entry.RepoRoot), 4096)
	entry.LockfileDigest = truncateRunes(strings.TrimSpace(entry.LockfileDigest), 256)
	entry.ReconcVersion = truncateRunes(strings.TrimSpace(entry.ReconcVersion), 128)
	entry.Agent = truncateRunes(strings.TrimSpace(entry.Agent), 256)
	return entry
}

func boundedStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	bytesUsed := 0
	for _, value := range values {
		value = truncateRunes(strings.TrimSpace(value), 4096)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		if len(out) >= maxEntryListItems || bytesUsed+len(value) > maxEntryListBytes {
			break
		}
		seen[value] = struct{}{}
		out = append(out, value)
		bytesUsed += len(value)
	}
	return out
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func auditVerbose() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("RECONC_AUDIT_VERBOSE"))) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}
