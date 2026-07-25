package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"reconc.dev/reconc/internal/pathidentity"
)

const (
	defaultHistoryBoundary = "520dd9348c1d35acb581768c8979c29fbc025c2a"
	defaultMaxFileBytes    = 32 << 20
)

type auditOptions struct {
	Root            string
	HistoryBoundary string
	MaxFileBytes    int64
}

type auditReport struct {
	TrackedFiles           int
	AuditedCommits         int
	AuditedHistoricalBlobs int
	Findings               []auditFinding
}

type auditFinding struct {
	Rule   string
	Path   string
	Line   int
	Detail string
}

type historyException struct {
	ThroughCommit string
	Owner         string
	Rationale     string
}

var legacyHistoryException = historyException{
	ThroughCommit: defaultHistoryBoundary,
	Owner:         "repository maintainer",
	Rationale:     "legacy public session trailers and pre-sanitization vocabulary predate the publication boundary; history and protected tags remain immutable",
}

var forbiddenWordDigests = map[string]string{
	"4c5cddb7859b93eebf26c551518c021a31fa0013b2c03afa5b541cbc8bd079a6": "private-project-name-1",
	"6751a012016130351049af79e8c1893ce9690357b9a884ed7ec3eccf936bd9a2": "private-project-name-2",
	"0de91c90064b72f29cca4fc772ed1d29adf00a8ade0c2edb04c59a5d722a16ff": "private-project-name-3",
	"8c704e3a6730d5654e02588991b8d4e98e7281ae405615df9f99398019d3d7b9": "private-project-name-4",
}

var (
	wordPattern        = regexp.MustCompile(`[A-Za-z0-9]+`)
	privatePathPattern = regexp.MustCompile(
		`(?i)(?:/(?:User` + `s|home)/[A-Za-z0-9._-]+(?:/[^\s"']*)?|/` +
			`Volumes/[A-Za-z0-9._-]+(?:/[^\s"']*)?|[A-Z]:\\User` + `s\\[^\s"']+)`,
	)
	sessionURLPattern = regexp.MustCompile(
		`(?i)https?://(?:claude\.ai/code/session_[A-Za-z0-9_-]+|chatgpt\.com/(?:c|codex/tasks)/[A-Za-z0-9_-]+)`,
	)
	credentialURLPattern = regexp.MustCompile(`(?i)https?://[^/@\s:]+:[^/@\s]+@[^\s]+`)
	accessTokenPattern   = regexp.MustCompile(
		`(?:AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-(?:proj-)?[A-Za-z0-9_-]{20,}|xai-[A-Za-z0-9_-]{20,}|AIza[0-9A-Za-z_-]{30,})`,
	)
	keyAssignmentPattern = regexp.MustCompile(
		`(?i)(?:OPENAI_API_KEY|ANTHROPIC_API_KEY|XAI_API_KEY|AWS_SECRET_ACCESS_KEY|GITHUB_TOKEN)\s*[:=]\s*["']?[A-Za-z0-9_./+-]{12,}`,
	)
	suspiciousTrackedPathPattern = regexp.MustCompile(
		`(?i)(?:^|/)(?:\.gitkeep|\.env|id_rsa|id_ed25519|credentials(?:\.json)?|secrets?\.(?:json|ya?ml)|[^/]+\.(?:pem|p12|pfx))$`,
	)
	transcriptPathPattern = regexp.MustCompile(`(?i)(?:^|/)(?:transcripts?|session-export)(?:/|\.|$)`)
)

func defaultAuditOptions() auditOptions {
	return auditOptions{Root: ".", HistoryBoundary: legacyHistoryException.ThroughCommit, MaxFileBytes: defaultMaxFileBytes}
}

