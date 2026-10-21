package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/pathidentity"
)

func rejectAliasedComparisonOutput(output, baselinePath, resultPath string) error {
	if strings.TrimSpace(output) == "" {
		return nil
	}
	if err := rejectPathAlias(output, baselinePath, "baseline"); err != nil {
		return err
	}
	return rejectPathAlias(output, resultPath, "result")
}

func rejectPathAlias(output, input, label string) error {
	outAbs, err := canonicalComparisonPath(output)
	if err != nil {
		return err
	}
	inAbs, err := canonicalComparisonPath(input)
	if err != nil {
		return err
	}
	if outAbs == inAbs || sameExistingFile(outAbs, inAbs) {
		return fmt.Errorf("comparison output aliases the %s input", label)
	}
	return nil
}

func canonicalComparisonPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("comparison path is empty")
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve comparison path %q: %w", path, err)
	}
	return filepath.Clean(absolute), nil
}

func sameExistingFile(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}

// distinctOutputPaths reports whether two output paths resolve to different
// files; unresolvable paths count as aliases so recording fails closed.
func distinctOutputPaths(left, right string) bool {
	resolved, err := pathidentity.ResolveProspectiveBatch([]string{left, right})
	return err == nil && resolved[0] != resolved[1] && !sameExistingFile(resolved[0], resolved[1])
}
