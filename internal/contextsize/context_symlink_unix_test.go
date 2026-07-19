//go:build !windows

package contextsize

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanRejectsSymlinkPathsOutsideRepository(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret.md"), "secret")
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(repo, "linked.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(repo, []string{"linked.md"}, 0); err == nil {
		t.Fatal("expected symlink repository escape to fail")
	}
}
