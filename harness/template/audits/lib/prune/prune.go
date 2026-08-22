// Package prune is the compatibility cleanup used by older bootstraps.
//
// Current Reconc binaries own full lifecycle retention. This package keeps the
// former narrow manual utility safe for repositories that have not refreshed
// their bootstrap yet. It covers five legacy classes:
//   - sessions JSONs at ~/.reconc/sessions/claude/projects/<key>/sessions/
//   - reports JSONs at  ~/.reconc/sessions/claude/projects/<key>/reports/
//   - staged command proofs at ~/.reconc/sessions/claude/projects/<key>/command-proofs/
//   - the append-only audit log at <repo>/.reconc/audit.jsonl
//   - locks at ~/.reconc/sessions/claude/projects/<key>/locks/ (transient)
//
// `Run` keeps the newest N session/report files (count-based) and trims the
// JSONL log to the most recent lines that fit a byte budget AND a line-count
// budget. Locks older than 24h are also removed (defensive: stale lock from
// a crashed agent).
//
// All operations are idempotent and fail-safe: I/O errors on individual
// files are recorded in the Report but never abort the whole run, so a
// transient permission glitch does not break the audit cache that triggers
// us.
package prune

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	maxPrunePolicyBytes    = 1 << 20
	maxPruneDirectoryItems = 4_096
	maxPruneRetention      = 4_096
	maxPruneAuditLogBytes  = 64 << 20
	maxPruneAuditLogLines  = 100_000
	maxPruneInterval       = 7 * 24 * 60 * 60
)

// Policy holds the retention thresholds. Zero-value defaults to a safe
// budget so a missing YAML file does not silently disable pruning.
type Policy struct {
	SessionsRetention      int   `yaml:"sessions_retention"`
	ReportsRetention       int   `yaml:"reports_retention"`
	CommandProofsRetention int   `yaml:"command_proofs_retention"`
	AuditJsonlMaxBytes     int64 `yaml:"audit_jsonl_max_bytes"`
	AuditJsonlMaxLines     int   `yaml:"audit_jsonl_max_lines"`
	PruneIntervalSeconds   int64 `yaml:"prune_interval_seconds"`
}

// DefaultPolicy mirrors the current compatibility YAML. Product-core
// retention has additional byte, age, archive, temp, and total budgets.
func DefaultPolicy() Policy {
	return Policy{
		SessionsRetention:      32,
		ReportsRetention:       32,
		CommandProofsRetention: 64,
		AuditJsonlMaxBytes:     2_097_152,
		AuditJsonlMaxLines:     5_000,
		PruneIntervalSeconds:   21_600,
	}
}

// LoadPolicy reads the YAML at path. Missing / malformed / partial content
// is tolerated by merging with DefaultPolicy so the caller always gets a
// fully-populated, validated Policy.
func LoadPolicy(path string) (Policy, error) {
	policy := DefaultPolicy()
	bytes, err := readPruneRegularFile(path, maxPrunePolicyBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return policy, nil
		}
		return policy, fmt.Errorf("read %s: %w", path, err)
	}
	var loaded Policy
	if err := yaml.Unmarshal(bytes, &loaded); err != nil {
		return policy, fmt.Errorf("parse %s: %w", path, err)
	}
	if loaded.SessionsRetention > 0 {
		policy.SessionsRetention = loaded.SessionsRetention
	}
	if loaded.ReportsRetention > 0 {
		policy.ReportsRetention = loaded.ReportsRetention
	}
	if loaded.CommandProofsRetention > 0 {
		policy.CommandProofsRetention = loaded.CommandProofsRetention
	}
	if loaded.AuditJsonlMaxBytes > 0 {
		policy.AuditJsonlMaxBytes = loaded.AuditJsonlMaxBytes
	}
	if loaded.AuditJsonlMaxLines > 0 {
		policy.AuditJsonlMaxLines = loaded.AuditJsonlMaxLines
	}
	if loaded.PruneIntervalSeconds > 0 {
		policy.PruneIntervalSeconds = loaded.PruneIntervalSeconds
	}
	return policy, nil
}

// Report describes what one Run did.
type Report struct {
	SessionsDeleted      int
	SessionsKept         int
	ReportsDeleted       int
	ReportsKept          int
	CommandProofsDeleted int
	CommandProofsKept    int
	LocksDeleted         int
	JsonlLinesDropped    int
	JsonlBytesFreed      int64
	Errors               []string
}

