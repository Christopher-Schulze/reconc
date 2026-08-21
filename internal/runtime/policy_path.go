package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/pathidentity"
)

const maxEvidenceFileBytes int64 = 4 << 20

type evaluationPathState struct {
	repoRoot     string
	resolvedRoot string
	rootIdentity os.FileInfo
	prospective  *pathidentity.ProspectiveResolver
}

func newEvaluationPathState(repoRoot string) (*evaluationPathState, error) {
	return newEvaluationPathStateWithRootResolver(repoRoot, pathidentity.ResolveExisting)
}

func newEvaluationPathStateWithRootResolver(repoRoot string, resolveRoot func(string) (string, error)) (*evaluationPathState, error) {
	resolvedRoot, err := resolveRoot(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repo filesystem identity: %w", err)
	}
	identity, err := os.Stat(resolvedRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect resolved repo filesystem identity: %w", err)
	}
	// Go resolves Windows file IDs lazily inside os.SameFile. Compare two
	// known-current snapshots now so the stored identity is frozen before the
	// lexical root can be replaced during evaluation.
	confirmation, err := os.Stat(resolvedRoot)
	if err != nil {
		return nil, fmt.Errorf("confirm resolved repo filesystem identity: %w", err)
	}
	if !os.SameFile(identity, confirmation) {
		return nil, fmt.Errorf("resolved repository root changed during identity capture")
	}
	return &evaluationPathState{
		repoRoot: repoRoot, resolvedRoot: resolvedRoot, rootIdentity: identity,
		prospective: pathidentity.NewProspectiveResolver(),
	}, nil
}

func (s *evaluationPathState) revalidateRoot() error {
	if s == nil || s.resolvedRoot == "" || s.rootIdentity == nil {
		return fmt.Errorf("resolved repository root identity is unavailable")
	}
	current, err := os.Stat(s.resolvedRoot)
	if err != nil {
		return fmt.Errorf("revalidate repository root filesystem identity: %w", err)
	}
	if !os.SameFile(s.rootIdentity, current) {
		return fmt.Errorf("repository root filesystem identity changed during evaluation")
	}
	return nil
}

func (s *evaluationPathState) resolvePolicyFile(relative string) (string, error) {
	if err := s.revalidateRoot(); err != nil {
		return "", err
	}
	return resolvePolicyFileWithResolvedRoot(s.repoRoot, s.resolvedRoot, relative, s.prospective)
}

func resolvePolicyFile(repoRoot, relative string) (string, error) {
	state, err := newEvaluationPathState(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository root for policy file %q: %w", relative, err)
	}
	return state.resolvePolicyFile(relative)
}

func resolvePolicyFileWithResolvedRoot(repoRoot, resolvedRoot, relative string, prospective *pathidentity.ProspectiveResolver) (string, error) {
	configured := filepath.FromSlash(relative)
	cleaned := filepath.Clean(configured)
	if configured == "" || pathidentity.Rooted(relative) || pathidentity.EscapesLexically(relative) ||
		cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", &rerrors.RepoBoundaryError{Path: relative, RepoRoot: repoRoot}
	}
	if prospective == nil {
		prospective = pathidentity.NewProspectiveResolver()
	}
	resolved, err := prospective.Resolve(filepath.Join(resolvedRoot, cleaned))
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
