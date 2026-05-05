package changelog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleChangelog = `# Changelog

Front-matter describing the changelog.

## 2026-04-12 Feature X
- one
- two
- three

## 2026-04-11 Feature Y
- one

## 2026-04-10 Bug fix
- fixed a thing

## 2026-04-09 Docs pass
- tightened prose

## 2026-04-08 Setup
- init
`

func mkRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestRotateNoFileIsNoOp(t *testing.T) {
	repo := t.TempDir()
	r, err := Rotate(repo, Options{})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if r.Rotated {
		t.Errorf("expected no rotation for missing file, got rotated")
	}
	if !strings.Contains(r.Reason, "does not exist") {
		t.Errorf("expected 'does not exist' in reason, got: %s", r.Reason)
	}
}

func TestRotateBelowThresholdIsNoOp(t *testing.T) {
	repo := mkRepo(t)
	writeFile(t, filepath.Join(repo, "docs", "changelog.md"), sampleChangelog)
	r, err := Rotate(repo, Options{ThresholdLines: 500})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if r.Rotated {
		t.Errorf("expected no rotation (under threshold), got rotated")
	}
}

func TestRotateOverThresholdArchivesOldSections(t *testing.T) {
	repo := mkRepo(t)
	writeFile(t, filepath.Join(repo, "docs", "changelog.md"), sampleChangelog)
	now := func() time.Time { return time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC) }
	// Threshold = 10 lines forces archive.
	r, err := Rotate(repo, Options{ThresholdLines: 10, Now: now})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !r.Rotated {
		t.Fatalf("expected rotation, got no-op (%s)", r.Reason)
	}
	if r.SectionsArchived < 1 {
		t.Errorf("expected at least one section archived, got %d", r.SectionsArchived)
	}
	if r.LinesAfter >= r.LinesBefore {
		t.Errorf("expected LinesAfter < LinesBefore; got %d -> %d", r.LinesBefore, r.LinesAfter)
	}

	// Changelog file should still contain the newest section and the archive pointer.
	newCl, _ := os.ReadFile(filepath.Join(repo, "docs", "changelog.md"))
	if !strings.Contains(string(newCl), "2026-04-12") {
		t.Errorf("expected newest section kept, got:\n%s", string(newCl))
	}
	if !strings.Contains(string(newCl), "Older entries rotated to") {
		t.Errorf("expected archive pointer in rewritten changelog")
	}

	// Archive file should exist at expected path.
	archive, err := os.ReadFile(filepath.Join(repo, "docs", "changelog", "archive", "2026-Q2.md"))
	if err != nil {
		t.Fatalf("archive file should exist: %v", err)
	}
	if !strings.Contains(string(archive), "# Changelog Archive: 2026-Q2") {
		t.Errorf("archive header missing, got:\n%s", string(archive))
	}
	if !strings.Contains(string(archive), "2026-04-08 Setup") {
		t.Errorf("oldest section should be in archive")
	}
}

func TestRotateForceAppliesEvenWhenUnderThreshold(t *testing.T) {
	repo := mkRepo(t)
	writeFile(t, filepath.Join(repo, "docs", "changelog.md"), sampleChangelog)
	now := func() time.Time { return time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC) }
	r, err := Rotate(repo, Options{ThresholdLines: 9999, Force: true, Now: now})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !r.Rotated {
		t.Errorf("expected force rotation, got no-op: %s", r.Reason)
	}
	// Q1 for Feb 15.
	if !strings.Contains(r.ArchivePath, "2026-Q1") {
		t.Errorf("expected archive path to mention 2026-Q1, got %s", r.ArchivePath)
	}
}

func TestRotateAppendsToExistingArchive(t *testing.T) {
	repo := mkRepo(t)
	writeFile(t, filepath.Join(repo, "docs", "changelog.md"), sampleChangelog)
	// Pre-create an archive file.
	archiveDir := filepath.Join(repo, "docs", "changelog", "archive")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pre := "# Changelog Archive: 2026-Q2\n\n_preexisting_\n\n## 2026-03-01 Old\n- stuff\n"
	writeFile(t, filepath.Join(archiveDir, "2026-Q2.md"), pre)

	now := func() time.Time { return time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC) }
	_, err := Rotate(repo, Options{ThresholdLines: 10, Now: now})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(archiveDir, "2026-Q2.md"))
	got := string(data)
	if !strings.Contains(got, "_preexisting_") {
		t.Errorf("pre-existing archive content should be preserved")
	}
	if !strings.Contains(got, "2026-04-08 Setup") {
		t.Errorf("new rotated content should be appended")
	}
}

func TestListArchivesEmpty(t *testing.T) {
	repo := t.TempDir()
	out, err := ListArchives(repo, Options{})
	if err != nil {
		t.Fatalf("ListArchives: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected 0 archives, got %v", out)
	}
}

func TestListArchivesAfterRotation(t *testing.T) {
	repo := mkRepo(t)
	writeFile(t, filepath.Join(repo, "docs", "changelog.md"), sampleChangelog)
	now := func() time.Time { return time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC) }
	if _, err := Rotate(repo, Options{ThresholdLines: 10, Now: now}); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	archives, err := ListArchives(repo, Options{})
	if err != nil {
		t.Fatalf("ListArchives: %v", err)
	}
	if len(archives) != 1 {
		t.Fatalf("expected 1 archive, got %v", archives)
	}
	if !strings.HasSuffix(archives[0].Path, "2026-Q2.md") {
		t.Errorf("unexpected archive path: %s", archives[0].Path)
	}
	if archives[0].SizeBytes == 0 {
		t.Errorf("expected nonzero size")
	}
}

func TestQuarterLabel(t *testing.T) {
	for _, tc := range []struct {
		month int
		want  string
	}{
		{1, "2026-Q1"}, {3, "2026-Q1"},
		{4, "2026-Q2"}, {6, "2026-Q2"},
		{7, "2026-Q3"}, {9, "2026-Q3"},
		{10, "2026-Q4"}, {12, "2026-Q4"},
	} {
		got := quarterLabel(time.Date(2026, time.Month(tc.month), 1, 0, 0, 0, 0, time.UTC))
		if got != tc.want {
			t.Errorf("month %d: got %q, want %q", tc.month, got, tc.want)
		}
	}
}

func TestSplitSectionsNoHeadings(t *testing.T) {
	preamble, sections := splitSections([]string{"no", "headings", "here"})
	if len(sections) != 0 {
		t.Errorf("expected 0 sections, got %d", len(sections))
	}
	if len(preamble) != 3 {
		t.Errorf("expected all lines in preamble, got %d", len(preamble))
	}
}

func TestSplitSectionsPreamblePreserved(t *testing.T) {
	preamble, sections := splitSections([]string{"# Title", "", "Intro.", "## A", "- a", "## B", "- b"})
	if len(preamble) != 3 {
		t.Errorf("expected 3 preamble lines, got %v", preamble)
	}
	if len(sections) != 2 {
		t.Errorf("expected 2 sections, got %d", len(sections))
	}
	if sections[0].Title != "A" || sections[1].Title != "B" {
		t.Errorf("unexpected titles: %+v", sections)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