// Options configures a Run. RepoRoot is used to locate audit.jsonl and to
// derive the project key for ~/.reconc/sessions/. ReconcHome overrides the
// default ~/.reconc base (mirrors the agentsession layer's StateRootEnv).
type Options struct {
	RepoRoot   string
	ReconcHome string
	Policy     Policy
	DryRun     bool
}

// Run executes the prune. Idempotent: a second call with the same inputs
// returns a Report with all zero counts (nothing to delete).
func Run(opts Options) Report {
	report := Report{}
	policy := normalizedPolicy(opts.Policy, &report)
	repoRoot, err := resolveRepositoryIdentity(opts.RepoRoot)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("resolve repository root %s: %v", opts.RepoRoot, err))
		return report
	}
	stateRoot := resolveStateRoot(opts.ReconcHome)
	if stateRoot != "" {
		resolvedStateRoot, err := filepath.EvalSymlinks(stateRoot)
		if err == nil {
			projectDir := filepath.Join(resolvedStateRoot, "projects", projectKey(repoRoot))
			if err := validatePruneDescendant(resolvedStateRoot, projectDir); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("skip state retention: %v", err))
			} else {
				report.SessionsKept, report.SessionsDeleted = pruneDir(filepath.Join(projectDir, "sessions"), policy.SessionsRetention, opts.DryRun, &report)
				report.ReportsKept, report.ReportsDeleted = pruneDir(filepath.Join(projectDir, "reports"), policy.ReportsRetention, opts.DryRun, &report)
				report.CommandProofsKept, report.CommandProofsDeleted = pruneDir(filepath.Join(projectDir, "command-proofs"), policy.CommandProofsRetention, opts.DryRun, &report)
				report.LocksDeleted = pruneStaleLocks(filepath.Join(projectDir, "locks"), 24*time.Hour, opts.DryRun, &report)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			report.Errors = append(report.Errors, fmt.Sprintf("resolve state root %s: %v", stateRoot, err))
		}
	}
	jsonlPath := filepath.Join(repoRoot, ".reconc", "audit.jsonl")
	if err := validatePruneDescendant(repoRoot, jsonlPath); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("skip audit retention: %v", err))
		return report
	}
	dropped, freed := trimJsonl(jsonlPath, policy.AuditJsonlMaxBytes, policy.AuditJsonlMaxLines, opts.DryRun, &report)
	report.JsonlLinesDropped = dropped
	report.JsonlBytesFreed = freed
	return report
}

func resolveRepositoryIdentity(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	before, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !before.IsDir() {
		return "", fmt.Errorf("must be a directory")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	after, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !after.IsDir() || !os.SameFile(before, after) {
		return "", fmt.Errorf("filesystem identity changed while resolving")
	}
	return canonicalizeRepositoryCase(filepath.Clean(resolved), after), nil
}

func canonicalizeRepositoryCase(path string, identity os.FileInfo) string {
	volume := filepath.VolumeName(path)
	rest := strings.Trim(strings.TrimPrefix(filepath.Clean(path), volume), string(filepath.Separator))
	canonical := volume + string(filepath.Separator)
	for _, component := range strings.Split(rest, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		resolved := component
		entries, err := readPruneDirectoryNames(canonical, 65_536)
		if err == nil {
			for _, name := range entries {
				if name == component {
					resolved = component
					break
				}
				if strings.EqualFold(name, component) {
					resolved = name
				}
			}
		}
		canonical = filepath.Join(canonical, resolved)
	}
	canonicalInfo, err := os.Stat(canonical)
	if err != nil || !os.SameFile(identity, canonicalInfo) {
		return path
	}
	return canonical
}

func readPruneDirectoryNames(path string, limit int) ([]string, error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	entries, readErr := directory.ReadDir(limit + 1)
	closeErr := directory.Close()
	if err := errors.Join(readErr, closeErr); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > limit {
		return nil, fmt.Errorf("directory exceeds %d entries", limit)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, nil
}

func validatePruneDescendant(base, target string) error {
	relative, err := filepath.Rel(base, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %s escapes retention root %s", target, base)
	}
	current := filepath.Clean(base)
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("retention path component must not be a symlink: %s", current)
		}
	}
	return nil
}

