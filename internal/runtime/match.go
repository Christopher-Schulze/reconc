// Package runtime hosts the evaluator, evidence ingestion, and check
// report types that turn a compiled lockfile + runtime evidence into a
// pass/warn/block decision.
//
// This file: path matching with globstar (**) support.
//
// Validation and the bounded fallback use github.com/bmatcuk/doublestar
// because correct globstar matching across edge cases (escape sequences,
// character classes, the difference between `**` and `*`, the leading-`/`
// boundary, etc.) is exactly the sort of code that should not be
// reinvented for a security-relevant predicate. Bounded immutable programs
// reuse the differential-tested action glob compiler for the hot path.
package runtime

import (
	"fmt"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/policy"

	"github.com/bmatcuk/doublestar/v4"
)

// CompiledPathMatcher is an immutable POSIX doublestar matcher. Runtime
// policy patterns are validated once when a runtime plan is built and then
// evaluated without calling the error-returning parser on the hot path.
//
// The fallback pattern is retained for valid patterns that exceed the action
// glob compiler's bounded token representation. It still uses the validated
// doublestar fast path and therefore preserves the runtime lockfile grammar
// without admitting an unbounded compiled-plan allocation.
type CompiledPathMatcher struct {
	pattern string
	glob    *action.CompiledGlob
}

const (
	maxRuntimeCompiledPathMatcherBytes = 1 << 20
	maxRuntimeCompiledPathMatchers     = 4 << 20
)

// CompilePathMatcher trims configuration whitespace, validates the complete
// doublestar grammar, and creates the reusable matcher representation. The
// action glob compiler provides tokenized matching for bounded patterns;
// valid patterns outside that explicit bound retain their validated source
// and use doublestar's unchecked matcher safely.
func CompilePathMatcher(pattern string) (CompiledPathMatcher, error) {
	normalized := strings.TrimSpace(pattern)
	if !doublestar.ValidatePattern(normalized) {
		return CompiledPathMatcher{}, doublestar.ErrBadPattern
	}
	matcher := CompiledPathMatcher{pattern: normalized}
	if glob, err := action.CompileGlob(normalized); err == nil {
		matcher.glob = glob
	}
	return matcher, nil
}

// Pattern returns the normalized pattern retained by the compiled matcher.
func (m CompiledPathMatcher) Pattern() string {
	return m.pattern
}

// Match evaluates a previously validated pattern without reparsing its
// grammar. A zero matcher is always a miss.
func (m CompiledPathMatcher) Match(path string) bool {
	if m.pattern == "" && m.glob == nil {
		return false
	}
	if m.glob != nil {
		return m.glob.Match(path)
	}
	return doublestar.MatchUnvalidated(m.pattern, path)
}

type runtimePathMatchers struct {
	byPattern map[string]CompiledPathMatcher
}

func compileRuntimePathMatchers(rules []policy.Rule) (*runtimePathMatchers, error) {
	patterns := make(map[string]struct{})
	add := func(values []string) {
		for _, value := range values {
			patterns[value] = struct{}{}
		}
	}
	for index := range rules {
		rule := &rules[index]
		add(rule.Paths)
		add(rule.BeforePaths)
		add(rule.WhenPaths)
		add(rule.ScopePaths)
		for checkIndex := range rule.Checks {
			check := &rule.Checks[checkIndex]
			add(check.Paths)
			add(check.BeforePaths)
			add(check.WhenPaths)
		}
	}
	ordered := make([]string, 0, len(patterns))
	for pattern := range patterns {
		ordered = append(ordered, pattern)
	}
	sort.Strings(ordered)
	compiled := make(map[string]CompiledPathMatcher, len(ordered))
	compiledBytes := 0
	for _, pattern := range ordered {
		matcher, err := CompilePathMatcher(pattern)
		if err != nil {
			return nil, fmt.Errorf("compile runtime path pattern %q: %w", pattern, err)
		}
		if matcher.glob != nil {
			logicalBytes := matcher.glob.LogicalBytes()
			if logicalBytes > maxRuntimeCompiledPathMatcherBytes || compiledBytes > maxRuntimeCompiledPathMatchers-logicalBytes {
				matcher.glob = nil
			} else {
				compiledBytes += logicalBytes
			}
		}
		compiled[pattern] = matcher
	}
	return &runtimePathMatchers{byPattern: compiled}, nil
}

func (m *runtimePathMatchers) matchAny(patterns []string, path string) (matchedPattern string, matched bool, err error) {
	if m == nil {
		return MatchAny(patterns, path)
	}
	for _, pattern := range patterns {
		matcher, ok := m.byPattern[pattern]
		if !ok {
			return "", false, fmt.Errorf("runtime path pattern %q is not part of the immutable plan", pattern)
		}
		if matcher.Match(path) {
			return pattern, true, nil
		}
	}
	return "", false, nil
}

func matchAnyPaths(matchers *runtimePathMatchers, patterns []string, path string) (string, bool, error) {
	if matchers == nil {
		return MatchAny(patterns, path)
	}
	return matchers.matchAny(patterns, path)
}

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
	// Patterns are configuration text, so surrounding whitespace is
	// normalized. Path bytes are evidence and must remain verbatim: leading
	// and trailing spaces are legal POSIX filename characters.
	p := strings.TrimSpace(pattern)
	q := path
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
