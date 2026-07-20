//go:build windows

package agentsession

import (
	"path/filepath"
	"testing"
)

func TestClaudeProjectKeyMatchesMixedWindowsAlias(t *testing.T) {
	root := `C:\Users\runneradmin\AppData\Local\Temp\repo`
	aliases := func(path string) ([]string, error) {
		if path == `C:\Users\runneradmin` {
			return []string{path, `C:\Users\RUNNER~1`}, nil
		}
		return []string{path}, nil
	}
	mixed := `C:\Users\RUNNER~1\AppData\Local\Temp\repo`
	if !claudeProjectKeyMatchesResolvedAliases(root, claudeProjectKey(mixed), aliases) {
		t.Fatalf("mixed Windows alias was rejected: long=%q mixed=%q", root, mixed)
	}

	invalid := []string{
		`C:\UNCONF~1\runneradmin\AppData\Local\Temp\repo`,
		mixed + `-other`,
	}
	for _, path := range invalid {
		if claudeProjectKeyMatchesResolvedAliases(root, claudeProjectKey(path), aliases) {
			t.Fatalf("unconfirmed Windows project identity was accepted: %q", path)
		}
	}
}

func TestClaudeProjectKeyMatchesMixedWindowsAliasFailsClosed(t *testing.T) {
	root := `C:\Users\runneradmin\repo`
	aliasError := func(string) ([]string, error) {
		return nil, filepath.ErrBadPattern
	}
	if claudeProjectKeyMatchesResolvedAliases(root, claudeProjectKey(root), aliasError) {
		t.Fatal("alias lookup failure was accepted")
	}
}