func auditRepository(ctx context.Context, options auditOptions) (auditReport, error) {
	root, err := canonicalAuditRoot(ctx, options.Root)
	if err != nil {
		return auditReport{}, err
	}
	if options.HistoryBoundary == "" {
		return auditReport{}, errors.New("history boundary is empty")
	}
	if options.MaxFileBytes <= 0 {
		return auditReport{}, errors.New("maximum tracked-file size must be positive")
	}
	if options.HistoryBoundary == legacyHistoryException.ThroughCommit {
		if strings.TrimSpace(legacyHistoryException.Owner) == "" || strings.TrimSpace(legacyHistoryException.Rationale) == "" {
			return auditReport{}, errors.New("legacy history exception requires an owner and rationale")
		}
	}
	paths, err := trackedPaths(ctx, root)
	if err != nil {
		return auditReport{}, err
	}
	report := auditReport{TrackedFiles: len(paths), Findings: []auditFinding{}}
	for _, path := range paths {
		report.Findings = append(report.Findings, auditTrackedPath(root, path, options.MaxFileBytes)...)
	}
	historyFindings, commitCount, blobCount, err := auditPostBoundaryHistory(ctx, root, options.HistoryBoundary, options.MaxFileBytes)
	if err != nil {
		return auditReport{}, err
	}
	report.AuditedCommits = commitCount
	report.AuditedHistoricalBlobs = blobCount
	report.Findings = append(report.Findings, historyFindings...)
	report.Findings = uniqueSortedFindings(report.Findings)
	return report, nil
}

// canonicalAuditRoot resolves the audit root to its filesystem identity and
// proves it is the Git worktree root itself, not a subdirectory or a different
// checkout.
//
// Both sides go through pathidentity, the same resolver the enforcement layers
// use, because string comparison decides spelling rather than identity. On a
// case-insensitive filesystem filepath.EvalSymlinks leaves the caller's casing
// intact, so a working directory whose parent segments differ from the checkout
// only in letter case compared unequal to the Git-reported root and failed this
// audit on a healthy repository.
func canonicalAuditRoot(ctx context.Context, root string) (string, error) {
	abs, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return "", fmt.Errorf("resolve audit root identity: %w", err)
	}
	gitRoot, err := gitOutput(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	resolvedGitRoot, err := pathidentity.ResolveExisting(strings.TrimSpace(string(gitRoot)))
	if err != nil {
		return "", fmt.Errorf("resolve Git root identity: %w", err)
	}
	if abs != resolvedGitRoot {
		return "", fmt.Errorf("audit root %q is not the Git worktree root %q", abs, resolvedGitRoot)
	}
	return abs, nil
}

func trackedPaths(ctx context.Context, root string) ([]string, error) {
	unmerged, err := gitOutput(ctx, root, "ls-files", "-u", "-z")
	if err != nil {
		return nil, err
	}
	if len(unmerged) != 0 {
		return nil, errors.New("publication audit refuses an unmerged Git index")
	}
	body, err := gitOutput(ctx, root, "ls-files", "-z", "--cached")
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	for _, raw := range bytes.Split(body, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		if !utf8.Valid(raw) {
			return nil, errors.New("tracked path is not valid UTF-8")
		}
		paths = append(paths, string(raw))
	}
	sort.Strings(paths)
	return paths, nil
}

func auditTrackedPath(root, relative string, maxBytes int64) []auditFinding {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
	if relative == "" || filepath.IsAbs(filepath.FromSlash(relative)) || clean != relative || clean == ".." || strings.HasPrefix(clean, "../") {
		return []auditFinding{{Rule: "path/unsafe", Path: relative, Detail: "tracked path is not a canonical repository-relative path"}}
	}
	findings := auditPathName(relative)
	fullPath := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Lstat(fullPath)
	if err != nil {
		return append(findings, auditFinding{Rule: "path/unreadable", Path: relative, Detail: err.Error()})
	}
	var body []byte
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, readErr := os.Readlink(fullPath)
		if readErr != nil {
			return append(findings, auditFinding{Rule: "path/unreadable", Path: relative, Detail: readErr.Error()})
		}
		body = []byte(target)
	case info.Mode().IsRegular():
		body, err = readBoundedAuditFile(fullPath, maxBytes)
		if err != nil {
			return append(findings, auditFinding{Rule: "content/unreadable", Path: relative, Detail: err.Error()})
		}
	default:
		return append(findings, auditFinding{Rule: "path/irregular", Path: relative, Detail: "tracked path is neither a regular file nor a symbolic link"})
	}
	return append(findings, auditText(relative, string(body))...)
}