func normalizedPolicy(input Policy, report *Report) Policy {
	policy := DefaultPolicy()
	applyInt := func(name string, value, limit int, target *int) {
		switch {
		case value == 0:
		case value < 0 || value > limit:
			report.Errors = append(report.Errors, fmt.Sprintf("%s must be between 1 and %d; using default", name, limit))
		default:
			*target = value
		}
	}
	applyInt64 := func(name string, value, limit int64, target *int64) {
		switch {
		case value == 0:
		case value < 0 || value > limit:
			report.Errors = append(report.Errors, fmt.Sprintf("%s must be between 1 and %d; using default", name, limit))
		default:
			*target = value
		}
	}
	applyInt("sessions_retention", input.SessionsRetention, maxPruneRetention, &policy.SessionsRetention)
	applyInt("reports_retention", input.ReportsRetention, maxPruneRetention, &policy.ReportsRetention)
	applyInt("command_proofs_retention", input.CommandProofsRetention, maxPruneRetention, &policy.CommandProofsRetention)
	applyInt64("audit_jsonl_max_bytes", input.AuditJsonlMaxBytes, maxPruneAuditLogBytes, &policy.AuditJsonlMaxBytes)
	applyInt("audit_jsonl_max_lines", input.AuditJsonlMaxLines, maxPruneAuditLogLines, &policy.AuditJsonlMaxLines)
	applyInt64("prune_interval_seconds", input.PruneIntervalSeconds, maxPruneInterval, &policy.PruneIntervalSeconds)
	return policy
}

func resolveStateRoot(reconcHome string) string {
	if reconcHome != "" {
		return filepath.Join(reconcHome, "sessions", "claude")
	}
	if env := os.Getenv("RECONC_CLAUDE_STATE_DIR"); env != "" {
		return env
	}
	if env := os.Getenv("RECONC_HOME"); env != "" {
		return filepath.Join(env, "sessions", "claude")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".reconc", "sessions", "claude")
	}
	return ""
}

func readPruneRegularFile(path string, maxBytes int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("must be a non-symlink regular file")
	}
	if before.Size() > maxBytes {
		return nil, fmt.Errorf("exceeds %d bytes", maxBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	afterFile, statErr := file.Stat()
	afterPath, lstatErr := os.Lstat(path)
	closeErr := file.Close()
	if err := errors.Join(readErr, statErr, lstatErr, closeErr); err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("exceeds %d bytes", maxBytes)
	}
	if !os.SameFile(before, afterFile) || !os.SameFile(afterFile, afterPath) ||
		before.Mode() != afterFile.Mode() || before.Size() != afterFile.Size() ||
		!before.ModTime().Equal(afterFile.ModTime()) {
		return nil, fmt.Errorf("changed while reading")
	}
	return body, nil
}

