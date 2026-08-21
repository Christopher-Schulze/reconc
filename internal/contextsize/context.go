// Package contextsize implements the canonical session-entry token budget
// guard (W43). Agent entrypoints and the current TASK can grow silently and
// consume the session budget before implementation begins.
//
// `reconc context size [repo]` reports bytes and approximate tokens per file,
// plus a total and budget-status flag. Exit 1 when the total exceeds the
// budget so CI gates can block unnecessary session-loaded prose.
package contextsize

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/pathidentity"
)

// Default budget in approximate tokens. ~20 KTokens = ~80 KB assuming
// the common ~4 bytes/token heuristic. Generous enough for real agent rules,
// onboarding, TASK overview, and one active TASK detail.
const DefaultTokenBudget = 20000

const (
	MaxContextFiles     = 4096
	MaxContextPathBytes = 1024
	maxInt64Value       = int64(1<<63 - 1)
	maxContextFileBytes = maxInt64Value - 1
)

// BytesPerTokenEstimate is a conservative heuristic for approximate
// token counting without pulling in a tokenizer library. Real tokens
// vary 2-8 bytes depending on content; 4 is a sensible default that
// overestimates for code-heavy content (safer for a budget guard).
const BytesPerTokenEstimate = 4

// DefaultFiles lists the canonical Reconc session entrypoints. Optional
// project docs belong in --files; counting every historical or reference doc
// by default makes the budget report itself drift from the actual startup path.
var DefaultFiles = []string{
	"AGENTS.md",
	"CLAUDE.md",
	"start.md",
	"docs/tasks.md",
}

// FileReport is the per-file result of a scan.
type FileReport struct {
	Path         string `json:"path"`
	SizeBytes    int64  `json:"size_bytes"`
	ApproxTokens int64  `json:"approx_tokens"`
	Exists       bool   `json:"exists"`
}

// ScanReport aggregates all file reports plus the total + budget
// decision.
type ScanReport struct {
	FormatVersion     string       `json:"format_version"`
	RepoRoot          string       `json:"repo_root"`
	TokenBudget       int64        `json:"token_budget"`
	TotalApproxTokens int64        `json:"total_approx_tokens"`
	TotalBytes        int64        `json:"total_bytes"`
	OverBudget        bool         `json:"over_budget"`
	Largest           string       `json:"largest_file,omitempty"`
	Files             []FileReport `json:"files"`
}

// Scan inspects the given files under repoRoot and returns a report.
// Missing files are listed with Exists=false and zero size; they don't
// contribute to the total. Paths and symlink targets outside repoRoot fail
// closed instead of exposing external file metadata through the report.
func Scan(repoRoot string, files []string, tokenBudget int) (ScanReport, error) {
	if len(files) == 0 {
		files = append([]string(nil), DefaultFiles...)
	}
	if tokenBudget <= 0 {
		tokenBudget = DefaultTokenBudget
	}
	budget := int64(tokenBudget)
	if len(files) > MaxContextFiles {
		return ScanReport{}, fmt.Errorf("context file input contains %d paths; maximum is %d", len(files), MaxContextFiles)
	}
	root, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return ScanReport{}, fmt.Errorf("resolve repository root filesystem identity: %w", err)
	}
	out := ScanReport{
		FormatVersion: "1",
		RepoRoot:      root,
		TokenBudget:   budget,
		Files:         make([]FileReport, 0, len(files)),
	}
	largestTokens := int64(0)
	seen := make(map[string]struct{}, len(files))
	for _, raw := range files {
		rel, err := normalizeContextPath(raw)
		if err != nil {
			return ScanReport{}, err
		}
		if _, exists := seen[rel]; exists {
			continue
		}
		seen[rel] = struct{}{}
		fr := FileReport{Path: rel}
		info, exists, err := contextFileInfo(root, rel)
		if err != nil {
			return ScanReport{}, err
		}
		if exists && !info.IsDir() {
			fr.Exists = true
			fr.SizeBytes = info.Size()
			fr.ApproxTokens = approxTokens(info.Size())
			out.TotalBytes = saturatingAdd(out.TotalBytes, fr.SizeBytes)
			out.TotalApproxTokens = saturatingAdd(out.TotalApproxTokens, fr.ApproxTokens)
			if fr.ApproxTokens > largestTokens {
				largestTokens = fr.ApproxTokens
				out.Largest = rel
			}
		}
		out.Files = append(out.Files, fr)
	}
	// Deterministic order by descending token count, then alpha for
	// readable default output. Original insertion order is lost but
	// that's fine -- callers can ask for it via --order=path if ever
	// needed.
	sort.SliceStable(out.Files, func(i, j int) bool {
		if out.Files[i].ApproxTokens != out.Files[j].ApproxTokens {
			return out.Files[i].ApproxTokens > out.Files[j].ApproxTokens
		}
		return out.Files[i].Path < out.Files[j].Path
	})
	out.OverBudget = out.TotalApproxTokens > budget
	return out, nil
}

func normalizeContextPath(raw string) (string, error) {
	if !utf8.ValidString(raw) || len(raw) > MaxContextPathBytes {
		return "", fmt.Errorf("context path must be valid UTF-8 and at most %d bytes", MaxContextPathBytes)
	}
	cleaned := filepath.Clean(filepath.FromSlash(strings.TrimSpace(raw)))
	if cleaned == "." || filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || len(filepath.ToSlash(cleaned)) > MaxContextPathBytes {
		return "", fmt.Errorf("context path %q must stay inside the repository", raw)
	}
	return filepath.ToSlash(cleaned), nil
}

func contextFileInfo(root, relative string) (os.FileInfo, bool, error) {
	full := filepath.Join(root, filepath.FromSlash(relative))
	lstat, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("inspect context file %s: %w", relative, err)
	}
	if lstat.IsDir() {
		return lstat, false, nil
	}
	if lstat.Mode()&os.ModeSymlink != 0 {
		resolved, resolveErr := pathidentity.ResolveExisting(full)
		if resolveErr != nil {
			return nil, false, fmt.Errorf("resolve context file identity %s: %w", relative, resolveErr)
		}
		rel, relErr := filepath.Rel(root, resolved)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, false, fmt.Errorf("context file %s resolves outside the repository", relative)
		}
		return nil, false, fmt.Errorf("context file %s must be a non-symlink regular file", relative)
	}
	var opened os.FileInfo
	if err := boundedio.WithRegularFileSnapshot(full, maxContextFileBytes, func(_ *os.File, info os.FileInfo) error {
		opened = info
		return nil
	}); err != nil {
		return nil, false, fmt.Errorf("inspect stable context file %s: %w", relative, err)
	}
	return opened, true, nil
}

// approxTokens converts a byte count to an estimated token count using
// a fixed divisor. Overestimates for code-heavy content on purpose;
// the budget guard should err on the side of caution.
func approxTokens(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	return (bytes-1)/BytesPerTokenEstimate + 1
}

func saturatingAdd(left, right int64) int64 {
	if right < 0 || left < 0 {
		return maxInt64Value
	}
	if left > maxInt64Value-right {
		return maxInt64Value
	}
	return left + right
}
