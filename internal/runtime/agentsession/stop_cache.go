package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/runtime"
)

const (
	stopPolicyFingerprintVersion = "stop-policy-report-v3"
	stopPolicyUntrackedModeEnv   = "RECONC_STOP_FINGERPRINT_UNTRACKED"
)

type stopPolicyFingerprintInput struct {
	Version            string          `json:"version"`
	RepoRoot           string          `json:"repo_root"`
	PolicyLockHash     string          `json:"policy_lock_hash"`
	ReportFormat       string          `json:"report_format"`
	SchemaBase         string          `json:"schema_base"`
	ReadPaths          []string        `json:"read_paths"`
	WritePaths         []string        `json:"write_paths"`
	Commands           []string        `json:"commands"`
	Claims             []string        `json:"claims"`
	CommandResults     []CommandResult `json:"command_results"`
	GitHead            string          `json:"git_head"`
	GitStatusMode      string          `json:"git_status_mode"`
	GitStatus          string          `json:"git_status"`
	GitDirtyFiles      []gitDirtyFile  `json:"git_dirty_files"`
	ReconcAuditNoCache string          `json:"reconc_audit_no_cache"`
}

type stopPolicyEvidenceInput struct {
	ReadPaths      []string        `json:"read_paths"`
	WritePaths     []string        `json:"write_paths"`
	Commands       []string        `json:"commands"`
	Claims         []string        `json:"claims"`
	CommandResults []CommandResult `json:"command_results"`
}

type gitDirtyFile struct {
	Path         string `json:"path"`
	WorktreeHash string `json:"worktree_hash"`
	IndexEntry   string `json:"index_entry"`
}

func runStopPolicyCheck(repoRoot string, state SessionState) (*runtime.CheckReport, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	return withStopPolicyReportLock(root, state.SessionID, func() (*runtime.CheckReport, error) {
		return runStopPolicyCheckLocked(root, state)
	})
}

func runStopPolicyCheckLocked(repoRoot string, state SessionState) (*runtime.CheckReport, error) {
	fingerprintInput := stopPolicyFingerprintInputFor(repoRoot, state)
	fingerprint := hashStopPolicyFingerprintInput(fingerprintInput)
	if fingerprint != "" && state.StopPolicyFingerprint == fingerprint && state.StopPolicyReportHash != "" {
		report, reportHash, err := readLatestReport(repoRoot, state.SessionID)
		if err == nil && reportHash == state.StopPolicyReportHash {
			return report, nil
		}
	}

	report, err := runCheckAndSave(repoRoot, state.SessionID, state.ReadPaths,
		state.WritePaths, state.Commands, state.CommandResults, state.Claims)
	if err != nil {
		return nil, err
	}
	reportHash := hashCheckReport(report)
	if fingerprint != "" && reportHash != "" {
		initialEvidenceHash := stopPolicyEvidenceHash(state)
		fingerprintInput.PolicyLockHash = fileContentHash(filepath.Join(repoRoot, ".reconc", "policy.lock.json"))
		reportFingerprint := hashStopPolicyFingerprintInput(fingerprintInput)
		_, _ = MutateSessionState(repoRoot, state.SessionID, func(current SessionState) SessionState {
			if stopPolicyEvidenceHash(current) == initialEvidenceHash {
				current.StopPolicyFingerprint = reportFingerprint
				current.StopPolicyEvidenceHash = initialEvidenceHash
				current.StopPolicyReportHash = reportHash
				current.ReportPath = sessionReportPath(repoRoot, state.SessionID)
			}
			return current
		})
	}
	return report, nil
}

func cachedCleanStopPolicyReportForEvidence(repoRoot string, state SessionState, evidenceHash string) (*runtime.CheckReport, bool) {
	if evidenceHash == "" || state.StopPolicyEvidenceHash != evidenceHash || state.StopPolicyReportHash == "" {
		return nil, false
	}
	report, reportHash, err := readLatestReport(repoRoot, state.SessionID)
	if err != nil || reportHash != state.StopPolicyReportHash {
		return nil, false
	}
	if len(blockingViolations(report)) != 0 {
		return nil, false
	}
	return report, true
}

func withStopPolicyReportLock(repoRoot, sessionID string, fn func() (*runtime.CheckReport, error)) (*runtime.CheckReport, error) {
	lockPath := filepath.Join(projectDir(repoRoot), "locks", sanitiseID(sessionID)+".stop-policy.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("create stop-policy lock dir: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open stop-policy lock: %w", err)
	}
	defer file.Close()
	unlock, err := lockSessionFile(file)
	if err != nil {
		return nil, fmt.Errorf("lock stop-policy report: %w", err)
	}
	defer unlock()
	return fn()
}