func auditPathName(path string) []auditFinding {
	findings := []auditFinding{}
	if suspiciousTrackedPathPattern.MatchString(path) {
		findings = append(findings, auditFinding{Rule: "path/sensitive-artifact", Path: path, Detail: "tracked filename is reserved for secrets or placeholder residue"})
	}
	if transcriptPathPattern.MatchString(path) {
		findings = append(findings, auditFinding{Rule: "path/session-material", Path: path, Detail: "tracked transcript or session-export path is forbidden"})
	}
	return findings
}

func auditText(path, body string) []auditFinding {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	findings := []auditFinding{}
	privateKeyPattern := regexp.MustCompile("-----BEGIN (?:RSA |OPENSSH |EC )?" + "PRIVATE KEY-----")
	legacyTrailerPattern := regexp.MustCompile(regexp.QuoteMeta("Claude" + "-Session:"))
	for index, line := range lines {
		lineNumber := index + 1
		for _, word := range wordPattern.FindAllString(line, -1) {
			digest := sha256.Sum256([]byte(strings.ToLower(word)))
			if label, found := forbiddenWordDigests[hex.EncodeToString(digest[:])]; found {
				findings = append(findings, auditFinding{Rule: "content/private-name", Path: path, Line: lineNumber, Detail: "contains " + label})
			}
		}
		for _, rule := range []struct {
			id      string
			pattern *regexp.Regexp
			detail  string
		}{
			{id: "content/private-path", pattern: privatePathPattern, detail: "contains an absolute user or mounted-volume path"},
			{id: "content/session-url", pattern: sessionURLPattern, detail: "contains an agent session or share URL"},
			{id: "content/session-trailer", pattern: legacyTrailerPattern, detail: "contains a legacy agent-session commit trailer"},
			{id: "content/credential-url", pattern: credentialURLPattern, detail: "contains credentials embedded in a URL"},
			{id: "content/access-token", pattern: accessTokenPattern, detail: "contains a token-shaped credential"},
			{id: "content/key-assignment", pattern: keyAssignmentPattern, detail: "contains a literal secret assignment"},
			{id: "content/private-key", pattern: privateKeyPattern, detail: "contains a private-key header"},
		} {
			if rule.pattern.MatchString(line) {
				findings = append(findings, auditFinding{Rule: rule.id, Path: path, Line: lineNumber, Detail: rule.detail})
			}
		}
	}
	return findings
}

func auditPostBoundaryHistory(ctx context.Context, root, boundary string, maxBytes int64) ([]auditFinding, int, int, error) {
	if _, err := gitOutput(ctx, root, "cat-file", "-e", boundary+"^{commit}"); err != nil {
		return nil, 0, 0, fmt.Errorf("legacy history boundary is unavailable; fetch full history: %w", err)
	}
	if _, err := gitOutput(ctx, root, "merge-base", "--is-ancestor", boundary, "HEAD"); err != nil {
		return nil, 0, 0, fmt.Errorf("legacy history boundary is not an ancestor of HEAD: %w", err)
	}
	body, err := gitOutput(ctx, root, "log", "-z", "--format=%H%x00%B", boundary+"..HEAD")
	if err != nil {
		return nil, 0, 0, err
	}
	parts := bytes.Split(body, []byte{0})
	findings := []auditFinding{}
	commits := make([]string, 0, len(parts)/2)
	count := 0
	for index := 0; index+1 < len(parts); index += 2 {
		if len(parts[index]) == 0 {
			continue
		}
		commit := string(parts[index])
		commits = append(commits, commit)
		count++
		findings = append(findings, auditText("commit/"+commit, string(parts[index+1]))...)
	}
	for _, commit := range commits {
		changedPaths, pathErr := gitOutput(
			ctx,
			root,
			"diff-tree",
			"--root",
			"-m",
			"--no-commit-id",
			"--name-only",
			"--diff-filter=ACMRTUXB",
			"-r",
			"-z",
			commit,
		)
		if pathErr != nil {
			return nil, 0, 0, pathErr
		}
		for _, rawPath := range bytes.Split(changedPaths, []byte{0}) {
			if len(rawPath) == 0 {
				continue
			}
			if !utf8.Valid(rawPath) {
				findings = append(findings, auditFinding{Rule: "history/path-encoding", Path: "commit/" + commit, Detail: "changed path is not valid UTF-8"})
				continue
			}
			path := string(rawPath)
			for _, finding := range auditPathName(path) {
				finding.Path = "commit/" + commit + "/" + path
				findings = append(findings, finding)
			}
		}
	}
	blobFindings, blobCount, err := auditPostBoundaryBlobs(ctx, root, boundary, maxBytes)
	if err != nil {
		return nil, 0, 0, err
	}
	findings = append(findings, blobFindings...)
	return findings, count, blobCount, nil
}

