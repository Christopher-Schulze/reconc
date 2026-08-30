package main

import (
	"bufio"
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

	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/gitexec"
	"reconc.dev/reconc/internal/pathidentity"
)

const (
	defaultHistoryBoundary = "520dd9348c1d35acb581768c8979c29fbc025c2a"
	defaultMaxFileBytes    = 32 << 20
	maxPublicationGitBytes = 64 << 20
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

type historicalFindingException struct {
	BlobID    string
	Rule      string
	Line      int
	Owner     string
	Rationale string
}

var legacyHistoryException = historyException{
	ThroughCommit: defaultHistoryBoundary,
	Owner:         "repository maintainer",
	Rationale:     "legacy public session trailers and pre-sanitization vocabulary predate the publication boundary; history and protected tags remain immutable",
}

var historicalFindingExceptions = []historicalFindingException{
	{
		BlobID: "61ce12d07ecbf45d856d181a767539de232c5c72", Rule: "content/key-assignment", Line: 54,
		Owner: "repository maintainer", Rationale: "synthetic impact-corpus redaction fixture; current source constructs the marker without publishing a literal assignment",
	},
	{
		BlobID: "6ce72489cf01a97df409a5e822806aa557bdc9ee", Rule: "content/private-path", Line: 115,
		Owner: "repository maintainer", Rationale: "synthetic action-privacy fuzz fixture committed in TASK 156; current source constructs the marker without publishing a literal path",
	},
	{
		BlobID: "7c3573310d2133cf05c5334a817e146db1d1d1c8", Rule: "content/private-path", Line: 553,
		Owner: "repository maintainer", Rationale: "synthetic action-privacy regression fixture committed in TASK 156; current source constructs the marker without publishing a literal path",
	},
	{
		BlobID: "7c3573310d2133cf05c5334a817e146db1d1d1c8", Rule: "content/private-path", Line: 558,
		Owner: "repository maintainer", Rationale: "synthetic action-privacy regression fixture committed in TASK 156; current source constructs the marker without publishing a literal path",
	},
	{
		BlobID: "7c3573310d2133cf05c5334a817e146db1d1d1c8", Rule: "content/private-path", Line: 566,
		Owner: "repository maintainer", Rationale: "synthetic action-privacy regression fixture committed in TASK 156; current source constructs the marker without publishing a literal path",
	},
	{
		BlobID: "7c3573310d2133cf05c5334a817e146db1d1d1c8", Rule: "content/private-path", Line: 568,
		Owner: "repository maintainer", Rationale: "synthetic action-privacy regression fixture committed in TASK 156; current source constructs the marker without publishing a literal path",
	},
	{
		BlobID: "7c3573310d2133cf05c5334a817e146db1d1d1c8", Rule: "content/private-path", Line: 569,
		Owner: "repository maintainer", Rationale: "synthetic action-privacy regression fixture committed in TASK 156; current source constructs the marker without publishing a literal path",
	},
	{
		BlobID: "26f3bdfdf22e5261dee3a1e1f1a3fd10e4c07564", Rule: "content/access-token", Line: 149,
		Owner: "repository maintainer", Rationale: "synthetic credential-detector regression fixture committed in TASK 159; current source constructs the token from non-matching fragments",
	},
	{
		BlobID: "26f3bdfdf22e5261dee3a1e1f1a3fd10e4c07564", Rule: "content/access-token", Line: 163,
		Owner: "repository maintainer", Rationale: "synthetic credential-detector non-persistence assertion committed in TASK 159; current source reuses the constructed fixture value",
	},
	{
		BlobID: "740d18a5635bb10c1dab6824af5d9842f33806c7", Rule: "content/access-token", Line: 5,
		Owner: "repository maintainer", Rationale: "synthetic AWS credential-detector corpus fixture committed in TASK 159; current corpus joins non-matching fragments at test time",
	},
	{
		BlobID: "740d18a5635bb10c1dab6824af5d9842f33806c7", Rule: "content/access-token", Line: 11,
		Owner: "repository maintainer", Rationale: "synthetic GitHub credential-detector corpus fixture committed in TASK 159; current corpus joins non-matching fragments at test time",
	},
	{
		BlobID: "740d18a5635bb10c1dab6824af5d9842f33806c7", Rule: "content/private-key", Line: 23,
		Owner: "repository maintainer", Rationale: "synthetic private-key detector corpus fixture committed in TASK 159; current corpus joins non-matching fragments at test time",
	},
	{
		BlobID: "b9367841013d2af74436da3fc8abfbb1097f7aaf", Rule: "content/private-path", Line: 21,
		Owner: "repository maintainer", Rationale: "TASK 218 acceptance wording used slash-separated home/user privacy vocabulary; it is documentation, not a filesystem path",
	},
	{
		BlobID: "c5ca06c5aaeb1fb8001186ad314d76a9d58968c3", Rule: "content/access-token", Line: 58,
		Owner: "repository maintainer", Rationale: "TASK 218 provider-token regression corpus used literal synthetic tokens; current corpus joins non-matching fragments at test time",
	},
	{
		BlobID: "1b0707541e54a2f7a7b5bf65cc8968358178e861", Rule: "content/private-path", Line: 55,
		Owner: "repository maintainer", Rationale: "synthetic Windows path redaction fixture; current source constructs the path from non-matching fragments",
	},
	{
		BlobID: "1b0707541e54a2f7a7b5bf65cc8968358178e861", Rule: "content/private-path", Line: 57,
		Owner: "repository maintainer", Rationale: "synthetic Windows path non-persistence assertion; current source constructs the path from non-matching fragments",
	},
	{
		BlobID: "227a25052a7adb0ff861970a2f6547b2743466dc", Rule: "content/private-path", Line: 134,
		Owner: "repository maintainer", Rationale: "synthetic Windows repository-root fixture; current source constructs the path from non-matching fragments",
	},
	{
		BlobID: "227a25052a7adb0ff861970a2f6547b2743466dc", Rule: "content/private-path", Line: 153,
		Owner: "repository maintainer", Rationale: "synthetic Windows neighbor-path fixture; current source constructs the paths from non-matching fragments",
	},
	{
		BlobID: "227a25052a7adb0ff861970a2f6547b2743466dc", Rule: "content/private-path", Line: 158,
		Owner: "repository maintainer", Rationale: "synthetic Windows substring-boundary fixture; current source constructs the path from non-matching fragments",
	},
	{
		BlobID: "227a25052a7adb0ff861970a2f6547b2743466dc", Rule: "content/private-path", Line: 159,
		Owner: "repository maintainer", Rationale: "synthetic Windows substring expected value; current source constructs the path from non-matching fragments",
	},
	{
		BlobID: "9eab0a0433829ad8b347aa40426ddaba6aa1c233", Rule: "content/private-path", Line: 24,
		Owner: "repository maintainer", Rationale: "synthetic TASK 464 Windows path redaction fixture; current source constructs the path from non-matching fragments",
	},
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
		`(?:^|[^A-Za-z0-9])(?:AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-(?:proj-)?[A-Za-z0-9_-]{20,}|xai-[A-Za-z0-9_-]{20,}|AIza[0-9A-Za-z_-]{30,})`,
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
	for _, exception := range historicalFindingExceptions {
		if len(exception.BlobID) != 40 || exception.Rule == "" || exception.Line < 1 || strings.TrimSpace(exception.Owner) == "" || strings.TrimSpace(exception.Rationale) == "" {
			return auditReport{}, errors.New("historical finding exception is incomplete")
		}
		if _, err := hex.DecodeString(exception.BlobID); err != nil {
			return auditReport{}, errors.New("historical finding exception has an invalid Git object ID")
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
	indexedPaths, err := decodeTrackedPaths(body)
	if err != nil {
		return nil, err
	}
	deletedBody, err := gitOutput(ctx, root, "ls-files", "-z", "--deleted")
	if err != nil {
		return nil, err
	}
	deletedPaths, err := decodeTrackedPaths(deletedBody)
	if err != nil {
		return nil, err
	}
	deleted := make(map[string]struct{}, len(deletedPaths))
	for _, path := range deletedPaths {
		deleted[path] = struct{}{}
	}
	paths := make([]string, 0, len(indexedPaths))
	for _, path := range indexedPaths {
		if _, missing := deleted[path]; !missing {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func decodeTrackedPaths(body []byte) ([]string, error) {
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
	auditable := make([]gitObjectMetadata, 0, len(metadata))
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
		auditable = append(auditable, object)
	}
	blobFindings, err := auditGitBlobBatch(ctx, root, auditable)
	if err != nil {
		return nil, 0, err
	}
	findings = append(findings, filterHistoricalFindingExceptions(blobFindings)...)
	return findings, blobCount, nil
}

func filterHistoricalFindingExceptions(findings []auditFinding) []auditFinding {
	out := make([]auditFinding, 0, len(findings))
	for _, finding := range findings {
		excepted := false
		for _, exception := range historicalFindingExceptions {
			if finding.Path == "history/blob/"+exception.BlobID && finding.Rule == exception.Rule && finding.Line == exception.Line {
				excepted = true
				break
			}
		}
		if !excepted {
			out = append(out, finding)
		}
	}
	return out
}

func auditGitBlobBatch(ctx context.Context, root string, objects []gitObjectMetadata) ([]auditFinding, error) {
	if len(objects) == 0 {
		return nil, nil
	}
	objectIDs := make([]string, len(objects))
	for index, object := range objects {
		objectIDs[index] = object.ID
	}
	command := gitexec.CommandContext(ctx, root, nil, "cat-file", "--batch")
	command.Stdin = strings.NewReader(strings.Join(objectIDs, "\n") + "\n")
	stderr, err := boundedexec.NewBuffer(maxPublicationGitBytes)
	if err != nil {
		return nil, err
	}
	command.Stderr = stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open git cat-file --batch output: %w", err)
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start git cat-file --batch: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	reader := bufio.NewReader(stdout)
	findings := []auditFinding{}
	for _, object := range objects {
		header, readErr := reader.ReadString('\n')
		if readErr != nil {
			return nil, abortGitBlobBatch(command, stderr, fmt.Errorf("read header for %s: %w", object.ID, readErr))
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != object.ID || fields[1] != "blob" {
			return nil, abortGitBlobBatch(command, stderr, fmt.Errorf("unexpected Git blob header %q", strings.TrimSpace(header)))
		}
		size, parseErr := strconv.ParseInt(fields[2], 10, 64)
		if parseErr != nil || size < 0 || size > maxPublicationGitBytes || size != object.Size {
			return nil, abortGitBlobBatch(command, stderr, fmt.Errorf("git blob %s size changed during publication audit", object.ID))
		}
		blob := make([]byte, int(size))
		if _, readErr := io.ReadFull(reader, blob); readErr != nil {
			return nil, abortGitBlobBatch(command, stderr, fmt.Errorf("read Git blob %s: %w", object.ID, readErr))
		}
		delimiter, readErr := reader.ReadByte()
		if readErr != nil {
			return nil, abortGitBlobBatch(command, stderr, fmt.Errorf("read delimiter for Git blob %s: %w", object.ID, readErr))
		}
		if delimiter != '\n' {
			return nil, abortGitBlobBatch(command, stderr, fmt.Errorf("invalid delimiter after Git blob %s", object.ID))
		}
		findings = append(findings, auditText("history/blob/"+object.ID, string(blob))...)
	}
	if err := command.Wait(); err != nil {
		return nil, fmt.Errorf("git cat-file --batch: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stderr.Truncated() {
		return nil, fmt.Errorf("git cat-file --batch stderr exceeded %d bytes", maxPublicationGitBytes)
	}
	return findings, nil
}

func abortGitBlobBatch(command *exec.Cmd, stderr *boundedexec.Buffer, cause error) error {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
	_ = command.Wait()
	if detail := strings.TrimSpace(stderr.String()); detail != "" {
		return fmt.Errorf("git cat-file --batch: %w: %s", cause, detail)
	}
	return fmt.Errorf("git cat-file --batch: %w", cause)
}

func gitObjectMetadataBatch(ctx context.Context, root string, objectIDs []string) ([]gitObjectMetadata, error) {
	if len(objectIDs) == 0 {
		return nil, nil
	}
	command := gitexec.CommandContext(ctx, root, nil, "cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)")
	command.Stdin = strings.NewReader(strings.Join(objectIDs, "\n") + "\n")
	body, err := boundedexec.CombinedOutput(command, maxPublicationGitBytes)
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
	return boundedio.ReadRegularFile(path, maxBytes)
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
	command := gitexec.CommandContext(ctx, root, nil, args...)
	body, err := boundedexec.CombinedOutput(command, maxPublicationGitBytes)
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(body)))
	}
	return body, nil
}
