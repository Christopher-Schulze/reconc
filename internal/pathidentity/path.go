// Package pathidentity resolves filesystem paths to their operating-system
// identity. It follows Unix symlinks and Windows reparse points, including
// directory junctions, and preserves not-yet-created suffixes safely.
package pathidentity

import (
	"fmt"
	"os"
	"path/filepath"
)

const maxProspectiveResolverAncestors = 4096

type prospectiveAncestor struct {
	identity string
	info     os.FileInfo
}

// ProspectiveResolver resolves a bounded batch of paths while reusing only
// filesystem-confirmed existing ancestors. Each cached ancestor is Lstat'ed
// again before reuse, so a symlink, junction, rename, or metadata replacement
// cannot silently inherit an old identity.
type ProspectiveResolver struct {
	ancestors map[string]prospectiveAncestor
}

// NewProspectiveResolver creates an empty evaluation-scoped resolver. It is
// intentionally not process-global: identities are never reused across
// unrelated filesystem snapshots or repository roots.
func NewProspectiveResolver() *ProspectiveResolver {
	return &ProspectiveResolver{ancestors: make(map[string]prospectiveAncestor)}
}

// ResolveProspectiveBatch resolves every input in order, preserving duplicate
// entries and returning the first error with its original input context.
func ResolveProspectiveBatch(paths []string) ([]string, error) {
	resolver := NewProspectiveResolver()
	return resolver.ResolveBatch(paths)
}

// ResolveBatch resolves every input in order with this resolver's bounded
// ancestor cache.
func (r *ProspectiveResolver) ResolveBatch(paths []string) ([]string, error) {
	if r == nil {
		r = NewProspectiveResolver()
	}
	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		identity, err := r.Resolve(path)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, identity)
	}
	return resolved, nil
}

// Resolve resolves one prospective path and revalidates every reused
// existing ancestor before using its identity.
func (r *ProspectiveResolver) Resolve(path string) (string, error) {
	if r == nil {
		r = NewProspectiveResolver()
	}
	absolute := filepath.Clean(path)
	if !filepath.IsAbs(absolute) {
		var err error
		absolute, err = filepath.Abs(absolute)
		if err != nil {
			return "", fmt.Errorf("resolve absolute path %q: %w", path, err)
		}
	}
	cursor := absolute
	missing := []string{}
	for {
		if cached, ok := r.ancestors[cursor]; ok {
			current, err := os.Lstat(cursor)
			if err == nil && sameProspectiveAncestor(cached.info, current) {
				return appendProspectiveSuffix(cached.identity, missing), nil
			}
			delete(r.ancestors, cursor)
		}
		info, statErr := os.Lstat(cursor)
		if statErr == nil {
			resolved, resolveErr := ResolveExisting(cursor)
			if resolveErr != nil {
				return "", resolveErr
			}
			if len(r.ancestors) >= maxProspectiveResolverAncestors {
				r.ancestors = make(map[string]prospectiveAncestor)
			}
			r.ancestors[cursor] = prospectiveAncestor{identity: resolved, info: info}
			return appendProspectiveSuffix(resolved, missing), nil
		}
		if !os.IsNotExist(statErr) {
			return "", fmt.Errorf("inspect path %q: %w", cursor, statErr)
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", fmt.Errorf("cannot resolve an existing ancestor for %q", path)
		}
		missing = append(missing, filepath.Base(cursor))
		cursor = parent
	}
}

func appendProspectiveSuffix(identity string, missing []string) string {
	for index := len(missing) - 1; index >= 0; index-- {
		identity = filepath.Join(identity, missing[index])
	}
	return filepath.Clean(identity)
}

func sameProspectiveAncestor(left, right os.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) &&
		left.Mode() == right.Mode() && left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime())
}

// ResolveExisting returns the absolute operating-system identity of an
// existing path. On Windows this also expands 8.3 aliases and follows reparse
// points; on Unix it resolves symbolic links.
func ResolveExisting(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", path, err)
	}
	resolved, err := resolveExistingOS(filepath.Clean(absolute))
	if err != nil {
		return "", fmt.Errorf("resolve filesystem identity %q: %w", path, err)
	}
	return filepath.Clean(resolved), nil
}

// ResolveProspective resolves the longest existing ancestor of path to its
// operating-system identity, then appends the missing suffix. This makes
// containment checks safe before a target is created.
func ResolveProspective(path string) (string, error) {
	return NewProspectiveResolver().Resolve(path)
}

// ExistingAliases returns filesystem-confirmed spellings of one existing
// path. Windows includes the 8.3 spelling when available; all platforms
// include both the input spelling and its resolved identity.
func ExistingAliases(path string) ([]string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute path %q: %w", path, err)
	}
	absolute = filepath.Clean(absolute)
	resolved, err := ResolveExisting(absolute)
	if err != nil {
		return nil, err
	}
	candidates := append([]string{absolute, resolved}, existingAliasesOS(resolved)...)
	aliases := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		cleaned := filepath.Clean(candidate)
		key := aliasComparisonKey(cleaned)
		if cleaned == "" || seen[key] {
			continue
		}
		seen[key] = true
		aliases = append(aliases, cleaned)
	}
	return aliases, nil
}
