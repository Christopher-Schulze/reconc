// Package pathidentity resolves filesystem paths to their operating-system
// identity. It follows Unix symlinks and Windows reparse points, including
// directory junctions, and preserves not-yet-created suffixes safely.
package pathidentity

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const maxProspectiveResolverAncestors = 1 << 15

// lstatPath observes one path for the resolver; tests count it.
var lstatPath = os.Lstat

// prospectiveAncestor records one inspected path: an existing object with
// its identity and metadata, or, with nil info, a confirmed-missing path.
type prospectiveAncestor struct {
	identity string
	info     os.FileInfo
}

// ProspectiveResolver resolves a bounded batch of paths while reusing only
// filesystem-confirmed observations. Each cached entry is Lstat'ed again
// before reuse (while WithListedParents runs, once per call and once more
// when the call ends): an existing ancestor must keep its identity, its
// metadata, and the spelling the operating system reports for it, and a
// missing ancestor must still be missing, so a symlink, junction, rename of
// any ancestor, creation, or metadata replacement cannot silently inherit an
// old result.
// An existing non-symlink object derives its identity from its revalidated
// parent directory instead of resolving every ancestor again.
type ProspectiveResolver struct {
	ancestors map[string]prospectiveAncestor
	// recordedMissing reports whether any confirmed-missing entry was stored,
	// so lookups skip the ancestor scan until one exists.
	recordedMissing bool
	spellings       storedSpellings
	// listedParents and observations exist only while WithListedParents
	// runs; they hold the directories that call already inspected and the
	// paths it already observed. observationsFull records that a path went
	// unrecorded because observations reached its bound.
	listedParents    map[string]listedParent
	observations     map[string]pathObservation
	observationsFull bool
	// verifiedSpellings exists only during WithConfirmedSpellings; it holds
	// the reused identities that call already confirmed.
	verifiedSpellings map[string]string
}

// listedParent is a directory inspected once during WithListedParents: its
// identity and the metadata observed for its canonical path.
type listedParent struct {
	identity string
	info     os.FileInfo
}

