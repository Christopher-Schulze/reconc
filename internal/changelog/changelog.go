// Package changelog implements docs/changelog.md rotation (W45).
//
// When a project's changelog grows past a threshold, older sections
// move to docs/changelog/archive/YYYY-QN.md so that the auto-loaded
// file stays small (keeping agent session-start token budget low)
// while preserving full history on disk.
//
// Entry detection is deliberately simple: sections are delimited by
// `## ` at column 1. Anything before the first `## ` is the preamble
// and is never rotated. Newest-first convention is assumed (the
// default for most changelogs), so "rotate the oldest N entries" means
// "keep the first K entries, move the rest to archive".
package changelog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/filelock"
)

// Default lets rotate work with no config on a fresh repo. 200 lines
// is ~3K tokens and keeps the auto-loaded changelog under a
// comfortable session-start budget.
const (
	DefaultThresholdLines = 200
	DefaultChangelogPath  = "docs/changelog.md"
	DefaultArchiveDir     = "docs/changelog/archive"
)

// Options configures a Rotate call. Zero values mean "use defaults".
type Options struct {
	// ChangelogPath overrides the default docs/changelog.md.
	ChangelogPath string
	// ArchiveDir overrides the default docs/changelog/archive/.
	ArchiveDir string
	// ThresholdLines is the max line count the changelog may have
	// before rotation is triggered. If zero, DefaultThresholdLines.
	ThresholdLines int
	// Force ignores the threshold and rotates regardless.
	Force bool
	// Now is injectable for deterministic tests; defaults to time.Now.
	Now func() time.Time
}

// Result describes what Rotate did. Always non-nil even for no-op runs
// so callers can print a uniform summary.
type Result struct {
	ChangelogPath    string   `json:"changelog_path"`
	ArchivePath      string   `json:"archive_path"`
	Rotated          bool     `json:"rotated"`
	Reason           string   `json:"reason"`
	LinesBefore      int      `json:"lines_before"`
	LinesAfter       int      `json:"lines_after"`
	SectionsArchived int      `json:"sections_archived"`
	ArchivedIDs      []string `json:"archived_section_titles,omitempty"`
}

