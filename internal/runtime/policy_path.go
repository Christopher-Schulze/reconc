package runtime

import (
	"fmt"
	"path/filepath"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/pathidentity"
)

const maxEvidenceFileBytes int64 = 4 << 20

func resolvePolicyFile(repoRoot, relative string) (string, error) {
	configured := filepath.FromSlash(relative)
	cleaned := filepath.Clean(configured)
	if configured == "" || filepath.IsAbs(configured) || filepath.VolumeName(configured) != "" || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", &rerrors.RepoBoundaryError{Path: relative, RepoRoot: repoRoot}
	}
	resolvedRoot, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository root for policy file %q: %w", relative, err)
	}
	resolved, err := pathidentity.ResolveProspective(filepath.Join(resolvedRoot, cleaned))
	if err != nil {
		return "", fmt.Errorf("resolve policy file %q: %w", relative, err)
	}
	rel, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil {
		return "", fmt.Errorf("validate policy file %q containment: %w", relative, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", &rerrors.RepoBoundaryError{Path: relative, RepoRoot: resolvedRoot}
	}
	return resolved, nil
}