func readPruneDirectory(path string, expected os.FileInfo) ([]os.DirEntry, error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, statErr := directory.Stat()
	if statErr != nil || !opened.IsDir() || !os.SameFile(expected, opened) {
		return nil, errors.Join(statErr, directory.Close(), fmt.Errorf("directory changed while opening"))
	}
	entries, readErr := directory.ReadDir(maxPruneDirectoryItems + 1)
	after, lstatErr := os.Lstat(path)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, errors.Join(readErr, lstatErr, closeErr)
	}
	if err := errors.Join(lstatErr, closeErr); err != nil {
		return nil, err
	}
	if !os.SameFile(opened, after) || opened.Mode() != after.Mode() ||
		opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("directory changed while reading")
	}
	if len(entries) > maxPruneDirectoryItems {
		return nil, fmt.Errorf("directory exceeds %d entries", maxPruneDirectoryItems)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// projectKey replicates Reconc's hash16(repoRoot) → first 16 hex chars of
// SHA-256 so we hit the exact directory the agentsession runtime writes to.
func projectKey(repoRoot string) string {
	sum := sha256.Sum256([]byte(repoRoot))
	return hex.EncodeToString(sum[:])[:16]
}

// pruneDir keeps the newest `keep` regular files in dir (by mtime), removes
// the rest. Returns (kept, deleted) counts. Missing dir → (0, 0), no error.
func pruneDir(dir string, keep int, dryRun bool, report *Report) (int, int) {
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, 0
		}
		report.Errors = append(report.Errors, fmt.Sprintf("read %s: %v", dir, err))
		return 0, 0
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		report.Errors = append(report.Errors, fmt.Sprintf("read %s: not a non-symlink directory", dir))
		return 0, 0
	}

	entries, err := readPruneDirectory(dir, info)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, 0
		}
		report.Errors = append(report.Errors, fmt.Sprintf("read %s: %v", dir, err))
		return 0, 0
	}
	type fileInfo struct {
		name  string
		mtime time.Time
		info  os.FileInfo
	}
	var files []fileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("stat %s/%s: %v", dir, entry.Name(), err))
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			report.Errors = append(report.Errors, fmt.Sprintf("skip non-regular retained state %s/%s", dir, entry.Name()))
			continue
		}
		files = append(files, fileInfo{name: entry.Name(), mtime: info.ModTime(), info: info})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.After(files[j].mtime) })
	if keep < 0 {
		keep = 0
	}
	if len(files) <= keep {
		return len(files), 0
	}
	deleted := 0
	for _, f := range files[keep:] {
		path := filepath.Join(dir, f.name)
		current, err := os.Lstat(path)
		if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
			!os.SameFile(f.info, current) || current.Mode() != f.info.Mode() ||
			current.Size() != f.info.Size() || !current.ModTime().Equal(f.info.ModTime()) {
			report.Errors = append(report.Errors, fmt.Sprintf("skip changed retained state %s", path))
			continue
		}
		if dryRun {
			deleted++
			continue
		}
		if err := os.Remove(path); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("remove %s: %v", path, err))
			continue
		}
		deleted++
	}
	return keep, deleted
}

// pruneStaleLocks removes lock files older than maxAge so a crashed agent
// does not block forever.
func pruneStaleLocks(dir string, maxAge time.Duration, dryRun bool, report *Report) int {
	dirInfo, err := os.Lstat(dir)
	if err != nil || dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		return 0
	}
	entries, err := readPruneDirectory(dir, dirInfo)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-maxAge)
	deleted := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		current, err := os.Lstat(path)
		if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
			!os.SameFile(info, current) || current.Mode() != info.Mode() ||
			current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
			continue
		}
		if dryRun {
			deleted++
			continue
		}
		if err := os.Remove(path); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("remove stale lock %s: %v", path, err))
			continue
		}
		deleted++
	}
	return deleted
}

// trimJsonl rewrites path to keep the last lines that fit within both
// maxBytes and maxLines. Whichever cap binds first wins. Lines longer than
// maxBytes are dropped (the audit log occasionally embeds a multi-megabyte
// commit diff which is useless for debugging anyway). Returns
// (linesDropped, bytesFreed).
func trimJsonl(path string, maxBytes int64, maxLines int, dryRun bool, report *Report) (int, int64) {
	stat, err := os.Lstat(path)
	if err != nil {
		return 0, 0
	}
	if stat.Mode()&os.ModeSymlink != 0 || !stat.Mode().IsRegular() {
		report.Errors = append(report.Errors, fmt.Sprintf("trim %s: audit log must be a non-symlink regular file", path))
		return 0, 0
	}
	if stat.Size() > maxPruneAuditLogBytes {
		report.Errors = append(report.Errors, fmt.Sprintf("trim %s: audit log exceeds %d-byte safe scan bound", path, maxPruneAuditLogBytes))
		return 0, 0
	}
	original := stat.Size()
	lineCount, err := countLinesQuick(path, stat)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("trim %s: %v", path, err))
		return 0, 0
	}
	if original <= maxBytes && lineCount <= maxLines {
		return 0, 0
	}
	tail, dropped, err := tailLinesWithinBudget(path, maxBytes, maxLines, stat)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("trim %s: %v", path, err))
		return 0, 0
	}
	if dryRun {
		return dropped, original - int64(len(tail))
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".audit-jsonl-*")
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("create tmp for %s: %v", path, err))
		return 0, 0
	}
	if _, err := tmp.Write(tail); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		report.Errors = append(report.Errors, fmt.Sprintf("write tmp for %s: %v", path, err))
		return 0, 0
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		report.Errors = append(report.Errors, fmt.Sprintf("close tmp for %s: %v", path, err))
		return 0, 0
	}
	current, err := os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(stat, current) || current.Mode() != stat.Mode() ||
		current.Size() != stat.Size() || !current.ModTime().Equal(stat.ModTime()) {
		os.Remove(tmp.Name())
		report.Errors = append(report.Errors, fmt.Sprintf("trim %s: audit log changed before publication", path))
		return 0, 0
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		report.Errors = append(report.Errors, fmt.Sprintf("rename %s: %v", path, err))
		return 0, 0
	}
	return dropped, original - int64(len(tail))
}

