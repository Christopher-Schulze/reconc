package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/runtime"
)

const (
	stopPolicyFingerprintVersion = "stop-policy-report-v4"
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
	GitStatusOK        bool            `json:"git_status_ok"`
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

type stopPolicyGitSnapshot struct {
	Head       string
	Status     string
	StatusMode string
	StatusOK   bool
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
		stopScopeWritePathsToUncommitted(repoRoot, state.WritePaths, stopPolicyGitSnapshot{
			Head:       fingerprintInput.GitHead,
			Status:     fingerprintInput.GitStatus,
			StatusMode: fingerprintInput.GitStatusMode,
			StatusOK:   fingerprintInput.GitStatusOK,
		}), state.Commands, state.CommandResults, state.Claims)
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

// stopScopeWritePathsToUncommitted intersects this session's recorded writes
// with the exact Git status snapshot already used by the Stop fingerprint.
// A clean committed path was gated at commit time and no longer needs to
// trigger stop-time batch rules. If Git status or path normalization cannot be
// trusted, the original path is retained so scoping always fails closed.
func stopScopeWritePathsToUncommitted(repoRoot string, writePaths []string, snapshot stopPolicyGitSnapshot) []string {
	if len(writePaths) == 0 {
		return writePaths
	}
	if !snapshot.StatusOK {
		return writePaths
	}
	dirty := map[string]struct{}{}
	for _, path := range dirtyPathsFromStatus(snapshot.Status) {
		dirty[path] = struct{}{}
	}
	if len(dirty) == 0 {
		return nil
	}
	out := make([]string, 0, len(writePaths))
	for _, writePath := range writePaths {
		rel := stopRepoRelPosix(repoRoot, writePath)
		if rel == "" || stopPathDirtyOrUnderDirtyDir(rel, dirty) {
			out = append(out, writePath)
		}
	}
	return out
}

func stopPathDirtyOrUnderDirtyDir(rel string, dirty map[string]struct{}) bool {
	if _, ok := dirty[rel]; ok {
		return true
	}
	segments := strings.Split(rel, "/")
	for i := 1; i < len(segments); i++ {
		if _, ok := dirty[strings.Join(segments[:i], "/")+"/"]; ok {
			return true
		}
	}
	return false
}

func stopRepoRelPosix(repoRoot, raw string) string {
	path := strings.TrimSpace(raw)
	if path == "" {
		return ""
	}
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) {
		cleaned := filepath.Clean(path)
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return ""
		}
		return filepath.ToSlash(cleaned)
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	cleaned := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = resolved
	}
	rel, err := filepath.Rel(root, cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

func cachedCleanStopPolicyReportForEvidence(repoRoot string, state SessionState, evidenceHash string) (*runtime.CheckReport, bool) {
	if evidenceHash == "" || state.StopPolicyEvidenceHash != evidenceHash || state.StopPolicyReportHash == "" {
		return nil, false
	}
	currentFingerprint := stopPolicyFingerprint(repoRoot, state)
	if currentFingerprint == "" || currentFingerprint != state.StopPolicyFingerprint {
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
	unlock, err := filelock.Lock(file)
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
	gitSnapshot := stopPolicyGitSnapshotFor(root)
	dirtyFiles := []gitDirtyFile{}
	if gitSnapshot.StatusOK {
		dirtyFiles = gitDirtyFiles(root, gitSnapshot.Status)
	}
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
		GitHead:            gitSnapshot.Head,
		GitStatusMode:      gitSnapshot.StatusMode,
		GitStatusOK:        gitSnapshot.StatusOK,
		GitStatus:          gitSnapshot.Status,
		GitDirtyFiles:      dirtyFiles,
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
	snapshot := stopPolicyGitSnapshotFor(repoRoot)
	return snapshot.Status, snapshot.StatusMode
}

func stopPolicyGitSnapshotFor(repoRoot string) stopPolicyGitSnapshot {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(stopPolicyUntrackedModeEnv)))
	switch mode {
	case "all", "no", "normal":
	default:
		mode = "normal"
	}
	raw, err := gitCommandOutput(repoRoot, "status", "--porcelain=v1", "-z", "--untracked-files="+mode)
	status := filterStopPolicyGitStatus(raw)
	if err != nil {
		status = "error:" + err.Error() + "\n" + status
	}
	return stopPolicyGitSnapshot{
		Head:       gitHeadFingerprint(repoRoot),
		Status:     status,
		StatusMode: mode,
		StatusOK:   err == nil,
	}
}

