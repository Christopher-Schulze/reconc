// Package audit implements the optional policy-decision audit log (W29).
//
// When enabled, every non-trivial enforcement decision (check, ci,
// assert, can) appends one JSONL record to .reconc/audit.jsonl. The
// log is append-only, crash-safe (O_APPEND writes are atomic for small
// records on POSIX), and self-rotating once it exceeds the configured
// size cap.
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
)

// Relative path under the repo root where the log lives.
const AuditFileRelative = ".reconc/audit.jsonl"

// DefaultMaxSizeBytes triggers rotation. When the log exceeds this, it
// gets renamed to .reconc/audit.jsonl.N (where N = next free integer)
// and a fresh empty file is created.
const DefaultMaxSizeBytes = 50 * 1024 * 1024 // 50 MiB

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
// The call is append-only (O_APPEND), so concurrent reconc invocations
// can log to the same file without cross-line corruption for records
// smaller than the kernel's PIPE_BUF (typically 4 KiB on Linux/macOS).
func Append(repoRoot string, entry Entry, maxSizeBytes int64) error {
	if repoRoot == "" {
		return nil
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	path := filepath.Join(repoRoot, AuditFileRelative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("audit: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("audit: open: %w", err)
	}
	defer f.Close()
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("audit: marshal: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("audit: write: %w", err)
	}

	// Best-effort rotation check AFTER the write so the log always
	// contains the current entry. Rotation failures propagate as a
	// non-fatal "append-succeeded-but-rotate-failed" error so callers
	// can log / alert. The record IS still persisted -- losing one
	// rotation is strictly better than losing the current entry.
	if maxSizeBytes <= 0 {
		maxSizeBytes = DefaultMaxSizeBytes
	}
	if info, err := f.Stat(); err == nil && info.Size() > maxSizeBytes {
		if rerr := rotate(path); rerr != nil {
			return fmt.Errorf("audit: append succeeded but rotation failed: %w", rerr)
		}
	}
	return nil
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
// scanned fully (simple linear read); for a 50MiB log that's ~100ms
// which is fine for CLI use. We don't maintain an index because
// append-only JSONL is self-describing and the cost-of-read is bounded
// by the rotation cap.
func Tail(repoRoot string, opts TailOptions) ([]Entry, error) {
	if repoRoot == "" {
		return nil, nil
	}
	path := filepath.Join(repoRoot, AuditFileRelative)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Entry{}, nil
		}
		return nil, fmt.Errorf("audit: open: %w", err)
	}
	defer f.Close()

	var all []Entry
	sc := bufio.NewScanner(f)
	// Default scanner buffer is 64KiB; raise to 1MiB so a pathological
	// very-long record doesn't stop the scan.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			// Skip malformed lines rather than failing the whole query;
			// the log is meant to survive partial writes on crash.
			continue
		}
		if !matchesFilters(e, opts) {
			continue
		}
		all = append(all, e)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("audit: scan: %w", err)
	}

	if opts.N > 0 && len(all) > opts.N {
		all = all[len(all)-opts.N:]
	}
	return all, nil
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
	TotalEntries  int            `json:"total_entries"`
	FirstTS       string         `json:"first_ts,omitempty"`
	LastTS        string         `json:"last_ts,omitempty"`
	ByDecision    map[string]int `json:"by_decision"`
	ByEvent       map[string]int `json:"by_event"`
	TopRules      []RuleCount    `json:"top_rules"`
	BlockingFires int            `json:"blocking_fires"`
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
	for i, e := range entries {
		if i == 0 {
			out.FirstTS = e.Timestamp
		}
		out.LastTS = e.Timestamp
		out.ByDecision[e.Decision]++
		out.ByEvent[e.Event]++
		if e.BlockingCount > 0 {
			out.BlockingFires++
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

// rotate moves path to path.N where N is the smallest free integer.
// Used by Append when the log grows past the size cap.
func rotate(path string) error {
	for n := 1; n < 1000; n++ {
		dst := fmt.Sprintf("%s.%d", path, n)
		if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
			return os.Rename(path, dst)
		}
	}
	return errors.New("audit: rotation exhausted 1000 suffixes; cleanup old archives")
}

// ExportJSONL writes the full log to w as-is. Useful for CSV export
// tooling or cross-repo aggregation.
func ExportJSONL(repoRoot string, w io.Writer) error {
	path := filepath.Join(repoRoot, AuditFileRelative)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}
