package contextsize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanEmptyRepo(t *testing.T) {
	repo := t.TempDir()
	r := Scan(repo, nil, 0)
	if r.TotalBytes != 0 || r.TotalApproxTokens != 0 {
		t.Errorf("expected empty totals, got %+v", r)
	}
	if r.OverBudget {
		t.Error("empty repo should never be over budget")
	}
	if r.TokenBudget != DefaultTokenBudget {
		t.Errorf("expected default budget %d, got %d", DefaultTokenBudget, r.TokenBudget)
	}
	// All default files should be listed, all with Exists=false.
	if len(r.Files) != len(DefaultFiles) {
		t.Errorf("expected %d files in report, got %d", len(DefaultFiles), len(r.Files))
	}
	for _, f := range r.Files {
		if f.Exists {
			t.Errorf("no files should exist in empty repo; got %+v", f)
		}
	}
}

func TestScanReportsSizes(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), strings.Repeat("x", 1000))
	mustWrite(t, filepath.Join(repo, "docs", "changelog.md"), strings.Repeat("y", 4000))

	r := Scan(repo, nil, 0)
	if r.TotalBytes != 5000 {
		t.Errorf("expected 5000 total bytes, got %d", r.TotalBytes)
	}
	expectedTokens := (1000 + 4000) / BytesPerTokenEstimate
	if r.TotalApproxTokens != expectedTokens {
		t.Errorf("expected %d approx tokens, got %d", expectedTokens, r.TotalApproxTokens)
	}
	if r.Largest != "docs/changelog.md" {
		t.Errorf("expected largest=docs/changelog.md, got %q", r.Largest)
	}
}

func TestScanOverBudgetTrips(t *testing.T) {
	repo := t.TempDir()
	// 40000 bytes = ~10000 tokens. Budget 100 -> over.
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), strings.Repeat("x", 40000))
	r := Scan(repo, nil, 100)
	if !r.OverBudget {
		t.Error("expected OverBudget=true")
	}
}

func TestScanUnderBudget(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "small")
	r := Scan(repo, nil, 10000)
	if r.OverBudget {
		t.Errorf("expected under budget, got %+v", r)
	}
}

func TestScanCustomFileList(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "custom.md"), strings.Repeat("x", 400))
	r := Scan(repo, []string{"custom.md"}, 0)
	if len(r.Files) != 1 {
		t.Errorf("expected 1 file in report, got %d", len(r.Files))
	}
	if !r.Files[0].Exists || r.Files[0].Path != "custom.md" {
		t.Errorf("unexpected custom report: %+v", r.Files[0])
	}
}

func TestScanSortsByTokensDesc(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), strings.Repeat("x", 100))
	mustWrite(t, filepath.Join(repo, "README.md"), strings.Repeat("x", 5000))
	mustWrite(t, filepath.Join(repo, "docs", "context.md"), strings.Repeat("x", 2000))

	r := Scan(repo, nil, 0)
	// Existing files should be first (descending tokens), absents last.
	// Top two should be README.md, then docs/context.md, then AGENTS.md.
	existing := []string{}
	for _, f := range r.Files {
		if f.Exists {
			existing = append(existing, f.Path)
		}
	}
	wantOrder := []string{"README.md", "docs/context.md", "AGENTS.md"}
	if len(existing) != 3 {
		t.Fatalf("expected 3 existing files, got %v", existing)
	}
	for i, want := range wantOrder {
		if existing[i] != want {
			t.Errorf("order[%d]: got %q, want %q (full existing: %v)", i, existing[i], want, existing)
		}
	}
}

func TestScanMissingFilesNotCounted(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "small")
	r := Scan(repo, nil, 0)
	// Missing files should not add to total.
	if r.TotalApproxTokens != approxTokens(5) {
		t.Errorf("missing files must not contribute to total; got %d", r.TotalApproxTokens)
	}
}

func TestApproxTokensBoundary(t *testing.T) {
	if approxTokens(0) != 0 {
		t.Error("0 bytes should be 0 tokens")
	}
	if approxTokens(-10) != 0 {
		t.Error("negative bytes should be 0 tokens")
	}
	if approxTokens(400) != 100 {
		t.Errorf("400 bytes should be 100 tokens (at 4 bytes/token), got %d", approxTokens(400))
	}
}
