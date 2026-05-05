// Package runtime hosts the evaluator, evidence ingestion, and check
// report types that turn a compiled lockfile + runtime evidence into a
// pass/warn/block decision.
//
// This file: path matching with globstar (**) support.
//
// We delegate the actual glob matching to github.com/bmatcuk/doublestar
// because correct globstar matching across edge cases (escape sequences,
// character classes, the difference between `**` and `*`, the leading-`/`
// boundary, etc.) is exactly the sort of code that should not be
// reinvented for a security-relevant predicate.
package runtime

import (
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// MatchPath reports whether the given POSIX-style path matches the
// glob pattern using fnmatch + ** semantics:
//
//   - *      matches any sequence of chars except '/'
//   - **     matches any sequence including '/'
//   - ?      matches any single non-'/' char
//   - [abc]  character class
//   - all other chars match literally
//
// Both pattern and path are compared as case-sensitive POSIX paths
// (forward slashes). Callers are responsible for normalizing OS-native
// paths to POSIX form before calling MatchPath.
//
// Errors from the underlying matcher (malformed pattern) are reported
// as a non-nil error AND a false match. Callers usually want to treat
// a match error as a violation of the rule itself rather than silently
// passing.
func MatchPath(pattern, path string) (bool, error) {
	// Normalize both inputs to POSIX form so OS-native backslashes
	// don't trip up the matcher on Windows.
	p := filepath.ToSlash(strings.TrimSpace(pattern))
	q := filepath.ToSlash(strings.TrimSpace(path))
	return doublestar.Match(p, q)
}

// MatchAny reports whether path matches any of the given patterns.
// Returns the first matching pattern (or empty string), the matched
// boolean, and any error from underlying matching.
//
// On the first error encountered, returns ("", false, err) without
// consulting later patterns. This is intentional: a bad pattern in the
// rule set should fail loudly, not silently.
func MatchAny(patterns []string, path string) (matchedPattern string, matched bool, err error) {
	for _, pat := range patterns {
		ok, err := MatchPath(pat, path)
		if err != nil {
			return "", false, err
		}
		if ok {
			return pat, true, nil
		}
	}
	return "", false, nil
}

// MatchAnyPath reports whether ANY of the paths matches ANY of the
// patterns. Used by rule kinds that ask "did the agent touch any file
// in this set?".
//
// Returns (matchedPath, matchedPattern, matched, err). matchedPath and
// matchedPattern carry the FIRST hit so callers can populate violation
// reports with concrete examples.
func MatchAnyPath(patterns, paths []string) (matchedPath, matchedPattern string, matched bool, err error) {
	for _, pp := range paths {
		pat, ok, err := MatchAny(patterns, pp)
		if err != nil {
			return "", "", false, err
		}
		if ok {
			return pp, pat, true, nil
		}
	}
	return "", "", false, nil
}
