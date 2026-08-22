package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"reconc-harness/template/audits/lib/reconcbinary"
)

type gitDiffFile struct {
	path       string
	addedLines []string
	changed    bool
}

var testSkipPattern = regexp.MustCompile(`\bt\.Skip(?:f|Now)?\s*\(`)

func auditAgentQuality(root string) []string {
	var failures []string
	failures = append(failures, auditReconcBinaryFreshness(root)...)
	diffFiles, diffFailures := collectGitDiffFiles(root)
	failures = append(failures, diffFailures...)
	if len(diffFailures) > 0 {
		return failures
	}
	failures = append(failures, auditChangedQuality(diffFiles)...)
	return failures
}

func collectGitDiffFiles(root string) (map[string]*gitDiffFile, []string) {
	insideCommand, cancel := commandWithTimeout(shortAuditCommandTimeout, "git", "-C", root, "rev-parse", "--is-inside-work-tree")
	inside, err := runPreparedAuditCommand(insideCommand, true)
	cancel()
	if err != nil {
		if _, statErr := os.Stat(filepath.Join(root, ".git")); statErr == nil {
			return nil, []string{fmt.Sprintf("git rev-parse --is-inside-work-tree failed: %v: %s", err, strings.TrimSpace(string(inside)))}
		}
		return nil, nil
	}
	if strings.TrimSpace(string(inside)) != "true" {
		return nil, nil
	}
	files := map[string]*gitDiffFile{}
	var failures []string
	for _, args := range [][]string{
		{"diff", "--cached", "--unified=0", "--no-ext-diff", "--"},
		{"diff", "--unified=0", "--no-ext-diff", "--"},
	} {
		cmd, cancel := commandWithTimeout(shortAuditCommandTimeout, "git", append([]string{"-C", root}, args...)...)
		out, err := runPreparedAuditCommand(cmd, true)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Sprintf("git %s failed: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out))))
			continue
		}
		mergeGitDiffFiles(files, parseGitDiffFiles(string(out)))
	}
	return files, failures
}

func mergeGitDiffFiles(dst map[string]*gitDiffFile, src map[string]*gitDiffFile) {
	for path, file := range src {
		existing := dst[path]
		if existing == nil {
			dst[path] = file
			continue
		}
		existing.changed = existing.changed || file.changed
		existing.addedLines = append(existing.addedLines, file.addedLines...)
	}
}

func parseGitDiffFiles(output string) map[string]*gitDiffFile {
	files := map[string]*gitDiffFile{}
	var current *gitDiffFile
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				path := strings.TrimPrefix(parts[3], "b/")
				if path != "/dev/null" {
					current = ensureGitDiffFile(files, path)
				}
			}
		case strings.HasPrefix(line, "+++ b/"):
			path := strings.TrimPrefix(line, "+++ b/")
			if path != "/dev/null" {
				current = ensureGitDiffFile(files, path)
			}
		case current != nil && strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			current.changed = true
			current.addedLines = append(current.addedLines, strings.TrimPrefix(line, "+"))
		case current != nil && strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			current.changed = true
		}
	}
	return files
}

func ensureGitDiffFile(files map[string]*gitDiffFile, path string) *gitDiffFile {
	path = filepath.ToSlash(path)
	file := files[path]
	if file == nil {
		file = &gitDiffFile{path: path}
		files[path] = file
	}
	return file
}

func auditChangedQuality(files map[string]*gitDiffFile) []string {
	var failures []string
	changedTestDirs := map[string]bool{}
	sensitiveProdDirs := map[string][]string{}
	for _, file := range files {
		path := filepath.ToSlash(file.path)
		if strings.HasSuffix(path, "_test.go") && file.changed {
			changedTestDirs[filepath.ToSlash(filepath.Dir(path))] = true
		}
		if isSensitiveProductionGo(path) && file.changed {
			dir := filepath.ToSlash(filepath.Dir(path))
			sensitiveProdDirs[dir] = append(sensitiveProdDirs[dir], path)
		}
		for _, line := range file.addedLines {
			failures = append(failures, auditAddedLineQuality(path, line)...)
		}
	}
	for dir, paths := range sensitiveProdDirs {
		if !changedTestDirs[dir] {
			sort.Strings(paths)
			failures = append(failures, fmt.Sprintf("%s: sensitive Go changes require a same-package *_test.go change in this diff; changed production files: %s", dir, strings.Join(paths, ", ")))
		}
	}
	return failures
}