// tailLinesWithinBudget returns the trailing slice of path that fits both
// maxBytes and maxLines, plus the number of leading lines it dropped.
// Lines that individually exceed maxBytes are skipped (treated as garbage).
func tailLinesWithinBudget(path string, maxBytes int64, maxLines int, expected os.FileInfo) ([]byte, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(expected, opened) {
		return nil, 0, errors.Join(err, f.Close(), fmt.Errorf("audit log changed while opening"))
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxPruneAuditLogBytes+1)
	var all [][]byte
	totalLines := 0
	for scanner.Scan() {
		totalLines++
		line := append([]byte{}, scanner.Bytes()...)
		if int64(len(line))+1 > maxBytes {
			continue
		}
		all = append(all, line)
	}
	scanErr := scanner.Err()
	afterFile, statErr := f.Stat()
	afterPath, lstatErr := os.Lstat(path)
	closeErr := f.Close()
	if err := errors.Join(scanErr, statErr, lstatErr, closeErr); err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, err
	}
	if !os.SameFile(opened, afterFile) || !os.SameFile(afterFile, afterPath) ||
		opened.Mode() != afterFile.Mode() || opened.Size() != afterFile.Size() ||
		!opened.ModTime().Equal(afterFile.ModTime()) {
		return nil, 0, fmt.Errorf("audit log changed while reading")
	}
	keep := all
	if maxLines > 0 && len(keep) > maxLines {
		keep = keep[len(keep)-maxLines:]
	}
	for {
		var size int64
		for _, l := range keep {
			size += int64(len(l)) + 1
		}
		if size <= maxBytes || len(keep) == 0 {
			break
		}
		keep = keep[1:]
	}
	out := make([]byte, 0)
	for _, l := range keep {
		out = append(out, l...)
		out = append(out, '\n')
	}
	return out, totalLines - len(keep), nil
}

func countLinesQuick(path string, expected os.FileInfo) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(expected, opened) {
		return 0, errors.Join(err, f.Close(), fmt.Errorf("audit log changed while opening"))
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxPruneAuditLogBytes+1)
	count := 0
	for scanner.Scan() {
		count++
	}
	scanErr := scanner.Err()
	afterFile, statErr := f.Stat()
	afterPath, lstatErr := os.Lstat(path)
	closeErr := f.Close()
	if err := errors.Join(scanErr, statErr, lstatErr, closeErr); err != nil {
		return 0, err
	}
	if !os.SameFile(opened, afterFile) || !os.SameFile(afterFile, afterPath) ||
		opened.Mode() != afterFile.Mode() || opened.Size() != afterFile.Size() ||
		!opened.ModTime().Equal(afterFile.ModTime()) {
		return 0, fmt.Errorf("audit log changed while counting lines")
	}
	return count, nil
}

// PolicyPathFromRepo returns the canonical YAML path inside repoRoot.
func PolicyPathFromRepo(repoRoot string) string {
	harnessRoot := filepath.Join(repoRoot, "tools", "reconc", "harness")
	harnessInfo, inspectErr := os.Lstat(harnessRoot)
	if inspectErr == nil && harnessInfo.Mode()&os.ModeSymlink == 0 && harnessInfo.IsDir() {
		entries, err := readPruneDirectory(harnessRoot, harnessInfo)
		if err != nil {
			return filepath.Join(harnessRoot, "template", "config", "workflow", "prune-policy.yaml")
		}
		var firstPolicy string
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			candidate := filepath.Join(harnessRoot, entry.Name(), "config", "workflow", "prune-policy.yaml")
			if _, err := os.Stat(candidate); err == nil {
				if entry.Name() == "template" {
					return candidate
				}
				if firstPolicy == "" {
					firstPolicy = candidate
				}
			}
		}
		if firstPolicy != "" {
			return firstPolicy
		}
	}
	return filepath.Join(harnessRoot, "template", "config", "workflow", "prune-policy.yaml")
}