type gitObjectMetadata struct {
	ID   string
	Type string
	Size int64
}

func auditPostBoundaryBlobs(ctx context.Context, root, boundary string, maxBytes int64) ([]auditFinding, int, error) {
	body, err := gitOutput(ctx, root, "rev-list", "--objects", "--no-object-names", boundary+"..HEAD")
	if err != nil {
		return nil, 0, err
	}
	objectIDs := strings.Fields(string(body))
	metadata, err := gitObjectMetadataBatch(ctx, root, objectIDs)
	if err != nil {
		return nil, 0, err
	}
	findings := []auditFinding{}
	blobCount := 0
	for _, object := range metadata {
		if object.Type != "blob" {
			continue
		}
		blobCount++
		path := "history/blob/" + object.ID
		if object.Size > maxBytes {
			findings = append(findings, auditFinding{Rule: "history/oversized-blob", Path: path, Detail: fmt.Sprintf("blob exceeds %d-byte publication-audit bound", maxBytes)})
			continue
		}
		blob, readErr := gitOutput(ctx, root, "cat-file", "blob", object.ID)
		if readErr != nil {
			return nil, 0, readErr
		}
		if int64(len(blob)) != object.Size {
			return nil, 0, fmt.Errorf("git blob %s size changed during publication audit", object.ID)
		}
		findings = append(findings, auditText(path, string(blob))...)
	}
	return findings, blobCount, nil
}

func gitObjectMetadataBatch(ctx context.Context, root string, objectIDs []string) ([]gitObjectMetadata, error) {
	if len(objectIDs) == 0 {
		return nil, nil
	}
	command := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)")
	command.Stdin = strings.NewReader(strings.Join(objectIDs, "\n") + "\n")
	body, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git cat-file --batch-check: %w: %s", err, strings.TrimSpace(string(body)))
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != len(objectIDs) {
		return nil, fmt.Errorf("git cat-file --batch-check returned %d rows for %d objects", len(lines), len(objectIDs))
	}
	metadata := make([]gitObjectMetadata, 0, len(lines))
	for index, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != objectIDs[index] {
			return nil, fmt.Errorf("unexpected git cat-file metadata row %q", line)
		}
		size, parseErr := strconv.ParseInt(fields[2], 10, 64)
		if parseErr != nil || size < 0 {
			return nil, fmt.Errorf("invalid Git object size in metadata row %q", line)
		}
		metadata = append(metadata, gitObjectMetadata{ID: fields[0], Type: fields[1], Size: size})
	}
	return metadata, nil
}

func readBoundedAuditFile(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("file exceeds %d-byte publication-audit bound", maxBytes)
	}
	return body, nil
}

func uniqueSortedFindings(findings []auditFinding) []auditFinding {
	sort.Slice(findings, func(left, right int) bool {
		leftKey := fmt.Sprintf("%s\x00%09d\x00%s\x00%s", findings[left].Path, findings[left].Line, findings[left].Rule, findings[left].Detail)
		rightKey := fmt.Sprintf("%s\x00%09d\x00%s\x00%s", findings[right].Path, findings[right].Line, findings[right].Rule, findings[right].Detail)
		return leftKey < rightKey
	})
	if len(findings) < 2 {
		return findings
	}
	out := findings[:1]
	for _, finding := range findings[1:] {
		if finding != out[len(out)-1] {
			out = append(out, finding)
		}
	}
	return out
}

func gitOutput(ctx context.Context, root string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	body, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(body)))
	}
	return body, nil
}