func stopPolicyFingerprint(repoRoot string, state SessionState) string {
	return hashStopPolicyFingerprintInput(stopPolicyFingerprintInputFor(repoRoot, state))
}

func stopPolicyFingerprintInputFor(repoRoot string, state SessionState) stopPolicyFingerprintInput {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		root = repoRoot
	}
	gitStatus, gitStatusMode := stopPolicyGitStatus(root)
	return stopPolicyFingerprintInput{
		Version:            stopPolicyFingerprintVersion,
		RepoRoot:           root,
		PolicyLockHash:     fileContentHash(filepath.Join(root, ".reconc", "policy.lock.json")),
		ReportFormat:       runtime.CheckReportFormatVersion,
		SchemaBase:         os.Getenv("RECONC_SCHEMA_BASE_URL"),
		ReadPaths:          sortedUnique(state.ReadPaths),
		WritePaths:         sortedUnique(state.WritePaths),
		Commands:           sortedUnique(state.Commands),
		Claims:             sortedUnique(state.Claims),
		CommandResults:     append([]CommandResult{}, state.CommandResults...),
		GitHead:            gitOutput(root, "rev-parse", "HEAD"),
		GitStatusMode:      gitStatusMode,
		GitStatus:          gitStatus,
		GitDirtyFiles:      gitDirtyFiles(root, gitStatus),
		ReconcAuditNoCache: os.Getenv("RECONC_AUDIT_NO_CACHE"),
	}
}

func hashStopPolicyFingerprintInput(input stopPolicyFingerprintInput) string {
	body, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func stopPolicyGitStatus(repoRoot string) (status string, mode string) {
	mode = strings.ToLower(strings.TrimSpace(os.Getenv(stopPolicyUntrackedModeEnv)))
	switch mode {
	case "all", "no", "normal":
	default:
		mode = "normal"
	}
	if mode == "normal" {
		return stopPolicyGitStatusNormal(repoRoot), mode
	}
	return filteredGitOutput(repoRoot, "status", "--porcelain=v1", "-z", "--untracked-files="+mode), mode
}

func stopPolicyGitStatusNormal(repoRoot string) string {
	type gitStatusPart struct {
		name string
		out  string
	}
	ch := make(chan gitStatusPart, 2)
	go func() {
		ch <- gitStatusPart{
			name: "tracked",
			out:  filteredGitOutput(repoRoot, "status", "--porcelain=v1", "-z", "--untracked-files=no"),
		}
	}()
	go func() {
		ch <- gitStatusPart{
			name: "untracked",
			out:  untrackedDirectoryStatus(repoRoot),
		}
	}()

	var tracked, untracked string
	for i := 0; i < 2; i++ {
		part := <-ch
		switch part.name {
		case "tracked":
			tracked = part.out
		case "untracked":
			untracked = part.out
		}
	}
	return tracked + untracked
}

func untrackedDirectoryStatus(repoRoot string) string {
	raw := filteredGitOutput(repoRoot, "ls-files", "--others", "--exclude-standard", "--directory", "-z")
	if raw == "" {
		return ""
	}
	records := strings.Split(raw, "\x00")
	var b strings.Builder
	for _, record := range records {
		path := strings.TrimSpace(record)
		if path == "" || stopPolicyRuntimeStateRecord(path) {
			continue
		}
		b.WriteString("?? ")
		b.WriteString(filepath.ToSlash(path))
		b.WriteByte(0)
	}
	return b.String()
}

func stopPolicyEvidenceHash(state SessionState) string {
	input := stopPolicyEvidenceInput{
		ReadPaths:      sortedUnique(state.ReadPaths),
		WritePaths:     sortedUnique(state.WritePaths),
		Commands:       sortedUnique(state.Commands),
		Claims:         sortedUnique(state.Claims),
		CommandResults: append([]CommandResult{}, state.CommandResults...),
	}
	body, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	return hashBytes(body)
}

func readLatestReport(repoRoot, sessionID string) (*runtime.CheckReport, string, error) {
	path := sessionReportPath(repoRoot, sessionID)
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var report runtime.CheckReport
	if err := json.Unmarshal(body, &report); err != nil {
		return nil, "", fmt.Errorf("unmarshal report %s: %w", path, err)
	}
	return &report, hashCheckReport(&report), nil
}

func hashCheckReport(report *runtime.CheckReport) string {
	if report == nil {
		return ""
	}
	body, err := json.Marshal(report)
	if err != nil {
		return ""
	}
	return hashBytes(body)
}

func hashBlockingViolations(violations []runtime.Violation) string {
	body, err := json.Marshal(violations)
	if err != nil {
		return ""
	}
	return hashBytes(body)
}

func hashBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func fileContentHash(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return "error:" + err.Error()
	}
	return hashBytes(body)
}

