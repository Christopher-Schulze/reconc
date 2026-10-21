package ingest

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
)

const (
	maxPolicyGlobPatterns         = 256
	maxPolicyGlobPatternBytes     = 1024
	maxPolicyGlobMatches          = maxPolicySources + 1
	maxPolicyGlobDirectories      = maxPolicySources + 1
	maxPolicyGlobDirectoryEntries = maxPolicySources + 1
)

// readPolicyDirectory lists one directory for a policy glob; tests count it.
var readPolicyDirectory = boundedio.ReadDir

// ExpandPolicyIncludePattern returns the bounded regular-file matches for one
// already validated repository-relative policy include pattern.
func ExpandPolicyIncludePattern(root, pattern string) ([]string, error) {
	return boundedPolicyGlob(root, pattern)
}

// ExpandPolicyIncludePatternWithContext expands one bounded include while
// honoring cancellation between directory and entry operations.
func ExpandPolicyIncludePatternWithContext(ctx context.Context, root, pattern string) ([]string, error) {
	return boundedPolicyGlobWithContext(ctx, root, pattern)
}

// ExpandPolicyIncludePatternsWithContext expands bounded includes in order,
// one match list per pattern. Each directory is read and each candidate
// inspected once for the whole set, so patterns that share a directory share
// its listing.
func ExpandPolicyIncludePatternsWithContext(ctx context.Context, root string, patterns []string) ([][]string, error) {
	return boundedPolicyGlobsWithContext(ctx, root, patterns)
}

// boundedPolicyGlob expands one repository-relative glob without ever
// materializing an unbounded filepath.Glob result. The grammar is segment
// based (`*`, `?`, and character classes); `**` is not recursive magic.
func boundedPolicyGlob(root, pattern string) ([]string, error) {
	return boundedPolicyGlobWithContext(context.Background(), root, pattern)
}

func boundedPolicyGlobWithContext(ctx context.Context, root, pattern string) ([]string, error) {
	matches, err := boundedPolicyGlobsWithContext(ctx, root, []string{pattern})
	if err != nil {
		return nil, err
	}
	return matches[0], nil
}

func boundedPolicyGlobsWithContext(ctx context.Context, root string, patterns []string) ([][]string, error) {
	if ctx == nil {
		return nil, context.Canceled
	}
	observed := &globObservations{listings: map[string][]os.DirEntry{}, candidates: map[string]globCandidate{}}
	all := make([][]string, 0, len(patterns))
	for _, pattern := range patterns {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(pattern) == 0 || len(pattern) > maxPolicyGlobPatternBytes {
			return nil, fmt.Errorf("policy include pattern must be 1-%d bytes", maxPolicyGlobPatternBytes)
		}
		segments := strings.Split(pattern, "/")
		if len(segments) == 0 {
			return nil, fmt.Errorf("policy include pattern %q is empty", pattern)
		}
		for _, segment := range segments {
			if segment == "" || segment == "." || segment == ".." {
				return nil, fmt.Errorf("policy include pattern %q contains an unsupported path segment", pattern)
			}
		}
		state := boundedGlobState{ctx: ctx, pattern: pattern, observed: observed}
		if err := state.walk(root, segments, 0); err != nil {
			return nil, err
		}
		all = append(all, state.matches)
	}
	return all, nil
}

type boundedGlobState struct {
	ctx         context.Context
	pattern     string
	observed    *globObservations
	matches     []string
	directories int
}

// globObservations holds the directory listings and candidate inspections of
// one expansion, so patterns that share a directory read it once.
type globObservations struct {
	listings   map[string][]os.DirEntry
	candidates map[string]globCandidate
}

// globCandidate is one held os.Stat result: an existing entry or a missing one.
type globCandidate struct {
	info os.FileInfo
	err  error
}

func (o *globObservations) list(directory string) ([]os.DirEntry, error) {
	if entries, ok := o.listings[directory]; ok {
		return entries, nil
	}
	entries, err := readPolicyDirectory(directory, maxPolicyGlobDirectoryEntries)
	if err == nil {
		o.listings[directory] = entries
	}
	return entries, err
}

func (o *globObservations) stat(candidate string) (os.FileInfo, error) {
	if observed, ok := o.candidates[candidate]; ok {
		return observed.info, observed.err
	}
	info, err := os.Stat(candidate)
	if err == nil || os.IsNotExist(err) {
		o.candidates[candidate] = globCandidate{info: info, err: err}
	}
	return info, err
}

func (s *boundedGlobState) walk(directory string, segments []string, index int) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.directories++
	if s.directories > maxPolicyGlobDirectories {
		return fmt.Errorf("policy include pattern %q exceeds %d directories", s.pattern, maxPolicyGlobDirectories)
	}
	segment := segments[index]
	if hasPolicyGlobMeta(segment) {
		entries, err := s.observed.list(directory)
		if err != nil {
			return fmt.Errorf("enumerate policy include pattern %q: %w", s.pattern, err)
		}
		for _, entry := range entries {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			matched, err := path.Match(segment, entry.Name())
			if err != nil {
				return fmt.Errorf("compile policy include pattern %q: %w", s.pattern, err)
			}
			if !matched {
				continue
			}
			if err := s.visit(filepath.Join(directory, entry.Name()), segments, index); err != nil {
				return err
			}
		}
		return nil
	}
	return s.visit(filepath.Join(directory, filepath.FromSlash(segment)), segments, index)
}

func (s *boundedGlobState) visit(candidate string, segments []string, index int) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	info, err := s.observed.stat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect policy include pattern %q: %w", s.pattern, err)
	}
	last := index == len(segments)-1
	if last {
		if !info.Mode().IsRegular() {
			return nil
		}
		if len(s.matches) >= maxPolicyGlobMatches {
			return fmt.Errorf("policy include pattern %q exceeds %d matched entries", s.pattern, maxPolicyGlobMatches-1)
		}
		s.matches = append(s.matches, candidate)
		return nil
	}
	if !info.IsDir() {
		return nil
	}
	return s.walk(candidate, segments, index+1)
}

func hasPolicyGlobMeta(segment string) bool {
	return strings.ContainsAny(segment, `*?[\`)
}

func validatePolicyGlobPatterns(patterns []string) error {
	if len(patterns) > maxPolicyGlobPatterns {
		return fmt.Errorf("policy include list exceeds %d patterns", maxPolicyGlobPatterns)
	}
	for _, pattern := range patterns {
		if len(pattern) == 0 || len(pattern) > maxPolicyGlobPatternBytes {
			return fmt.Errorf("policy include pattern must be 1-%d bytes", maxPolicyGlobPatternBytes)
		}
		if _, err := boundedPolicyGlobPatternSegments(pattern); err != nil {
			return err
		}
	}
	return nil
}

func boundedPolicyGlobPatternSegments(pattern string) ([]string, error) {
	segments := strings.Split(pattern, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return nil, fmt.Errorf("policy include pattern %q contains an unsupported path segment", pattern)
		}
		if _, err := path.Match(segment, ""); err != nil {
			return nil, fmt.Errorf("policy include pattern %q is malformed: %w", pattern, err)
		}
	}
	return segments, nil
}
