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
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/jsonl"
)

// Relative path under the repo root where the log lives.
const AuditFileRelative = ".reconc/audit.jsonl"

const (
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
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("audit: marshal: %w", err)
	}
	if len(line)+1 > maxRecordBytes {
		return fmt.Errorf("audit: bounded record is %d bytes; maximum is %d", len(line)+1, maxRecordBytes)
	}
	if maxSizeBytes <= 0 {
		maxSizeBytes = DefaultMaxSizeBytes
	}
	path := filepath.Join(repoRoot, AuditFileRelative)
	if err := jsonl.Append(path, line, jsonl.Policy{MaxBytes: maxSizeBytes, MaxArchives: MaxArchiveFiles}); err != nil {
		return fmt.Errorf("audit: append: %w", err)
	}
	return nil
}

// EnforceRetention compacts legacy oversized audit files and removes
// archives outside the fixed ring.
func EnforceRetention(repoRoot string) (jsonl.EnforceResult, error) {
	path := filepath.Join(repoRoot, AuditFileRelative)
	return jsonl.Enforce(path, jsonl.Policy{MaxBytes: DefaultMaxSizeBytes, MaxArchives: MaxArchiveFiles})
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
	var all []Entry
	path := filepath.Join(repoRoot, AuditFileRelative)
	for _, source := range jsonl.PathsOldestFirst(path, MaxArchiveFiles) {
		if err := scanEntries(source, opts, &all); err != nil {
			return nil, err
		}
	}

	if opts.N > 0 && len(all) > opts.N {
		all = all[len(all)-opts.N:]
	}
	return all, nil
}

func scanEntries(path string, opts TailOptions, all *[]Entry) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("audit: open: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 32*1024), maxRecordBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(line, &entry); err != nil || !matchesFilters(entry, opts) {
			continue
		}
		*all = append(*all, entry)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("audit: scan: %w", err)
	}
	return nil
}

func matchesFilters(e Entry, opts TailOptions) bool {
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
	if opts.Since != "" {
		// RFC3339 lexical ordering == chronological for same-offset
		// timestamps. Our Append() always uses UTC so this is safe.
		if e.Timestamp < opts.Since {
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
	path := filepath.Join(repoRoot, AuditFileRelative)
	for _, source := range jsonl.PathsOldestFirst(path, MaxArchiveFiles) {
		file, err := os.Open(source)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		_, copyErr := io.Copy(w, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
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
