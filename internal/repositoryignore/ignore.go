// Package repositoryignore owns the single managed .gitignore block used by
// every Reconc onboarding path.
package repositoryignore

import (
	"fmt"
	"strings"
)

const (
	// RelativePath is the repository-relative ignore file managed by Reconc.
	RelativePath = ".gitignore"
	// BlockStart and BlockEnd delimit the only section Reconc may replace.
	BlockStart = "# >>> reconc bootstrap runtime"
	BlockEnd   = "# <<< reconc bootstrap runtime"
)

// Body returns the stable ignore rules for Reconc-owned runtime state. The
// compiled policy lockfile remains committable.
func Body() string {
	return `/tools/reconc/dist/
.reconc/*
!.reconc/
!.reconc/policy.lock.json
.reconc/audit.jsonl*
.reconc/cache/
.reconc/locks/
.reconc/reports/
.reconc/run/
.reconc/sessions/
.reconc/task-transaction.json
.reconc/bootstrap-*.json
*.reconc-candidate-*
*.reconc-remove-candidate-*`
}

// Block returns the complete marker-delimited managed section.
func Block() string {
	return BlockStart + "\n" + Body() + "\n" + BlockEnd + "\n"
}

// Merge appends or replaces exactly one managed section while preserving all
// user-owned bytes outside it. Duplicate or incomplete markers fail closed.
func Merge(existing string) (string, error) {
	if strings.Count(existing, BlockStart) > 1 || strings.Count(existing, BlockEnd) > 1 {
		return "", fmt.Errorf("%s has duplicate reconc bootstrap managed block markers", RelativePath)
	}
	startIndex := strings.Index(existing, BlockStart)
	endIndex := strings.Index(existing, BlockEnd)
	if startIndex == -1 && endIndex == -1 {
		separator := ""
		if existing != "" && !strings.HasSuffix(existing, "\n") {
			separator = "\n"
		}
		if existing != "" {
			separator += "\n"
		}
		return existing + separator + Block(), nil
	}
	if startIndex == -1 || endIndex == -1 || endIndex < startIndex {
		return "", fmt.Errorf("%s has an incomplete reconc bootstrap managed block", RelativePath)
	}
	endIndex += len(BlockEnd)
	if endIndex < len(existing) && existing[endIndex] == '\n' {
		endIndex++
	}
	return existing[:startIndex] + Block() + existing[endIndex:], nil
}
