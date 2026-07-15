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

func scan(t *testing.T, repo string, files []string, tokenBudget int) ScanReport {
	t.Helper()
	report, err := Scan(repo, files, tokenBudget)
	if err != nil {
		t.Fatalf("scan context: %v", err)
	}
	return report
}

func TestScanEmptyRepo(t *testing.T) {
	repo := t.TempDir()
	r := scan(t, repo, nil, 0)
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
	mustWrite(t, filepath.Join(repo, "docs", "tasks.md"), strings.Repeat("y", 4000))

	r := scan(t, repo, nil, 0)
	if r.TotalBytes != 5000 {
		t.Errorf("expected 5000 total bytes, got %d", r.TotalBytes)
	}
	expectedTokens := (1000 + 4000) / BytesPerTokenEstimate
	if r.TotalApproxTokens != expectedTokens {
		t.Errorf("expected %d approx tokens, got %d", expectedTokens, r.TotalApproxTokens)
	}
	if r.Largest != "docs/tasks.md" {
		t.Errorf("expected largest=docs/tasks.md, got %q", r.Largest)
	}
}

func TestScanOverBudgetTrips(t *testing.T) {
	repo := t.TempDir()
	// 40000 bytes = ~10000 tokens. Budget 100 -> over.
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), strings.Repeat("x", 40000))
	r := scan(t, repo, nil, 100)
	if !r.OverBudget {
		t.Error("expected OverBudget=true")
	}
}

func TestScanUnderBudget(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "small")
	r := scan(t, repo, nil, 10000)
	if r.OverBudget {
		t.Errorf("expected under budget, got %+v", r)
	}
}

func TestScanCustomFileList(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "custom.md"), strings.Repeat("x", 400))
	r := scan(t, repo, []string{"custom.md"}, 0)
	if len(r.Files) != 1 {
		t.Errorf("expected 1 file in report, got %d", len(r.Files))
	}
	if !r.Files[0].Exists || r.Files[0].Path != "custom.md" {
		t.Errorf("unexpected custom report: %+v", r.Files[0])
	}
}

func TestScanDeduplicatesNormalizedPaths(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "12345")
	r := scan(t, repo, []string{"AGENTS.md", "./AGENTS.md", "AGENTS.md"}, 0)
	if len(r.Files) != 1 {
		t.Fatalf("expected one normalized file, got %+v", r.Files)
	}
	if r.TotalBytes != 5 || r.TotalApproxTokens != 2 {
		t.Fatalf("duplicate paths changed totals: %+v", r)
	}
}

func TestScanSortsByTokensDesc(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), strings.Repeat("x", 100))
	mustWrite(t, filepath.Join(repo, "start.md"), strings.Repeat("x", 5000))
	mustWrite(t, filepath.Join(repo, "docs", "tasks.md"), strings.Repeat("x", 2000))

	r := scan(t, repo, nil, 0)
	// Existing files should be first (descending tokens), absents last.
	// Top two should be start.md, then docs/tasks.md, then AGENTS.md.
	existing := []string{}
	for _, f := range r.Files {
		if f.Exists {
			existing = append(existing, f.Path)
		}
	}
	wantOrder := []string{"start.md", "docs/tasks.md", "AGENTS.md"}
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
	r := scan(t, repo, nil, 0)
	// Missing files should not add to total.
	if r.TotalApproxTokens != approxTokens(5) {
		t.Errorf("missing files must not contribute to total; got %d", r.TotalApproxTokens)
	}
}

func TestScanRejectsPathsOutsideRepository(t *testing.T) {
	repo := t.TempDir()
	if _, err := Scan(repo, []string{"../outside.md"}, 0); err == nil {
		t.Fatal("expected lexical repository escape to fail")
	}
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret.md"), "secret")
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(repo, "linked.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Scan(repo, []string{"linked.md"}, 0); err == nil {
		t.Fatal("expected symlink repository escape to fail")
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
	if approxTokens(1) != 1 || approxTokens(5) != 2 {
		t.Errorf("non-empty files must round up: 1 byte=%d, 5 bytes=%d", approxTokens(1), approxTokens(5))
	}
}
