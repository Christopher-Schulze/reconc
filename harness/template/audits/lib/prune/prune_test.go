package prune

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path string, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
}

func setupSessionTree(t *testing.T) (repoRoot string, reconcHome string, projectKeyDir string) {
	t.Helper()
	repoRoot = t.TempDir()
	reconcHome = t.TempDir()
	key := projectKey(repoRoot)
	projectKeyDir = filepath.Join(reconcHome, "sessions", "claude", "projects", key)
	return repoRoot, reconcHome, projectKeyDir
}

func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()
	if p.SessionsRetention != 32 || p.ReportsRetention != 32 || p.CommandProofsRetention != 64 {
		t.Fatalf("unexpected retention: %+v", p)
	}
	if p.AuditJsonlMaxBytes != 2_097_152 || p.AuditJsonlMaxLines != 5_000 {
		t.Fatalf("unexpected jsonl caps: %+v", p)
	}
	if p.PruneIntervalSeconds != 21_600 {
		t.Fatalf("unexpected interval: %d", p.PruneIntervalSeconds)
	}
}

func TestRunKeepsNewestCommandProofs(t *testing.T) {
	repoRoot, home, projectDir := setupSessionTree(t)
	now := time.Now()
	for index := 0; index < 5; index++ {
		writeFile(t, filepath.Join(projectDir, "command-proofs", fmt.Sprintf("proof-%d.json", index)), "{}", now.Add(time.Duration(index)*time.Minute))
	}
	policy := DefaultPolicy()
	policy.CommandProofsRetention = 2
	report := Run(Options{RepoRoot: repoRoot, ReconcHome: home, Policy: policy})
	if report.CommandProofsKept != 2 || report.CommandProofsDeleted != 3 {
		t.Fatalf("unexpected command-proof retention: %+v", report)
	}
}

func TestLoadPolicyMissingReturnsDefault(t *testing.T) {
	got, err := LoadPolicy(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if got != DefaultPolicy() {
		t.Fatalf("expected DefaultPolicy, got %+v", got)
	}
}

func TestLoadPolicyMergesPartialYaml(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "policy.yaml")
	writeFile(t, path, "sessions_retention: 10\naudit_jsonl_max_bytes: 100000\n", time.Time{})
	got, err := LoadPolicy(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.SessionsRetention != 10 {
		t.Fatalf("partial sessions: %d", got.SessionsRetention)
	}
	if got.AuditJsonlMaxBytes != 100000 {
		t.Fatalf("partial bytes: %d", got.AuditJsonlMaxBytes)
	}
	if got.ReportsRetention != DefaultPolicy().ReportsRetention {
		t.Fatalf("missing fields should fall back to default, got reports=%d", got.ReportsRetention)
	}
	if got.PruneIntervalSeconds != DefaultPolicy().PruneIntervalSeconds {
		t.Fatalf("missing interval should fall back, got %d", got.PruneIntervalSeconds)
	}
}

func TestLoadPolicyInvalidYamlReturnsDefault(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bad.yaml")
	writeFile(t, path, "not: [valid", time.Time{})
	got, err := LoadPolicy(path)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if got != DefaultPolicy() {
		t.Fatalf("on parse error must return default, got %+v", got)
	}
}

func TestRunNoOpOnEmptyTree(t *testing.T) {
	repoRoot, home, _ := setupSessionTree(t)
	r := Run(Options{RepoRoot: repoRoot, ReconcHome: home, Policy: DefaultPolicy()})
	if r.SessionsDeleted != 0 || r.ReportsDeleted != 0 || r.JsonlLinesDropped != 0 {
		t.Fatalf("expected no-op, got %+v", r)
	}
	if len(r.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", r.Errors)
	}
}

func TestRunKeepsExactlyNNewestSessions(t *testing.T) {
	repoRoot, home, projectDir := setupSessionTree(t)
	now := time.Now()
	for i := 0; i < 30; i++ {
		path := filepath.Join(projectDir, "sessions", fmt.Sprintf("s%02d.json", i))
		writeFile(t, path, fmt.Sprintf(`{"i":%d}`, i), now.Add(time.Duration(i)*time.Minute))
	}
	policy := DefaultPolicy()
	policy.SessionsRetention = 5
	r := Run(Options{RepoRoot: repoRoot, ReconcHome: home, Policy: policy})
	if r.SessionsKept != 5 || r.SessionsDeleted != 25 {
		t.Fatalf("expected kept=5 deleted=25, got %+v", r)
	}
	entries, _ := os.ReadDir(filepath.Join(projectDir, "sessions"))
	if len(entries) != 5 {
		t.Fatalf("expected 5 files left, got %d", len(entries))
	}
	// Newest 5 by name suffix should remain (s25..s29).
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name()] = true
	}
	for i := 25; i < 30; i++ {
		if !names[fmt.Sprintf("s%02d.json", i)] {
			t.Fatalf("expected s%02d.json to survive, got %v", i, names)
		}
	}
}