// Rotate applies the rotation policy to repoRoot's changelog. It is a
// no-op if the changelog is below the threshold and Force is false.
// Returns a Result describing what happened.
//
// The read-modify-write cycle runs under a cross-process lock, both file
// publications are atomic, and archive appends skip sections whose exact
// content is already archived, so a crash between the two writes (or a
// repeated --force run) never duplicates history.
func Rotate(repoRoot string, opts Options) (*Result, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ChangelogPath == "" {
		opts.ChangelogPath = DefaultChangelogPath
	}
	if opts.ArchiveDir == "" {
		opts.ArchiveDir = DefaultArchiveDir
	}
	threshold := opts.ThresholdLines
	if threshold <= 0 {
		threshold = DefaultThresholdLines
	}

	unlock, err := acquireRotateLock(repoRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unlock() }()

	clPath := filepath.Join(repoRoot, opts.ChangelogPath)
	data, err := os.ReadFile(clPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &Result{
				ChangelogPath: opts.ChangelogPath,
				Rotated:       false,
				Reason:        "changelog file does not exist; nothing to rotate",
			}, nil
		}
		return nil, fmt.Errorf("read changelog: %w", err)
	}

	lines := splitLines(string(data))
	result := &Result{
		ChangelogPath: opts.ChangelogPath,
		LinesBefore:   len(lines),
		LinesAfter:    len(lines),
	}

	if !opts.Force && len(lines) <= threshold {
		result.Reason = fmt.Sprintf("under threshold (%d <= %d lines)", len(lines), threshold)
		return result, nil
	}

	// Strip archive-pointer trailers from previous rotations so they
	// neither ride along into the archive nor stack up in the rewrite.
	lines = stripArchiveTrailer(lines)

	preamble, sections := splitSections(lines)
	if len(sections) <= 1 {
		result.Reason = "not enough sections to rotate (need at least 2 ## headings)"
		return result, nil
	}

	// Keep enough of the newest sections that the remaining file is
	// under threshold; archive the rest.
	keep := selectKeepCount(preamble, sections, threshold, opts.Force)
	if keep >= len(sections) {
		result.Reason = "no sections need archiving"
		return result, nil
	}

	toArchive := sections[keep:]
	remaining := sections[:keep]

	// Assemble archive file. Use current quarter as label.
	now := opts.Now()
	quarter := quarterLabel(now)
	archiveName := quarter + ".md"
	archivePath := filepath.Join(repoRoot, opts.ArchiveDir, archiveName)

	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir archive dir: %w", err)
	}

	var archiveBuf strings.Builder
	existingArchive, _ := os.ReadFile(archivePath)
	archived := archivedSectionSet(existingArchive)
	if len(existingArchive) > 0 {
		archiveBuf.Write(existingArchive)
		if !strings.HasSuffix(string(existingArchive), "\n") {
			archiveBuf.WriteString("\n")
		}
	} else {
		archiveBuf.WriteString("# Changelog Archive: " + quarter + "\n\n")
		archiveBuf.WriteString("_Rotated from " + opts.ChangelogPath + " on " + now.UTC().Format("2006-01-02") + "._\n\n")
	}

	for _, s := range toArchive {
		content := sectionContent(s)
		if archived[content] {
			continue
		}
		archiveBuf.WriteString(content)
		archiveBuf.WriteString("\n\n")
	}

	if _, err := atomicfile.WriteIfChanged(archivePath, []byte(archiveBuf.String()), 0o644); err != nil {
		return nil, fmt.Errorf("write archive: %w", err)
	}

	// Rewrite changelog with preamble + kept sections.
	var newBuf strings.Builder
	if len(preamble) > 0 {
		newBuf.WriteString(strings.Join(preamble, "\n"))
		newBuf.WriteString("\n")
	}
	for _, s := range remaining {
		newBuf.WriteString(sectionContent(s))
		newBuf.WriteString("\n\n")
	}
	// Trailer pointer to the archive.
	newBuf.WriteString("---\n\n")
	newBuf.WriteString("_Older entries rotated to `" + filepath.Join(opts.ArchiveDir, archiveName) + "`._\n")

	if _, err := atomicfile.WriteIfChanged(clPath, []byte(newBuf.String()), 0o644); err != nil {
		return nil, fmt.Errorf("rewrite changelog: %w", err)
	}

	result.Rotated = true
	result.ArchivePath = filepath.Join(opts.ArchiveDir, archiveName)
	result.LinesAfter = len(splitLines(newBuf.String()))
	result.SectionsArchived = len(toArchive)
	result.ArchivedIDs = make([]string, 0, len(toArchive))
	for _, s := range toArchive {
		result.ArchivedIDs = append(result.ArchivedIDs, s.Title)
	}
	if opts.Force && result.LinesBefore <= threshold {
		result.Reason = fmt.Sprintf("forced rotation (%d sections moved)", len(toArchive))
	} else {
		result.Reason = fmt.Sprintf("over threshold (%d > %d lines); moved %d sections to archive",
			result.LinesBefore, threshold, len(toArchive))
	}
	return result, nil
}

// ListArchives enumerates archive files for repoRoot.
func ListArchives(repoRoot string, opts Options) ([]ArchiveInfo, error) {
	if opts.ArchiveDir == "" {
		opts.ArchiveDir = DefaultArchiveDir
	}
	archiveDir := filepath.Join(repoRoot, opts.ArchiveDir)
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []ArchiveInfo{}, nil
		}
		return nil, err
	}
	out := make([]ArchiveInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		relPath := filepath.Join(opts.ArchiveDir, e.Name())
		out = append(out, ArchiveInfo{
			Path:      relPath,
			SizeBytes: info.Size(),
			ModTime:   info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

// ArchiveInfo describes one archive file.
type ArchiveInfo struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	ModTime   string `json:"mod_time"`
}

// -------- internals --------------------------------------------------

// acquireRotateLock serializes concurrent Rotate calls for one repository.
// The lock file lives under .reconc/locks/, the runtime state directory
// covered by the repository ignore policy and retention.
func acquireRotateLock(repoRoot string) (func() error, error) {
	lockPath := filepath.Join(repoRoot, ".reconc", "locks", "changelog.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open rotate lock: %w", err)
	}
	unlock, err := filelock.Lock(lock)
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("acquire rotate lock: %w", err)
	}
	return func() error {
		err := unlock()
		_ = lock.Close()
		return err
	}, nil
}

