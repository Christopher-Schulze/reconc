// Package contextsize implements the auto-loaded-file token budget
// guard (W43). At session start, most agents read AGENTS.md + a handful
// of docs/ files unconditionally; those files grow silently and blow
// the session-start token budget.
//
// `reconc context size [repo]` scans the usual suspects and reports
// bytes + approximate tokens per file, plus a total and a
// budget-status flag. Exit 1 when the total exceeds the budget so CI
// gates can block PRs that add too much session-loaded prose.
package contextsize

import (
	"os"
	"path/filepath"
	"sort"
)

// Default budget in approximate tokens. ~20 KTokens = ~80 KB assuming
// the common ~4 bytes/token heuristic. Generous enough for a real
// project's AGENTS.md + changelog tail + one or two support docs.
const DefaultTokenBudget = 20000

// BytesPerTokenEstimate is a conservative heuristic for approximate
// token counting without pulling in a tokenizer library. Real tokens
// vary 2-8 bytes depending on content; 4 is a sensible default that
// overestimates for code-heavy content (safer for a budget guard).
const BytesPerTokenEstimate = 4

// DefaultFiles lists the files typically auto-loaded at session start
// by Claude Code / Codex agents. Order is presentation-only.
var DefaultFiles = []string{
	"AGENTS.md",
	"CLAUDE.md",
	"docs/agent-guide.md",
	"docs/changelog.md",
	"docs/context.md",
	"docs/map.md",
	"docs/spec.md",
	"docs/todo.md",
	"README.md",
}

// FileReport is the per-file result of a scan.
type FileReport struct {
	Path         string `json:"path"`
	SizeBytes    int64  `json:"size_bytes"`
	ApproxTokens int    `json:"approx_tokens"`
	Exists       bool   `json:"exists"`
}

// ScanReport aggregates all file reports plus the total + budget
// decision.
type ScanReport struct {
	RepoRoot          string       `json:"repo_root"`
	TokenBudget       int          `json:"token_budget"`
	TotalApproxTokens int          `json:"total_approx_tokens"`
	TotalBytes        int64        `json:"total_bytes"`
	OverBudget        bool         `json:"over_budget"`
	Largest           string       `json:"largest_file,omitempty"`
	Files             []FileReport `json:"files"`
}

// Scan inspects the given files under repoRoot and returns a report.
// Missing files are listed with Exists=false and zero size; they don't
// contribute to the total (so a user can keep all nine defaults in the
// list without penalising repos that don't use all of them).
func Scan(repoRoot string, files []string, tokenBudget int) ScanReport {
	if len(files) == 0 {
		files = DefaultFiles
	}
	if tokenBudget <= 0 {
		tokenBudget = DefaultTokenBudget
	}
	out := ScanReport{
		RepoRoot:    repoRoot,
		TokenBudget: tokenBudget,
		Files:       make([]FileReport, 0, len(files)),
	}
	largestTokens := 0
	for _, rel := range files {
		full := filepath.Join(repoRoot, rel)
		fr := FileReport{Path: rel}
		info, err := os.Stat(full)
		if err == nil && !info.IsDir() {
			fr.Exists = true
			fr.SizeBytes = info.Size()
			fr.ApproxTokens = approxTokens(info.Size())
			out.TotalBytes += fr.SizeBytes
			out.TotalApproxTokens += fr.ApproxTokens
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
	out.OverBudget = out.TotalApproxTokens > tokenBudget
	return out
}

// approxTokens converts a byte count to an estimated token count using
// a fixed divisor. Overestimates for code-heavy content on purpose;
// the budget guard should err on the side of caution.
func approxTokens(bytes int64) int {
	if bytes <= 0 {
		return 0
	}
	return int(bytes) / BytesPerTokenEstimate
}
