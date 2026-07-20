//go:build !windows

package agentsession

func claudeProjectKeyMatchesFilesystemAliases(string, string) bool {
	return false
}
