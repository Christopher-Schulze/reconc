package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEnabledEnvOverridesConfig(t *testing.T) {
	t.Setenv("RECONC_AUDIT", "1")
	if !Enabled("/repo", false) {
		t.Error("RECONC_AUDIT=1 must enable even when configEnabled=false")
	}
	t.Setenv("RECONC_AUDIT", "0")
	if Enabled("/repo", true) {
		t.Error("RECONC_AUDIT=0 must disable even when configEnabled=true")
	}
}

func TestEnabledFallsBackToConfig(t *testing.T) {
	os.Unsetenv("RECONC_AUDIT")
	if !Enabled("/repo", true) {
		t.Error("configEnabled=true must enable when env is unset")
	}
	if Enabled("/repo", false) {
		t.Error("configEnabled=false must disable when env is unset")
	}
}

func TestAppendCreatesFile(t *testing.T) {
	repo := t.TempDir()
	entry := Entry{Event: "check", Decision: "pass", OK: true}
	if err := Append(repo, entry, 0); err != nil {
		t.Fatalf("Append: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, AuditFileRelative))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), `"event":"check"`) {
		t.Errorf("log content wrong: %s", string(data))
	}
	// Exactly one newline at end of record.
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("each record must end with a newline")
	}
}

func TestAppendInjectsTimestamp(t *testing.T) {
	repo := t.TempDir()
	if err := Append(repo, Entry{Event: "check"}, 0); err != nil {
		t.Fatalf("Append: %v", err)
	}
	entries, err := Tail(repo, TailOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Timestamp == "" {
		t.Errorf("expected auto-timestamp, got %+v", entries)
	}
}

func TestAppendMultipleProducesJSONL(t *testing.T) {
	repo := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := Append(repo, Entry{Event: "check", Decision: "pass"}, 0); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	data, _ := os.ReadFile(filepath.Join(repo, AuditFileRelative))
	if lines := strings.Count(string(data), "\n"); lines != 3 {
		t.Errorf("expected 3 lines, got %d", lines)
	}
}

func TestTailEmpty(t *testing.T) {
	repo := t.TempDir()
	entries, err := Tail(repo, TailOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty tail, got %d entries", len(entries))
	}
}

func TestTailRespectsN(t *testing.T) {
	repo := t.TempDir()
	for i := 0; i < 5; i++ {
		if err := Append(repo, Entry{Event: "check", Decision: "pass"}, 0); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	entries, err := Tail(repo, TailOptions{N: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestTailFiltersByRule(t *testing.T) {
	repo := t.TempDir()
	if err := Append(repo, Entry{Event: "check", RuleIDs: []string{"r1"}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := Append(repo, Entry{Event: "check", RuleIDs: []string{"r2"}}, 0); err != nil {
		t.Fatal(err)
	}
	entries, err := Tail(repo, TailOptions{RuleID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].RuleIDs[0] != "r1" {
		t.Errorf("wrong entry filtered: %v", entries[0])
	}
}

func TestTailFiltersByDecision(t *testing.T) {
	repo := t.TempDir()
	_ = Append(repo, Entry{Event: "check", Decision: "pass"}, 0)
	_ = Append(repo, Entry{Event: "check", Decision: "block"}, 0)
	_ = Append(repo, Entry{Event: "check", Decision: "warn"}, 0)
	entries, _ := Tail(repo, TailOptions{Decision: "block"})
	if len(entries) != 1 || entries[0].Decision != "block" {
		t.Errorf("expected single block entry, got %v", entries)
	}
}

func TestTailFiltersBySince(t *testing.T) {
	repo := t.TempDir()
	// Pre-fill with a "yesterday" entry and a "now" entry.
	yesterday := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_ = Append(repo, Entry{Timestamp: yesterday, Event: "check"}, 0)
	_ = Append(repo, Entry{Timestamp: now, Event: "check"}, 0)

	// since = now should exclude the yesterday entry.
	entries, _ := Tail(repo, TailOptions{Since: now})
	if len(entries) != 1 || entries[0].Timestamp != now {
		t.Errorf("--since filter wrong: %v", entries)
	}
}

func TestTailSkipsMalformedLines(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, AuditFileRelative)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	// Write one valid, one malformed, one valid line.
	valid := `{"event":"check","decision":"pass","ts":"2026-04-14T00:00:00Z"}` + "\n"
	garbage := "this-is-not-json\n"
	valid2 := `{"event":"check","decision":"block","ts":"2026-04-14T00:00:01Z"}` + "\n"
	if err := os.WriteFile(path, []byte(valid+garbage+valid2), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := Tail(repo, TailOptions{})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected malformed line skipped; got %d entries: %v", len(entries), entries)
	}
}

func TestStatsAggregates(t *testing.T) {
	repo := t.TempDir()
	_ = Append(repo, Entry{Event: "check", Decision: "pass"}, 0)
	_ = Append(repo, Entry{Event: "check", Decision: "block", BlockingCount: 1, RuleIDs: []string{"r1"}}, 0)
	_ = Append(repo, Entry{Event: "ci", Decision: "block", BlockingCount: 2, RuleIDs: []string{"r1", "r2"}}, 0)

	stats, err := Stats(repo)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TotalEntries != 3 {
		t.Errorf("expected 3 entries, got %d", stats.TotalEntries)
	}
	if stats.ByDecision["pass"] != 1 || stats.ByDecision["block"] != 2 {
		t.Errorf("wrong decision counts: %v", stats.ByDecision)
	}
	if stats.BlockingFires != 2 {
		t.Errorf("expected 2 blocking fires, got %d", stats.BlockingFires)
	}
	if len(stats.TopRules) < 1 || stats.TopRules[0].RuleID != "r1" || stats.TopRules[0].Count != 2 {
		t.Errorf("expected r1 as top rule with count 2, got %v", stats.TopRules)
	}
}

func TestRotationCreatesArchive(t *testing.T) {
	repo := t.TempDir()
	// Tiny size cap -- each write exceeds cap and triggers rotation.
	_ = Append(repo, Entry{Event: "check"}, 10)
	_ = Append(repo, Entry{Event: "check"}, 10)

	// Rotation moves the live file to .jsonl.N; the live file may or
	// may not exist at this instant (only re-created by the NEXT
	// Append). What we can guarantee: at least one archive exists.
	live := filepath.Join(repo, AuditFileRelative)
	matches, _ := filepath.Glob(live + ".*")
	if len(matches) == 0 {
		t.Errorf("expected at least one rotated archive file .jsonl.N")
	}
}

func TestExportJSONL(t *testing.T) {
	repo := t.TempDir()
	_ = Append(repo, Entry{Event: "check", Decision: "pass"}, 0)
	_ = Append(repo, Entry{Event: "ci", Decision: "block"}, 0)
	var buf bytes.Buffer
	if err := ExportJSONL(repo, &buf); err != nil {
		t.Fatalf("ExportJSONL: %v", err)
	}
	if strings.Count(buf.String(), "\n") != 2 {
		t.Errorf("expected 2 records in export, got:\n%s", buf.String())
	}
}

func TestExportJSONLMissingFile(t *testing.T) {
	repo := t.TempDir()
	var buf bytes.Buffer
	if err := ExportJSONL(repo, &buf); err != nil {
		t.Errorf("missing file should not error, got: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty export for missing file")
	}
}

// --- rotation errors are propagated ---------------------------------

func TestAppendPropagatesRotationFailure(t *testing.T) {
	repo := t.TempDir()
	// Pre-create all 999 rotation slots so rotate() runs out of room.
	// This simulates the error path without needing a cross-filesystem
	// setup.
	basePath := filepath.Join(repo, AuditFileRelative)
	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 1000; i++ {
		if err := os.WriteFile(fmt.Sprintf("%s.%d", basePath, i), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// First append creates the live file.
	if err := Append(repo, Entry{Event: "check"}, 0); err != nil {
		t.Fatalf("initial append: %v", err)
	}
	// Force rotation: tiny cap makes the next write blow the limit.
	err := Append(repo, Entry{Event: "check"}, 10)
	if err == nil {
		t.Fatal("expected rotation-failure error to propagate")
	}
	if !strings.Contains(err.Error(), "append succeeded but rotation failed") {
		t.Errorf("expected append-succeeded-rotation-failed error, got: %v", err)
	}
	// Record still persisted in the live log despite rotation failure.
	data, rerr := os.ReadFile(basePath)
	if rerr != nil {
		t.Fatalf("live log should exist: %v", rerr)
	}
	if !strings.Contains(string(data), `"event":"check"`) {
		t.Errorf("record should persist before rotation failure, got: %s", string(data))
	}
}

// --- concurrency stress test for O_APPEND claim ---------------------

func TestAuditAppendIsConcurrencySafe(t *testing.T) {
	// The package doc claims "O_APPEND writes are atomic for small
	// records on POSIX". Prove it under contention: N goroutines each
	// append M times, then verify the log has exactly N*M lines and
	// every line is valid JSON (no torn records).
	repo := t.TempDir()
	const (
		writers       = 50
		perWriter     = 20
		expectedLines = writers * perWriter
	)

	var wg sync.WaitGroup
	wg.Add(writers)
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				entry := Entry{
					Event:    "check",
					Decision: "pass",
					RuleIDs:  []string{fmt.Sprintf("worker-%d-%d", id, i)},
				}
				if err := Append(repo, entry, 0); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("writer error: %v", err)
		}
	}

	// Count records AND verify each line decodes as JSON.
	path := filepath.Join(repo, AuditFileRelative)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != expectedLines {
		t.Errorf("expected %d records, got %d", expectedLines, len(lines))
	}
	for i, line := range lines {
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Errorf("line %d not valid JSON (record torn?): %v", i, err)
			if i > 5 {
				break
			}
		}
	}
}

func BenchmarkAuditRecordSize(b *testing.B) {
	// Reports the max serialised size of a representative Entry. If any
	// future change pushes record size past PIPE_BUF (4096 bytes on most
	// POSIX systems), concurrent appends could interleave. This benchmark
	// surfaces that risk via an assertion.
	entry := Entry{
		Timestamp:      "2026-04-14T00:00:00Z",
		Event:          "check",
		Decision:       "block",
		OK:             false,
		RuleIDs:        []string{"a-rule", "another-rule", "a-third-rule"},
		ViolationCount: 3,
		BlockingCount:  2,
		WritePaths:     []string{"src/main.go", "docs/x.md", "tests/a_test.go"},
		ReadPaths:      []string{"AGENTS.md", "docs/spec.md"},
		Commands:       []string{"go test ./...", "go vet ./..."},
		Claims:         []string{"ci-green"},
		RepoRoot:       "/repo/reconc",
		ReconcVersion:  "0.4.0",
	}
	data, err := json.Marshal(entry)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("typical audit record: %d bytes (PIPE_BUF safe ceiling: 4096)", len(data))
	if len(data) > 4096 {
		b.Errorf("record size %d exceeds 4 KiB PIPE_BUF safety ceiling", len(data))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(entry)
	}
}
