//go:build !windows

package assurance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeExistingPathRejectsProspectiveSymlinkEscape(t *testing.T) {
	repo := t.TempDir()
	root, err := canonicalRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, "escape")); err != nil {
		t.Fatal(err)
	}
	_, _, err = safeExistingPath(root, "escape/not-created.yml")
	if err == nil || !strings.Contains(err.Error(), "outside repository") {
		t.Fatalf("prospective symlink escape was not rejected: %v", err)
	}
}
