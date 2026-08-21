package ingest

import (
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

// boundedPolicyGlob expands one repository-relative glob without ever
// materializing an unbounded filepath.Glob result. The grammar is segment
// based (`*`, `?`, and character classes); `**` is not recursive magic.
func boundedPolicyGlob(root, pattern string) ([]string, error) {
	if len(pattern) == 0 || len(pattern) > maxPolicyGlobPatternBytes {
		return nil, fmt.Errorf("policy include pattern must be 1-%d bytes", maxPolicyGlobPatternBytes)
	}
	normalized := filepath.ToSlash(pattern)
	segments := strings.Split(normalized, "/")
	if len(segments) == 0 {
		return nil, fmt.Errorf("policy include pattern %q is empty", pattern)
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return nil, fmt.Errorf("policy include pattern %q contains an unsupported path segment", pattern)
		}
	}
	state := boundedGlobState{pattern: pattern}
	if err := state.walk(root, segments, 0); err != nil {
		return nil, err
	}
	return state.matches, nil
}

type boundedGlobState struct {
	pattern     string
	matches     []string
	directories int
}

func (s *boundedGlobState) walk(directory string, segments []string, index int) error {
	s.directories++
	if s.directories > maxPolicyGlobDirectories {
		return fmt.Errorf("policy include pattern %q exceeds %d directories", s.pattern, maxPolicyGlobDirectories)
	}
	segment := segments[index]
	if hasPolicyGlobMeta(segment) {
		entries, err := boundedio.ReadDir(directory, maxPolicyGlobDirectoryEntries)
		if err != nil {
			return fmt.Errorf("enumerate policy include pattern %q: %w", s.pattern, err)
		}
		for _, entry := range entries {
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
	info, err := os.Stat(candidate)
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
	return strings.ContainsAny(segment, "*?[")
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
	segments := strings.Split(filepath.ToSlash(pattern), "/")
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