func TestRunIdempotent(t *testing.T) {
	repoRoot, home, projectDir := setupSessionTree(t)
	now := time.Now()
	for i := 0; i < 10; i++ {
		writeFile(t, filepath.Join(projectDir, "sessions", fmt.Sprintf("s%02d.json", i)), "{}", now.Add(time.Duration(i)*time.Minute))
	}
	policy := DefaultPolicy()
	policy.SessionsRetention = 3
	first := Run(Options{RepoRoot: repoRoot, ReconcHome: home, Policy: policy})
	if first.SessionsDeleted != 7 {
		t.Fatalf("first run deleted=%d", first.SessionsDeleted)
	}
	second := Run(Options{RepoRoot: repoRoot, ReconcHome: home, Policy: policy})
	if second.SessionsDeleted != 0 || second.SessionsKept != 3 {
		t.Fatalf("second run must be no-op, got %+v", second)
	}
}

func TestRunDryRunDoesNotMutate(t *testing.T) {
	repoRoot, home, projectDir := setupSessionTree(t)
	now := time.Now()
	for i := 0; i < 10; i++ {
		writeFile(t, filepath.Join(projectDir, "sessions", fmt.Sprintf("s%02d.json", i)), "{}", now.Add(time.Duration(i)*time.Minute))
	}
	policy := DefaultPolicy()
	policy.SessionsRetention = 3
	r := Run(Options{RepoRoot: repoRoot, ReconcHome: home, Policy: policy, DryRun: true})
	if r.SessionsDeleted != 7 {
		t.Fatalf("dry run reports planned deletions, got %d", r.SessionsDeleted)
	}
	entries, _ := os.ReadDir(filepath.Join(projectDir, "sessions"))
	if len(entries) != 10 {
		t.Fatalf("dry run must not delete, got %d files", len(entries))
	}
}

func TestRunPrunesReportsIndependently(t *testing.T) {
	repoRoot, home, projectDir := setupSessionTree(t)
	now := time.Now()
	for i := 0; i < 8; i++ {
		writeFile(t, filepath.Join(projectDir, "reports", fmt.Sprintf("r%02d.json", i)), "{}", now.Add(time.Duration(i)*time.Minute))
	}
	policy := DefaultPolicy()
	policy.ReportsRetention = 2
	r := Run(Options{RepoRoot: repoRoot, ReconcHome: home, Policy: policy})
	if r.ReportsKept != 2 || r.ReportsDeleted != 6 {
		t.Fatalf("expected reports kept=2 deleted=6, got %+v", r)
	}
	entries, _ := os.ReadDir(filepath.Join(projectDir, "reports"))
	if len(entries) != 2 {
		t.Fatalf("expected 2 report files, got %d", len(entries))
	}
}

func TestRunPrunesStaleLocks(t *testing.T) {
	repoRoot, home, projectDir := setupSessionTree(t)
	old := time.Now().Add(-48 * time.Hour)
	fresh := time.Now().Add(-1 * time.Hour)
	writeFile(t, filepath.Join(projectDir, "locks", "stale.lock"), "", old)
	writeFile(t, filepath.Join(projectDir, "locks", "fresh.lock"), "", fresh)
	r := Run(Options{RepoRoot: repoRoot, ReconcHome: home, Policy: DefaultPolicy()})
	if r.LocksDeleted != 1 {
		t.Fatalf("expected 1 stale lock removed, got %d", r.LocksDeleted)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "locks", "fresh.lock")); err != nil {
		t.Fatalf("fresh lock must remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "locks", "stale.lock")); !os.IsNotExist(err) {
		t.Fatalf("stale lock must be gone: %v", err)
	}
}