func auditAddedLineQuality(path string, line string) []string {
	var failures []string
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	if strings.HasSuffix(path, "_test.go") && testSkipPattern.MatchString(trimmed) {
		failures = append(failures, fmt.Sprintf("%s: added test skip is forbidden; fix the test or code instead of hiding coverage", path))
	}
	if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
		notImplementedPanic := "panic(\"not " + "implemented"
		todoPanic := "panic(\"todo"
		if strings.Contains(lower, notImplementedPanic) || strings.Contains(lower, todoPanic) {
			failures = append(failures, fmt.Sprintf("%s: added not-implemented panic is forbidden in production Go", path))
		}
		if comment := lineComment(trimmed); comment != "" {
			commentLower := strings.ToLower(comment)
			for _, token := range []string{"todo", "fixme", "stub", "placeholder", "temporary", "not implemented"} {
				if strings.Contains(commentLower, token) {
					failures = append(failures, fmt.Sprintf("%s: added prohibited production comment marker %q", path, token))
				}
			}
		}
	}
	if strings.HasPrefix(path, "docs/tasks/") && strings.HasSuffix(path, ".md") {
		if strings.HasPrefix(trimmed, "- Completion Claim:") || strings.HasPrefix(trimmed, "- Reality Check:") {
			for _, phrase := range []string{"will implement later", "for now", "basic implementation", "simple placeholder", "skeleton", "not yet implemented", "temporary solution", "left as exercise", "see todo"} {
				if strings.Contains(lower, phrase) {
					failures = append(failures, fmt.Sprintf("%s: added prohibited TASK/doc completion phrase %q", path, phrase))
				}
			}
		}
	}
	return failures
}

func lineComment(line string) string {
	const (
		goCode byte = iota
		goInterpretedString
		goRawString
		goRune
	)
	state := goCode
	escaped := false
	for index := 0; index < len(line); index++ {
		current := line[index]
		switch state {
		case goCode:
			switch current {
			case '"':
				state = goInterpretedString
			case '`':
				state = goRawString
			case '\'':
				state = goRune
			case '/':
				if index+1 < len(line) && line[index+1] == '/' {
					return line[index+2:]
				}
			}
		case goRawString:
			if current == '`' {
				state = goCode
			}
		case goInterpretedString, goRune:
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if state == goInterpretedString && current == '"' || state == goRune && current == '\'' {
				state = goCode
			}
		}
	}
	return ""
}

func isSensitiveProductionGo(path string) bool {
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || isGeneratedGoPath(path) {
		return false
	}
	for _, prefix := range []string{
		"backend/project/internal/policy/",
		"backend/project/internal/permissions/",
		"backend/project/internal/security/",
		"backend/project/internal/tools/",
		"codebase/backend/project/internal/policy/",
		"codebase/backend/project/internal/permissions/",
		"codebase/backend/project/internal/security/",
		"codebase/backend/project/internal/tools/",
		"tools/reconc/internal/",
		"tools/reconc/harness/project/",
		"tools/reconc/harness/template/",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func isGeneratedGoPath(path string) bool {
	base := filepath.Base(path)
	return strings.Contains(base, ".generated.") || strings.HasSuffix(base, "_generated.go")
}

func auditReconcBinaryFreshness(root string) []string {
	verified, err := reconcbinary.Open(root, false)
	if err != nil {
		return []string{err.Error()}
	}
	if verified == nil {
		return nil
	}
	if err := verified.Close(); err != nil {
		return []string{fmt.Sprintf("%s cannot be closed after inspection: %v", reconcbinary.RelativePath(), err)}
	}
	return nil
}

func localReconcBinaryRel() string {
	return reconcbinary.RelativePath()
}