func gitOutput(repoRoot string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "error:" + err.Error() + "\n" + string(out)
	}
	return string(out)
}

func gitDirtyFiles(repoRoot string, status string) []gitDirtyFile {
	paths := dirtyPathsFromStatus(status)
	indexEntries := gitIndexEntries(repoRoot, paths)
	files := make([]gitDirtyFile, 0, len(paths))
	for _, path := range paths {
		files = append(files, gitDirtyFile{
			Path:         path,
			WorktreeHash: worktreePathHash(repoRoot, path),
			IndexEntry:   indexEntries[path],
		})
	}
	return files
}

func dirtyPathsFromStatus(status string) []string {
	records := strings.Split(status, "\x00")
	paths := make([]string, 0, len(records))
	for _, record := range records {
		path := dirtyPathFromStatusRecord(record)
		if path == "" || stopPolicyRuntimeStateRecord(path) {
			continue
		}
		paths = append(paths, path)
	}
	return sortedUnique(paths)
}

func dirtyPathFromStatusRecord(record string) string {
	record = strings.TrimRight(record, "\r\n")
	if record == "" {
		return ""
	}
	if len(record) >= 4 && record[2] == ' ' {
		return filepath.ToSlash(strings.TrimSpace(record[3:]))
	}
	return filepath.ToSlash(record)
}

func gitIndexEntries(repoRoot string, paths []string) map[string]string {
	entries := map[string]string{}
	if len(paths) == 0 {
		return entries
	}
	args := append([]string{"-C", repoRoot, "ls-files", "-s", "-z", "--"}, paths...)
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		for _, path := range paths {
			entries[path] = "error:" + err.Error()
		}
		return entries
	}
	for _, record := range strings.Split(string(out), "\x00") {
		if record == "" {
			continue
		}
		tab := strings.IndexByte(record, '\t')
		if tab < 0 || tab == len(record)-1 {
			continue
		}
		path := filepath.ToSlash(record[tab+1:])
		entries[path] = record[:tab]
	}
	return entries
}

func worktreePathHash(repoRoot string, path string) string {
	fullPath := filepath.Join(repoRoot, filepath.FromSlash(path))
	info, err := os.Lstat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing"
		}
		return "error:" + err.Error()
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(fullPath)
		if err != nil {
			return "symlink-error:" + err.Error()
		}
		return "symlink:" + hashBytes([]byte(target))
	}
	if info.IsDir() {
		return "dir"
	}
	if !info.Mode().IsRegular() {
		return "mode:" + info.Mode().String()
	}
	body, err := os.ReadFile(fullPath)
	if err != nil {
		return "error:" + err.Error()
	}
	return hashBytes(body)
}

func filteredGitOutput(repoRoot string, args ...string) string {
	raw := gitOutput(repoRoot, args...)
	parts := strings.Split(raw, "\x00")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || stopPolicyRuntimeStateRecord(part) {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\x00") + "\x00"
}

func stopPolicyRuntimeStateRecord(record string) bool {
	for _, marker := range []string{
		".reconc/degenmode/",
		".reconc/cache/",
		".reconc/locks/",
		".reconc/reports/",
		".reconc/audit.jsonl",
	} {
		if strings.Contains(record, marker) {
			return true
		}
	}
	return false
}

func recordStopBlockAndRepeated(repoRoot, sessionID string, violations []runtime.Violation) bool {
	violationHash := hashBlockingViolations(violations)
	if violationHash == "" {
		return false
	}
	repeated := false
	_, err := MutateSessionState(repoRoot, sessionID, func(state SessionState) SessionState {
		repeated = state.LastStopBlockViolationHash == violationHash
		state.LastStopBlockViolationHash = violationHash
		return state
	})
	return err == nil && repeated
}

func reportPathForStop(repoRoot, sessionID string) string {
	path := sessionReportPath(repoRoot, sessionID)
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return path
}