// stripArchiveTrailer removes the "---" + archive-pointer trailer blocks
// appended by previous rotations from the end of the changelog lines so
// they neither stack up nor get carried into the archive.
func stripArchiveTrailer(lines []string) []string {
	for {
		end := len(lines)
		for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		if end == 0 {
			return lines
		}
		last := strings.TrimSpace(lines[end-1])
		if !strings.HasPrefix(last, "_Older entries rotated to ") || !strings.HasSuffix(last, "._") {
			return lines
		}
		cut := end - 1
		for cut > 0 && strings.TrimSpace(lines[cut-1]) == "" {
			cut--
		}
		if cut == 0 || strings.TrimSpace(lines[cut-1]) != "---" {
			return lines
		}
		cut--
		for cut > 0 && strings.TrimSpace(lines[cut-1]) == "" {
			cut--
		}
		lines = lines[:cut]
	}
}

// sectionContent renders one section as newline-joined text without a
// trailing newline. Used both for writing and for duplicate detection.
func sectionContent(s section) string {
	return strings.TrimRight(strings.Join(s.Lines, "\n"), "\n")
}

// archivedSectionSet indexes the exact content of every section already
// present in an archive file, so appends after a crash or a repeated
// forced rotation skip sections that were already archived.
func archivedSectionSet(archive []byte) map[string]bool {
	set := map[string]bool{}
	if len(archive) == 0 {
		return set
	}
	_, sections := splitSections(splitLines(string(archive)))
	for _, s := range sections {
		set[sectionContent(s)] = true
	}
	return set
}

// section is one `## ` delimited block in the changelog.
type section struct {
	Title string   // the "## ..." line content (without the ## prefix)
	Lines []string // all lines of the section, including the ## header
}

// splitSections separates a line slice into (preamble, sections).
// Preamble is everything before the first `## ` heading.
func splitSections(lines []string) (preamble []string, sections []section) {
	// Find first ## line.
	firstHeading := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "## ") {
			firstHeading = i
			break
		}
	}
	if firstHeading < 0 {
		return lines, nil
	}
	preamble = append([]string{}, lines[:firstHeading]...)

	current := section{Title: strings.TrimSpace(strings.TrimPrefix(lines[firstHeading], "## "))}
	current.Lines = append(current.Lines, lines[firstHeading])

	for i := firstHeading + 1; i < len(lines); i++ {
		l := lines[i]
		if strings.HasPrefix(l, "## ") {
			sections = append(sections, current)
			current = section{Title: strings.TrimSpace(strings.TrimPrefix(l, "## "))}
			current.Lines = []string{l}
			continue
		}
		current.Lines = append(current.Lines, l)
	}
	sections = append(sections, current)
	return preamble, sections
}

// selectKeepCount figures out how many of the newest sections to keep.
// In force mode, keeps the top 25% (rounded up to >= 1) as a "recent
// window" regardless of lines. Otherwise keeps as many sections as
// fit under the threshold.
func selectKeepCount(preamble []string, sections []section, threshold int, force bool) int {
	if force {
		keep := len(sections) / 4
		if keep < 1 {
			keep = 1
		}
		return keep
	}
	// Start from the newest (index 0) and accumulate lines until we
	// would exceed the threshold.
	running := len(preamble)
	for i, s := range sections {
		running += len(s.Lines)
		if running > threshold {
			// Keep up to but NOT including i. Guarantee at least 1.
			if i < 1 {
				return 1
			}
			return i
		}
	}
	return len(sections)
}

// splitLines is a helper that preserves empty-string entries for each
// newline (unlike strings.Fields).
func splitLines(s string) []string {
	if s == "" {
		return []string{}
	}
	// Trim trailing newline once so we don't end with a phantom entry.
	s = strings.TrimRight(s, "\n")
	return strings.Split(s, "\n")
}

// quarterLabel returns a YYYY-QN label for rotation archives.
func quarterLabel(t time.Time) string {
	q := (int(t.Month())-1)/3 + 1
	return fmt.Sprintf("%d-Q%d", t.Year(), q)
}
