package runtime

import (
	"fmt"

	"reconc.dev/reconc/internal/pathidentity"
)

func normalizePaths(paths []string, root string) ([]string, error) {
	resolvedRoot, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repo filesystem identity: %w", err)
	}
	return normalizePathsWithResolvedRoot(paths, resolvedRoot)
}

// normalizePathsWithResolvedRoot is the per-path normalization core. Callers
// on the check hot path resolve the repo root identity once via
// pathidentity.ResolveExisting and reuse it for every path, instead of paying
// the symlink/reparse resolution cost once per evidence path.
func normalizePathsWithResolvedRoot(paths []string, resolvedRoot string) ([]string, error) {
	prospective := pathidentity.NewProspectiveResolver()
	return normalizePathsWithResolver(paths, resolvedRoot, prospective)
}

func normalizeWriteEpochs(paths []string, epochs map[string]uint64, root string) (map[string]uint64, error) {
	resolvedRoot, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repo filesystem identity: %w", err)
	}
	return normalizeWriteEpochsWithResolvedRoot(paths, epochs, resolvedRoot)
}

// normalizeWriteEpochsWithResolvedRoot maps raw write-path epoch keys onto
// their normalized repo-relative form. It reuses the caller-resolved repo
// root so the check hot path resolves the root identity once instead of once
// per write path.
func normalizeWriteEpochsWithResolvedRoot(paths []string, epochs map[string]uint64, resolvedRoot string) (map[string]uint64, error) {
	prospective := pathidentity.NewProspectiveResolver()
	_, normalized, err := normalizeWritesAndEpochsWithResolver(paths, epochs, resolvedRoot, prospective)
	return normalized, err
}
