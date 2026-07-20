//go:build windows

package agentsession

import (
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/pathidentity"
)

// claudeProjectKeyMatchesFilesystemAliases accepts any component-wise mixture
// of an existing Windows path's long and 8.3 names. GetShortPathName returns
// only the all-short spelling, while real callers can inherit a short parent
// and append long children; matching per component covers that valid identity
// without allowing an unconfirmed project key.
func claudeProjectKeyMatchesFilesystemAliases(root, projectKey string) bool {
	resolved, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return false
	}
	return claudeProjectKeyMatchesResolvedAliases(resolved, projectKey, pathidentity.ExistingAliases)
}

func claudeProjectKeyMatchesResolvedAliases(
	resolved string,
	projectKey string,
	existingAliases func(string) ([]string, error),
) bool {
	volume := filepath.VolumeName(resolved)
	remainder := strings.Trim(strings.TrimPrefix(resolved, volume), string(filepath.Separator))
	components := []string{}
	if remainder != "" {
		components = strings.Split(remainder, string(filepath.Separator))
	}

	prefix := volume + string(filepath.Separator)
	positions := consumeClaudeProjectKeyTokens(projectKey, []int{0}, []string{claudeProjectKey(prefix)})
	current := prefix
	separatorKey := claudeProjectKey(string(filepath.Separator))
	for index, component := range components {
		if index > 0 {
			positions = consumeClaudeProjectKeyTokens(projectKey, positions, []string{separatorKey})
		}
		current = filepath.Join(current, component)
		aliases, aliasErr := existingAliases(current)
		if aliasErr != nil {
			return false
		}
		componentKeys := make([]string, 0, len(aliases)+1)
		componentKeys = append(componentKeys, claudeProjectKey(component))
		for _, alias := range aliases {
			componentKeys = append(componentKeys, claudeProjectKey(filepath.Base(alias)))
		}
		positions = consumeClaudeProjectKeyTokens(projectKey, positions, componentKeys)
		if len(positions) == 0 {
			return false
		}
	}
	for _, position := range positions {
		if position == len(projectKey) {
			return true
		}
	}
	return false
}

func consumeClaudeProjectKeyTokens(candidate string, positions []int, tokens []string) []int {
	next := []int{}
	seen := map[int]bool{}
	for _, position := range positions {
		for _, token := range tokens {
			end := position + len(token)
			if end > len(candidate) || !strings.EqualFold(candidate[position:end], token) || seen[end] {
				continue
			}
			seen[end] = true
			next = append(next, end)
		}
	}
	return next
}
