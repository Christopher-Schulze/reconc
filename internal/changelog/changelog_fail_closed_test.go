package changelog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRotateDoesNotRewriteChangelogWhenExistingArchiveIsUnreadable(t *testing.T) {
	repo := mkRepo(t)
	changelogPath := filepath.Join(repo, "docs", "changelog.md")
	writeFile(t, changelogPath, sampleChangelog)
	archivePath := filepath.Join(repo, "docs", "changelog", "archive", "2026-Q2.md")
	if err := os.MkdirAll(archivePath, 0o755); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(changelogPath)
	if err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC) }
	if _, err := Rotate(repo, Options{ThresholdLines: 10, Now: now}); err == nil {
		t.Fatal("unreadable existing archive must fail closed")
	}
	after, err := os.ReadFile(changelogPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("changelog changed after archive read failure")
	}
}