func TestTrimJsonlByLines(t *testing.T) {
	repoRoot, home, _ := setupSessionTree(t)
	path := filepath.Join(repoRoot, ".reconc", "audit.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&sb, `{"i":%d}`+"\n", i)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	policy := DefaultPolicy()
	policy.AuditJsonlMaxLines = 10
	policy.AuditJsonlMaxBytes = 1_000_000
	r := Run(Options{RepoRoot: repoRoot, ReconcHome: home, Policy: policy})
	if r.JsonlLinesDropped != 40 {
		t.Fatalf("expected 40 lines dropped, got %d", r.JsonlLinesDropped)
	}
	body, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) != 10 {
		t.Fatalf("expected 10 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], `"i":40`) || !strings.Contains(lines[9], `"i":49`) {
		t.Fatalf("expected last 10 lines, got first=%s last=%s", lines[0], lines[9])
	}
}

func TestTrimJsonlByBytes(t *testing.T) {
	repoRoot, home, _ := setupSessionTree(t)
	path := filepath.Join(repoRoot, ".reconc", "audit.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&sb, `{"i":%d,"pad":"%s"}`+"\n", i, strings.Repeat("x", 100))
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	policy := DefaultPolicy()
	policy.AuditJsonlMaxLines = 10000
	policy.AuditJsonlMaxBytes = 500
	r := Run(Options{RepoRoot: repoRoot, ReconcHome: home, Policy: policy})
	body, _ := os.ReadFile(path)
	if int64(len(body)) > policy.AuditJsonlMaxBytes {
		t.Fatalf("trimmed file %d bytes still over cap %d", len(body), policy.AuditJsonlMaxBytes)
	}
	if r.JsonlLinesDropped == 0 {
		t.Fatal("expected lines dropped to enforce byte cap")
	}
}

func TestTrimJsonlDropsOversizedLine(t *testing.T) {
	repoRoot, home, _ := setupSessionTree(t)
	path := filepath.Join(repoRoot, ".reconc", "audit.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	giant := strings.Repeat("x", 10_000)
	body := fmt.Sprintf("{\"small\":1}\n{\"giant\":\"%s\"}\n{\"small\":2}\n", giant)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	policy := DefaultPolicy()
	policy.AuditJsonlMaxLines = 100
	policy.AuditJsonlMaxBytes = 5_000
	Run(Options{RepoRoot: repoRoot, ReconcHome: home, Policy: policy})
	out, _ := os.ReadFile(path)
	if strings.Contains(string(out), "giant") {
		t.Fatalf("oversized line must be dropped, got %s", string(out))
	}
	if !strings.Contains(string(out), `"small":1`) || !strings.Contains(string(out), `"small":2`) {
		t.Fatalf("small lines must remain, got %s", string(out))
	}
}

func TestTrimJsonlMissingFile(t *testing.T) {
	repoRoot, home, _ := setupSessionTree(t)
	r := Run(Options{RepoRoot: repoRoot, ReconcHome: home, Policy: DefaultPolicy()})
	if r.JsonlLinesDropped != 0 || r.JsonlBytesFreed != 0 {
		t.Fatalf("missing audit.jsonl must be no-op, got %+v", r)
	}
}

func TestTrimJsonlUnderBudgetIsNoOp(t *testing.T) {
	repoRoot, home, _ := setupSessionTree(t)
	path := filepath.Join(repoRoot, ".reconc", "audit.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{\"a\":1}\n{\"b\":2}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := Run(Options{RepoRoot: repoRoot, ReconcHome: home, Policy: DefaultPolicy()})
	if r.JsonlLinesDropped != 0 {
		t.Fatalf("under-budget must be no-op, got %d dropped", r.JsonlLinesDropped)
	}
}

func TestProjectKeyMatchesReconcFormat(t *testing.T) {
	// Lock the canonical contract: 16 hex chars from sha256(repoRoot).
	got := projectKey("/repo")
	if len(got) != 16 {
		t.Fatalf("project key length: %d", len(got))
	}
	for _, c := range got {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("project key not lowercase hex: %s", got)
		}
	}
}

func TestPolicyPathFromRepo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "tools/reconc/harness/project/config/workflow/prune-policy.yaml"), "sessions_retention: 10\n", time.Now())
	got := PolicyPathFromRepo(root)
	want := filepath.Join(root, filepath.FromSlash("tools/reconc/harness/project/config/workflow/prune-policy.yaml"))
	if got != want {
		t.Fatalf("path: got %s want %s", got, want)
	}
}
