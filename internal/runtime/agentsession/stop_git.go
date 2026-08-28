package agentsession

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

	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/gitexec"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/runtime"
)

func gitCommandOutput(repoRoot string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := gitexec.CommandContext(ctx, repoRoot, nil, args...)
	out, err := boundedexec.CombinedOutput(cmd, maxStopGitOutputBytes)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return string(out), fmt.Errorf("git %s timed out", strings.Join(args, " "))
	}
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
	headBody, err := readBoundedFile(filepath.Join(gitDir, "HEAD"), maxGitControlFileBytes)
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
	// Rooting and escape are decided before cleaning and without asking the
	// running platform: `filepath.IsAbs` calls a POSIX root relative on Windows,
	// which would resolve `/etc/passwd` against the git directory there and
	// refuse it everywhere else.
	if pathidentity.Rooted(ref) || pathidentity.EscapesLexically(ref) {
		return "", fmt.Errorf("unsafe HEAD ref")
	}
	cleanRef := filepath.Clean(filepath.FromSlash(ref))
	if cleanRef == "." || pathidentity.Rooted(cleanRef) || pathidentity.EscapesLexically(cleanRef) {
		return "", fmt.Errorf("unsafe HEAD ref")
	}
	return cleanRef, nil
}

func gitRefObjectID(gitDir, cleanRef, ref string) (string, bool, error) {
	commonDir, err := gitCommonDir(gitDir)
	if err != nil {
		return "", false, err
	}
	roots := sortedUnique([]string{gitDir, commonDir})
	if objectID, found, err := readLooseGitRef(roots, cleanRef); found || err != nil {
		return objectID, found, err
	}
	return readPackedGitRef(roots, ref)
}

func readLooseGitRef(roots []string, cleanRef string) (string, bool, error) {
	for _, root := range roots {
		body, err := readBoundedFile(filepath.Join(root, cleanRef), maxGitControlFileBytes)
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
	refBytes := []byte(ref)
	for _, root := range roots {
		body, err := readBoundedFile(filepath.Join(root, "packed-refs"), maxPackedRefsBytes)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", false, err
		}
		for len(body) > 0 {
			line := body
			if newline := bytes.IndexByte(body, '\n'); newline >= 0 {
				line, body = body[:newline], body[newline+1:]
			} else {
				body = nil
			}
			objectID, candidate, ok := packedRefFields(line)
			if ok && bytes.Equal(candidate, refBytes) {
				return string(objectID), true, nil
			}
		}
	}
	return "", false, nil
}

func packedRefFields(line []byte) (objectID, ref []byte, ok bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] == '#' || line[0] == '^' {
		return nil, nil, false
	}
	separator := bytes.IndexAny(line, " \t")
	if separator <= 0 {
		return nil, nil, false
	}
	objectID = line[:separator]
	ref = bytes.TrimSpace(line[separator:])
	if len(ref) == 0 || bytes.ContainsAny(ref, " \t") {
		return nil, nil, false
	}
	return objectID, ref, true
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
	body, err := readBoundedFile(dotGit, maxGitControlFileBytes)
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

func gitCommonDir(gitDir string) (string, error) {
	body, err := readBoundedFile(filepath.Join(gitDir, "commondir"), maxGitControlFileBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return gitDir, nil
		}
		return "", fmt.Errorf("read git commondir: %w", err)
	}
	commonDir := strings.TrimSpace(string(body))
	if commonDir == "" {
		return "", fmt.Errorf("git commondir is empty")
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	return filepath.Clean(commonDir), nil
}

func gitDirtyFiles(repoRoot string, status string) []gitDirtyFile {
	paths := dirtyPathsFromStatus(status)
	indexEntries := gitIndexEntries(repoRoot, paths)
	files := make([]gitDirtyFile, 0, len(paths))
	for _, path := range paths {
		indexEntry := indexEntries[path]
		files = append(files, gitDirtyFile{
			Path:         path,
			WorktreeHash: worktreePathHash(repoRoot, path, indexEntry),
			IndexEntry:   indexEntry,
		})
	}
	return files
}

func completionDirtyFilesTrusted(files []gitDirtyFile) bool {
	for _, file := range files {
		if strings.HasPrefix(file.IndexEntry, "error:") {
			return false
		}
		hash := file.WorktreeHash
		switch {
		case hash == "missing":
			continue
		case strings.HasPrefix(hash, "dir:"):
			hash = strings.TrimPrefix(hash, "dir:")
		case strings.HasPrefix(hash, "symlink:"):
			hash = strings.TrimPrefix(hash, "symlink:")
		case strings.HasPrefix(hash, "submodule:"):
			hash = strings.TrimPrefix(hash, "submodule:")
		}
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != sha256.Size {
			return false
		}
	}
	return true
}