func stopPolicyGitStatusNormal(repoRoot string) string {
	snapshot := stopPolicyGitSnapshotFor(repoRoot)
	return snapshot.Status
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

func gitCommandOutput(repoRoot string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// gitHeadFingerprint reads Git's HEAD and referenced object directly. Stop
// already pays for one bounded status process; spawning a second Git process
// just to resolve HEAD added measurable hook latency. Worktree gitdirs and
// packed refs are supported, and any unreadable state is encoded fail-closed
// into the fingerprint instead of being ignored.
func gitHeadFingerprint(repoRoot string) string {
	gitDir, err := resolveGitDir(repoRoot)
	if err != nil {
		return "error:" + err.Error()
	}
	headBody, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "error:" + err.Error()
	}
	head := strings.TrimSpace(string(headBody))
	if !strings.HasPrefix(head, "ref: ") {
		return "detached:" + head
	}
	ref := strings.TrimSpace(strings.TrimPrefix(head, "ref: "))
	cleanRef, err := cleanGitRefPath(ref)
	if err != nil {
		return "error:" + err.Error()
	}
	objectID, found, err := gitRefObjectID(gitDir, cleanRef, ref)
	if err != nil {
		return "error:" + err.Error()
	}
	if found {
		return ref + "\n" + objectID
	}
	// Alternate ref backends such as reftable do not expose loose or packed
	// refs. Pay for rev-parse only on that exceptional path.
	if head, commandErr := gitCommandOutput(repoRoot, "rev-parse", "HEAD"); commandErr == nil {
		return ref + "\n" + strings.TrimSpace(head)
	}
	return ref + "\nmissing"
}

func cleanGitRefPath(ref string) (string, error) {
	cleanRef := filepath.Clean(filepath.FromSlash(ref))
	if filepath.IsAbs(cleanRef) || cleanRef == ".." || strings.HasPrefix(cleanRef, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe HEAD ref")
	}
	return cleanRef, nil
}

func gitRefObjectID(gitDir, cleanRef, ref string) (string, bool, error) {
	roots := sortedUnique([]string{gitDir, gitCommonDir(gitDir)})
	if objectID, found, err := readLooseGitRef(roots, cleanRef); found || err != nil {
		return objectID, found, err
	}
	return readPackedGitRef(roots, ref)
}

func readLooseGitRef(roots []string, cleanRef string) (string, bool, error) {
	for _, root := range roots {
		body, err := os.ReadFile(filepath.Join(root, cleanRef))
		if err == nil {
			return strings.TrimSpace(string(body)), true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
	}
	return "", false, nil
}

func readPackedGitRef(roots []string, ref string) (string, bool, error) {
	for _, root := range roots {
		body, err := os.ReadFile(filepath.Join(root, "packed-refs"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", false, err
		}
		for _, line := range strings.Split(string(body), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == ref {
				return fields[0], true, nil
			}
		}
	}
	return "", false, nil
}

func resolveGitDir(repoRoot string) (string, error) {
	dotGit := filepath.Join(repoRoot, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return "", fmt.Errorf("stat .git: %w", err)
	}
	if info.IsDir() {
		return dotGit, nil
	}
	body, err := os.ReadFile(dotGit)
	if err != nil {
		return "", fmt.Errorf("read .git: %w", err)
	}
	value := strings.TrimSpace(string(body))
	if !strings.HasPrefix(value, "gitdir:") {
		return "", fmt.Errorf("invalid .git file")
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(value, "gitdir:"))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoRoot, gitDir)
	}
	return filepath.Clean(gitDir), nil
}

func gitCommonDir(gitDir string) string {
	body, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	commonDir := strings.TrimSpace(string(body))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	return filepath.Clean(commonDir)
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

func filterStopPolicyGitStatus(raw string) string {
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
		".reconc/runloop/",
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
