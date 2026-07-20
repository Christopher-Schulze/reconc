//go:build !windows

package contextsize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanResolvesRepositoryAliasAndRejectsFileEscape(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "policy")
	alias := filepath.Join(t.TempDir(), "repo-alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	report, err := Scan(alias, []string{"AGENTS.md"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalBytes != int64(len("policy")) {
		t.Fatalf("aliased repository scan bytes=%d, want %d", report.TotalBytes, len("policy"))
	}

	outside := filepath.Join(t.TempDir(), "outside.md")
	mustWrite(t, outside, "external")
	if err := os.Symlink(outside, filepath.Join(repo, "linked.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(repo, []string{"linked.md"}, 100); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("external file identity was not rejected: %v", err)
	}
}