// dirtyPathsFromStatus parses `git status --porcelain=v1 -z` records.
// Each record is "XY <path>"; rename/copy records are followed by the
// origin path as a separate NUL field WITHOUT an XY prefix, and that
// origin is dirty too. Path bytes are verbatim (-z never quotes), so
// nothing is trimmed: leading/trailing spaces are part of the name.
func dirtyPathsFromStatus(status string) []string {
	records := strings.Split(status, "\x00")
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(records))
	add := func(path string) {
		path = filepath.ToSlash(path)
		if path == "" || stopPolicyRuntimeStateRecord(path) {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for i := 0; i < len(records); i++ {
		record := records[i]
		if record == "" {
			continue
		}
		if len(record) >= 4 && record[2] == ' ' {
			add(record[3:])
			if isRenameOrCopyStatus(record[0], record[1]) && i+1 < len(records) {
				i++
				add(records[i])
			}
			continue
		}
		// Defensive fallback for a record that does not match the
		// XY-prefix shape; keep its bytes verbatim.
		add(record)
	}
	sort.Strings(paths)
	return paths
}

func isRenameOrCopyStatus(x, y byte) bool {
	return x == 'R' || x == 'C' || y == 'R' || y == 'C'
}

// gitIndexBatchBytes bounds the path arguments passed to a single
// `git ls-files` invocation. Path arguments are appended to argv, and a
// large session can accumulate thousands of multi-kilobyte paths whose
// combined size exceeds the platform ARG_MAX (about 2 MiB on Linux), which
// would fail the spawn with E2BIG. Batching by total argument bytes keeps
// every invocation well under that limit while merging all index entries.
const gitIndexBatchBytes = 128 * 1024

func gitIndexEntries(repoRoot string, paths []string) map[string]string {
	entries := map[string]string{}
	if len(paths) == 0 {
		return entries
	}
	batch := []string{}
	batchBytes := 0
	flush := func() {
		if len(batch) == 0 {
			return
		}
		mergeGitIndexEntries(entries, repoRoot, batch)
		batch = nil
		batchBytes = 0
	}
	for _, path := range paths {
		if len(batch) > 0 && batchBytes+len(path) > gitIndexBatchBytes {
			flush()
		}
		batch = append(batch, path)
		batchBytes += len(path)
	}
	flush()
	return entries
}

func mergeGitIndexEntries(entries map[string]string, repoRoot string, paths []string) {
	args := append([]string{"ls-files", "-s", "-z", "--"}, paths...)
	out, err := gitCommandOutput(repoRoot, args...)
	if err != nil {
		for _, path := range paths {
			if _, seen := entries[path]; !seen {
				entries[path] = "error:" + err.Error()
			}
		}
		return
	}
	for _, record := range strings.Split(out, "\x00") {
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
}

func worktreePathHash(repoRoot string, path, indexEntry string) string {
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
		if strings.HasPrefix(indexEntry, "160000 ") {
			return submoduleWorktreeHash(fullPath)
		}
		return stopDirectoryContentHash(fullPath)
	}
	if !info.Mode().IsRegular() {
		return "mode:" + info.Mode().String()
	}
	hash, err := hashFileContent(fullPath)
	if err != nil {
		return "error:" + err.Error()
	}
	return hash
}

func submoduleWorktreeHash(root string) string {
	head, err := gitCommandOutput(root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "submodule-error:" + err.Error()
	}
	indexHash, err := gitIndexFingerprint(root)
	if err != nil {
		return "submodule-error:" + err.Error()
	}
	rawStatus, err := gitCommandOutput(root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return "submodule-error:" + err.Error()
	}
	status := filterStopPolicyGitStatus(rawStatus)
	dirtyFiles := gitDirtyFiles(root, status)
	if !completionDirtyFilesTrusted(dirtyFiles) {
		return "submodule-error:dirty content could not be bound safely"
	}
	body, err := json.Marshal(struct {
		Head       string         `json:"head"`
		IndexHash  string         `json:"index_hash"`
		Status     string         `json:"status"`
		DirtyFiles []gitDirtyFile `json:"dirty_files"`
	}{
		Head: strings.TrimSpace(head), IndexHash: indexHash, Status: status,
		DirtyFiles: dirtyFiles,
	})
	if err != nil {
		return "submodule-error:" + err.Error()
	}
	return "submodule:" + hashBytes(body)
}

func readBoundedFile(path string, maxBytes int64) ([]byte, error) {
	return boundedio.ReadFile(path, maxBytes)
}

// stopPolicyContentHashBound caps the bytes a single file contributes to the
// stop-policy fingerprint. Hashing multi-gigabyte dirty files fully would
// stall the stop hook until the platform timeout, degrading toward fail-open
// wherever failure-allow applies. Files above the bound are bound by size
// and modification time instead. That metadata identifies the input for
// diagnostics, but any oversized dirty file makes the stop-policy report
// cache ineligible and the completion candidate untrusted. This preserves
// bounded hook latency without ever reusing a report for content that was not
// hashed exactly. The policy lockfile stays far below the bound, so its hash
// is always content-exact.
const stopPolicyContentHashBound = 64 * 1024 * 1024

// stopPolicyLockfileScanBound matches the runtime's own lockfile read bound so
// a policy the evaluator would still load can never be silently treated as
// unreadable by the cache-eligibility scan.
const stopPolicyLockfileScanBound int64 = 16 << 20

func hashFileContent(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	return hashFileContentExpected(path, info)
}

func hashFileContentExpected(path string, expected os.FileInfo) (string, error) {
	if !expected.Mode().IsRegular() {
		return "", fmt.Errorf("hash content: %s is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	info, statErr := file.Stat()
	if statErr != nil {
		closeErr := file.Close()
		return "", errors.Join(statErr, closeErr)
	}
	if !info.Mode().IsRegular() || !os.SameFile(expected, info) {
		return "", errors.Join(fmt.Errorf("hash content: %s changed before open", path), file.Close())
	}
	if info.Size() > stopPolicyContentHashBound {
		closeErr := file.Close()
		if closeErr != nil {
			return "", closeErr
		}
		return oversizedFileFingerprint(info), nil
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, io.LimitReader(file, stopPolicyContentHashBound))
	after, statErr := file.Stat()
	closeErr := file.Close()
	if err := errors.Join(readErr, statErr, closeErr); err != nil {
		return "", err
	}
	if !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return "", fmt.Errorf("hash content: %s changed while reading", path)
	}
	beforeGeneration, beforeOK := stopPathMetadataGeneration(path, info)
	afterGeneration, afterOK := stopPathMetadataGeneration(path, after)
	if beforeOK != afterOK || (beforeOK && beforeGeneration != afterGeneration) {
		return "", fmt.Errorf("hash content: %s changed while reading", path)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func oversizedFileFingerprint(info os.FileInfo) string {
	return fmt.Sprintf("oversized:%d:%s", info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano))
}

func filterStopPolicyGitStatus(raw string) string {
	parts := strings.Split(raw, "\x00")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || stopPolicyRuntimeStateRecord(statusRecordPath(part)) {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\x00") + "\x00"
}

// statusRecordPath strips the two-character "XY " status prefix from a
// porcelain -z record so the path can be matched by prefix. Records without
// the prefix (rename origins) are returned verbatim.
func statusRecordPath(record string) string {
	if len(record) >= 3 && record[2] == ' ' {
		return record[3:]
	}
	return record
}

// stopPolicyRuntimeStateRecord reports whether a repo-relative path is
// Reconc-owned runtime state that must not influence the stop-policy
// fingerprint. Matching is prefix-based on the path: a substring match would
// wrongly drop user files such as "src/x.reconc/run/data.txt" whose name
// merely contains a runtime marker, leaving the stop cache stale when they
// change.
func stopPolicyRuntimeStateRecord(path string) bool {
	return strings.HasPrefix(filepath.ToSlash(path), ".reconc/")
}

func recordStopBlockAndRepeated(repoRoot, sessionID string, violations []runtime.Violation) (bool, string) {
	violationHash := hashBlockingViolations(violations)
	if violationHash == "" {
		return false, ""
	}
	repeated := false
	_, err := mutateSessionStateResolved(repoRoot, sessionID, func(state SessionState) SessionState {
		repeated = state.LastStopBlockViolationHash == violationHash
		state.LastStopBlockViolationHash = violationHash
		return state
	})
	return err == nil && repeated, "RB-" + violationHash[:12]
}

func reportPathForStop(repoRoot, sessionID string) string {
	path := sessionReportPath(repoRoot, sessionID)
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return path
}
