//go:build !windows

package agentsession

import (
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/privatefs"
)

func TestPrivateStateDirectoryIsRepairedOnlyWhenNeeded(t *testing.T) {
	repo := setupPolicyRepo(t)
	if _, err := InitializeSessionState(repo, "dir-fast-path"); err != nil {
		t.Fatal(err)
	}
	repairs := 0
	previous := repairPrivateStateDir
	t.Cleanup(func() { repairPrivateStateDir = previous })
	repairPrivateStateDir = func(path string) error {
		repairs++
		return previous(path)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Dir(sessionStatePath(root, "dir-fast-path"))
	if err := privatefs.ValidateDirectory(sessions); err != nil {
		t.Fatalf("session directory is not private after initialization: %v", err)
	}
	if err := ensurePrivateStateDir(sessions); err != nil {
		t.Fatal(err)
	}
	if repairs != 0 {
		t.Fatalf("an already private directory was walked for repair %d times", repairs)
	}
	if err := os.Chmod(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateStateDir(sessions); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sessions)
	if err != nil {
		t.Fatal(err)
	}
	if repairs == 0 || info.Mode().Perm() != 0o700 {
		t.Fatalf("an exposed directory must be repaired: repairs=%d mode=%v", repairs, info.Mode().Perm())
	}
}