// pathObservation is one Lstat result held for a WithListedParents call: an
// existing object's metadata, or the error that confirmed it missing.
type pathObservation struct {
	info os.FileInfo
	err  error
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
// ancestor cache, as one WithConfirmedSpellings pass.
func (r *ProspectiveResolver) ResolveBatch(paths []string) ([]string, error) {
	if r == nil {
		r = NewProspectiveResolver()
	}
	resolved := make([]string, 0, len(paths))
	err := r.WithConfirmedSpellings(func() error {
		for _, path := range paths {
			identity, err := r.Resolve(path)
			if err != nil {
				return err
			}
			resolved = append(resolved, identity)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

// WithConfirmedSpellings runs resolve with every reused identity confirmed
// against the operating system once for the whole call instead of once per
// path, for one pass over many paths. Each reuse still revalidates the
// object's own metadata; a rename above it during the call can go unnoticed
// for its later paths in the same call.
func (r *ProspectiveResolver) WithConfirmedSpellings(resolve func() error) error {
	if r == nil || r.verifiedSpellings != nil {
		return resolve()
	}
	r.verifiedSpellings = make(map[string]string)
	defer func() { r.verifiedSpellings = nil }()
	return resolve()
}

// WithListedParents runs resolve with every path observed once for the
// whole call instead of on every use, for bulk recorded evidence. A regular
// file or directory stored under its exact name in an inspected parent's
// listing takes that parent's identity plus the name without being inspected
// itself; every other path resolves as Resolve does against the call's
// observations. When resolve returns, every observation is taken again. If
// any path was created, removed, or replaced meanwhile, resolve runs once
// more with every path observed on every use, so no identity the call
// returns rests on an observation that changed during the call; resolve must
// therefore replace, never accumulate, its results. A directory replaced
// during that second run can still go unnoticed for its later paths, so the
// call is never used for a path that a decision will write. Reused
// identities are confirmed once per run, as WithConfirmedSpellings does.
func (r *ProspectiveResolver) WithListedParents(resolve func() error) error {
	if r == nil || r.listedParents != nil {
		return resolve()
	}
	current, err := r.listedRun(resolve, true)
	if err != nil || current {
		return err
	}
	_, err = r.listedRun(resolve, false)
	return err
}

// listedRun runs resolve with listed parents. With observe, every path is
// observed once for the run, and current reports whether every observation
// still holds when resolve returns.
func (r *ProspectiveResolver) listedRun(resolve func() error, observe bool) (current bool, err error) {
	r.listedParents = make(map[string]listedParent)
	if observe {
		r.observations = make(map[string]pathObservation)
	}
	defer func() { r.listedParents, r.observations, r.observationsFull = nil, nil, false }()
	if err := r.WithConfirmedSpellings(resolve); err != nil {
		return false, err
	}
	return r.observationsCurrent(), nil
}

// observationsCurrent reports whether every observation of the run still
// holds: an existing path keeps its identity and metadata, and a missing path
// is still missing. A run whose observations reached their bound cannot
// prove that.
func (r *ProspectiveResolver) observationsCurrent() bool {
	if r.observationsFull {
		return false
	}
	for path, observed := range r.observations {
		info, err := lstatPath(path)
		if observed.err != nil {
			if !os.IsNotExist(err) {
				return false
			}
			continue
		}
		if err != nil || !sameProspectiveAncestor(observed.info, info) {
			return false
		}
	}
	return true
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
	if identity, ok := r.resolveListed(absolute); ok {
		return identity, nil
	}
	return r.resolveAbsolute(path, absolute)
}

// resolveAbsolute walks from absolute to its deepest existing ancestor,
// revalidating every reused observation; path names the request in errors.
func (r *ProspectiveResolver) resolveAbsolute(path, absolute string) (string, error) {
	cursor := r.skipConfirmedMissing(absolute)
	for {
		if cached, ok := r.ancestors[cursor]; ok && cached.info != nil {
			current, err := r.lstat(cursor)
			if err == nil && r.reusable(cursor, cached, current) {
				return r.existingAncestorResult(cached.identity, absolute, cursor), nil
			}
			delete(r.ancestors, cursor)
		}
		info, statErr := r.lstat(cursor)
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

// resolveListed answers an existing regular file or directory from its
// parent's listing while WithListedParents runs.
func (r *ProspectiveResolver) resolveListed(absolute string) (string, bool) {
	if r.listedParents == nil || !composesExistingIdentity {
		return "", false
	}
	directory := filepath.Dir(absolute)
	if directory == absolute {
		return "", false
	}
	parent, seen := r.listedParents[directory]
	if !seen {
		parent = r.inspectListedParent(directory)
		r.listedParents[directory] = parent
	}
	if parent.info == nil {
		return "", false
	}
	name := filepath.Base(absolute)
	mode, listed := r.spellings.listedType(parent.identity, parent.info, name)
	if !listed || (!mode.IsDir() && !mode.IsRegular()) {
		return "", false
	}
	return filepath.Join(parent.identity, name), true
}

// inspectListedParent resolves a parent without the listing shortcut:
// resolving it from its own parent's listing would list every ancestor up to
// the root, including large shared directories. A parent below an ancestor
// that is still confirmed missing is missing too.
func (r *ProspectiveResolver) inspectListedParent(directory string) listedParent {
	if r.skipConfirmedMissing(directory) != directory {
		return listedParent{}
	}
	identity, err := r.resolveAbsolute(directory, directory)
	if err != nil {
		return listedParent{}
	}
	info, err := r.lstat(identity)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return listedParent{}
	}
	return listedParent{identity: identity, info: info}
}

// lstat observes path once per observing WithListedParents run and on every
// use otherwise. A run holds only existing and confirmed-missing results, so
// any other failure is observed again; past the bound a path is observed on
// every use and the run can no longer prove its observations current.
func (r *ProspectiveResolver) lstat(path string) (os.FileInfo, error) {
	if observed, ok := r.observations[path]; ok {
		return observed.info, observed.err
	}
	info, err := lstatPath(path)
	if r.observations != nil && (err == nil || os.IsNotExist(err)) {
		if len(r.observations) >= maxProspectiveResolverAncestors {
			r.observationsFull = true
		} else {
			r.observations[path] = pathObservation{info: info, err: err}
		}
	}
	return info, err
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
		if _, err := r.lstat(ancestor); !os.IsNotExist(err) {
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
		current, err := r.lstat(parent)
		if err == nil && r.reusable(parent, cached, current) {
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
	parentInfo, parentErr := r.lstat(parent)
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

// reusable reports whether a cached existing observation of path still
// holds: the object keeps its identity and metadata, and the operating system
// still spells it as the cached identity. A rename of any ancestor, including
// a case-only one, changes that spelling without touching the object itself.
// While WithConfirmedSpellings runs, an identity is confirmed once for the
// call.
func (r *ProspectiveResolver) reusable(path string, cached prospectiveAncestor, current os.FileInfo) bool {
	if !sameProspectiveAncestor(cached.info, current) {
		return false
	}
	if confirmed, ok := r.verifiedSpellings[path]; ok && confirmed == cached.identity {
		return true
	}
	if !spellingCurrent(path, cached.identity, current) {
		return false
	}
	if r.verifiedSpellings != nil {
		r.verifiedSpellings[path] = cached.identity
	}
	return true
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
		left.ModTime().Equal(right.ModTime()) && sameChangeTime(left, right)
}

// sameChangeTime compares inode change times where the platform reports
// them: a rename, including a case-only one, changes the renamed entry's
// change time but not its modification time.
func sameChangeTime(left, right os.FileInfo) bool {
	leftTime, leftOK := changeTime(left)
	rightTime, rightOK := changeTime(right)
	return leftOK == rightOK && leftTime == rightTime
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

// IdentityCurrent reports whether an existing directory or regular file
// still resolves to identity, a value ResolveExisting returned for it. On
// macOS and Windows one descriptor or handle lookup confirms the stored
// spelling of every component, so a case-only rename of any of them makes
// the identity stale without resolving each ancestor again.
func IdentityCurrent(path, identity string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		resolved, err := ResolveExisting(path)
		return err == nil && resolved == identity
	}
	return spellingCurrent(path, identity, info)
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
