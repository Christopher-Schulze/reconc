//go:build !windows && !darwin

package pathidentity

import "path/filepath"

func resolveExistingOS(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func existingAliasesOS(string) []string {
	return nil
}

func aliasComparisonKey(path string) string {
	return path
}
