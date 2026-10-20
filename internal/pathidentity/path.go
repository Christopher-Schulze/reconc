// Package pathidentity resolves filesystem paths to their operating-system
// identity. It follows Unix symlinks and Windows reparse points, including
// directory junctions, and preserves not-yet-created suffixes safely.
package pathidentity

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxProspectiveResolverAncestors = 1 << 15

// prospectiveAncestor records one inspected path: an existing object with
// its identity and metadata, or, with nil info, a confirmed-missing path.
type prospectiveAncestor struct {
	identity string
	info     os.FileInfo
}

// ProspectiveResolver resolves a bounded batch of paths while reusing only
// filesystem-confirmed observations. Each cached entry is Lstat'ed again
// before reuse: an existing ancestor must keep its identity and metadata, and
// a missing ancestor must still be missing, so a symlink, junction, rename,
// creation, or metadata replacement cannot silently inherit an old result.
// An existing non-symlink object derives its identity from its revalidated
// parent directory instead of resolving every ancestor again.
type ProspectiveResolver struct {
	ancestors map[string]prospectiveAncestor
	// recordedMissing reports whether any confirmed-missing entry was stored,
	// so lookups skip the ancestor scan until one exists.
	recordedMissing bool
	spellings       storedSpellings
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
// observation before using it.
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
	cursor := r.skipConfirmedMissing(absolute)
	for {
		if cached, ok := r.ancestors[cursor]; ok && cached.info != nil {
			current, err := os.Lstat(cursor)
			if err == nil && sameProspectiveAncestor(cached.info, current) {
				return r.existingAncestorResult(cached.identity, absolute, cursor), nil
			}
			delete(r.ancestors, cursor)
		}
		info, statErr := os.Lstat(cursor)
		if statErr == nil {
			resolved, resolveErr := r.resolveExisting(cursor, info)
			if resolveErr != nil {
				return "", resolveErr
			}
			r.remember(cursor, prospectiveAncestor{identity: resolved, info: info})
			return r.existingAncestorResult(resolved, absolute, cursor), nil
		}
		if !os.IsNotExist(statErr) {
			return "", fmt.Errorf("inspect path %q: %w", cursor, statErr)
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", fmt.Errorf("cannot resolve an existing ancestor for %q", path)
		}
		cursor = parent
	}
}

// existingAncestorResult records the missing child of the deepest existing
// ancestor, then appends the missing suffix to that ancestor's identity.
func (r *ProspectiveResolver) existingAncestorResult(identity, absolute, ancestor string) string {
	if ancestor != absolute {
		missing := firstMissingComponent(absolute, ancestor)
		if _, recorded := r.ancestors[missing]; !recorded {
			r.remember(missing, prospectiveAncestor{})
		}
	}
	return joinProspectiveSuffix(identity, absolute, ancestor)
}

// skipConfirmedMissing returns where the existing-ancestor walk for absolute
// starts. When the shallowest ancestor recorded as missing is still missing,
// every deeper component is missing too, so the walk resumes at its parent.
func (r *ProspectiveResolver) skipConfirmedMissing(absolute string) string {
	if !r.recordedMissing {
		return absolute
	}
	volume := len(filepath.VolumeName(absolute))
	for index := volume + 1; index <= len(absolute); index++ {
		if index < len(absolute) && !os.IsPathSeparator(absolute[index]) {
			continue
		}
		ancestor := absolute[:index]
		cached, ok := r.ancestors[ancestor]
		if !ok || cached.info != nil {
			continue
		}
		if _, err := os.Lstat(ancestor); !os.IsNotExist(err) {
			delete(r.ancestors, ancestor)
			return absolute
		}
		return filepath.Dir(ancestor)
	}
	return absolute
}

// resolveExisting returns the identity of an existing path. A non-symlink
// inside a cached, revalidated directory derives its identity from that
// directory. Otherwise the full path is resolved, and when that identity
// proves its parent's identity, the parent is cached for its siblings.
func (r *ProspectiveResolver) resolveExisting(path string, info os.FileInfo) (string, error) {
	parent := filepath.Dir(path)
	if !composesExistingIdentity || info.Mode()&os.ModeSymlink != 0 || parent == path {
		return ResolveExisting(path)
	}
	if cached, ok := r.ancestors[parent]; ok && cached.info != nil {
		current, err := os.Lstat(parent)
		if err == nil && sameProspectiveAncestor(cached.info, current) {
			if identity, ok := r.spellings.compose(cached.identity, current, filepath.Base(path), info); ok {
				return identity, nil
			}
		} else {
			delete(r.ancestors, parent)
		}
	}
	if len(r.ancestors) == 0 {
		// A resolver's first existing path skips the parent inspection that
		// only later siblings could reuse, so one-path resolutions cost no more.
		return ResolveExisting(path)
	}
	// The parent's metadata is captured before resolution, so a parent that
	// changes during resolution fails the next revalidation.
	parentInfo, parentErr := os.Lstat(parent)
	resolved, parentProven, err := resolveExistingOSWithParent(path)
	if err != nil {
		return "", fmt.Errorf("resolve filesystem identity %q: %w", path, err)
	}
	resolved = filepath.Clean(resolved)
	if parentProven && parentErr == nil {
		r.remember(parent, prospectiveAncestor{identity: filepath.Dir(resolved), info: parentInfo})
	}
	return resolved, nil
}

func (r *ProspectiveResolver) remember(path string, observation prospectiveAncestor) {
	if len(r.ancestors) >= maxProspectiveResolverAncestors {
		r.ancestors = make(map[string]prospectiveAncestor)
	}
	r.ancestors[path] = observation
	if observation.info == nil {
		r.recordedMissing = true
	}
}

// firstMissingComponent returns the child of ancestor on the way to absolute.
func firstMissingComponent(absolute, ancestor string) string {
	rest := strings.TrimLeftFunc(absolute[len(ancestor):], isPathSeparatorRune)
	if index := strings.IndexFunc(rest, isPathSeparatorRune); index >= 0 {
		rest = rest[:index]
	}
	return filepath.Join(ancestor, rest)
}

func isPathSeparatorRune(r rune) bool {
	return r == '/' || r == filepath.Separator
}

// joinProspectiveSuffix appends the part of absolute below its resolved
// ancestor to that ancestor's identity.
func joinProspectiveSuffix(identity, absolute, ancestor string) string {
	if ancestor == absolute {
		return filepath.Clean(identity)
	}
	return filepath.Join(identity, absolute[len(ancestor):])
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
