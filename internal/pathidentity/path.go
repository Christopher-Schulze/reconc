// Package pathidentity resolves filesystem paths to their operating-system
// identity. It follows Unix symlinks and Windows reparse points, including
// directory junctions, and preserves not-yet-created suffixes safely.
package pathidentity

import (
	"fmt"
	"os"
	"path/filepath"
)

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
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", path, err)
	}
	cursor := filepath.Clean(absolute)
	missing := []string{}
	for {
		if _, statErr := os.Lstat(cursor); statErr == nil {
			resolved, resolveErr := ResolveExisting(cursor)
			if resolveErr != nil {
				return "", resolveErr
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(statErr) {
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
