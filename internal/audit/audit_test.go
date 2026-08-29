package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reconc.dev/reconc/internal/jsonl"
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

func TestTailRecoversPublishedAuditTransaction(t *testing.T) {
	repo := t.TempDir()
	if err := Append(repo, Entry{Event: "before-crash", Decision: "pass"}, 0); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, AuditFileRelative)
	entry := normalizeEntry(Entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Event:     "published-before-crash",
		Decision:  "pass",
	})
	prepare := func() ([]byte, error) {
		entries, _, err := loadVerifiedSnapshot(repo)
		if err != nil {
			return nil, err
		}
		last := entries[len(entries)-1]
		entry.Sequence = last.Sequence + 1
		entry.PreviousDigest = last.Digest
		entry.ChainVersion = auditChainVersion
		digest, err := entryDigest(entry)
		if err != nil {
			return nil, err
		}
		entry.Digest = digest
		return json.Marshal(entry)
	}
	injected := errors.New("injected detached-head crash")
	err := jsonl.AppendTransaction(path, jsonl.Policy{MaxBytes: DefaultMaxSizeBytes, MaxArchives: MaxArchiveFiles}, prepare, func() error {
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("injected publication failure = %v", err)
	}
	entries, err := Tail(repo, TailOptions{})
	if err != nil {
		t.Fatalf("recover published audit transaction: %v", err)
	}
	if len(entries) != 2 || entries[1].Event != "published-before-crash" {
		t.Fatalf("recovered entries = %+v", entries)
	}
	if report, err := Verify(repo); err != nil || !report.Valid || report.Entries != 2 {
		t.Fatalf("recovered chain = %+v err=%v", report, err)
	}
	if _, err := os.Stat(path + ".append-transaction.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery journal remains: %v", err)
	}
}

func TestRecoverPendingAppendDoesNotFallbackForLookalikeError(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "belongs to a different layout")
	path := filepath.Join(repo, AuditFileRelative)
	layout := auditLayout(path)
	policy := jsonl.Policy{MaxBytes: DefaultMaxSizeBytes, MaxArchives: MaxArchiveFiles}
	injected := errors.New("audit commit failed")
	if err := jsonl.AppendTransactionWithLayout(path, policy, layout, func() ([]byte, error) {
		return []byte("{malformed"), nil
	}, func() error {
		return injected
	}); !errors.Is(err, injected) {
		t.Fatalf("initial interrupted append = %v, want %v", err, injected)
	}

	err := recoverPendingAppend(repo)
	if err == nil {
		t.Fatal("lookalike recovery unexpectedly succeeded")
	}
	if errors.Is(err, jsonl.ErrLayoutMismatch) {
		t.Fatalf("lookalike recovery incorrectly triggered legacy fallback: %v", err)
	}
	if !strings.Contains(err.Error(), "belongs to a different layout") {
		t.Fatalf("lookalike recovery error = %v, want path-derived lookalike wording", err)
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

func TestTailRejectsMalformedLines(t *testing.T) {
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
	if err == nil {
		t.Fatalf("malformed audit line must fail closed, got %d entries: %v", len(entries), entries)
	}
	if !strings.Contains(err.Error(), path+":2 contains malformed JSON") {
		t.Fatalf("malformed audit error = %v, want exact source and line", err)
	}
}

func TestTailRejectsOversizedRecordWithLineContext(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, AuditFileRelative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	oversized := append(bytes.Repeat([]byte{'x'}, maxRecordBytes), '\n')
	if err := os.WriteFile(path, oversized, 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := Tail(repo, TailOptions{})
	if err == nil {
		t.Fatalf("oversized audit record unexpectedly decoded %d entries", len(entries))
	}
	want := fmt.Sprintf("%s:1 exceeds the %d-byte record limit", path, maxRecordBytes)
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("oversized audit error = %v, want %q", err, want)
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
	if stats.LatestDecision != "block" || stats.LatestBlockingCount != 2 {
		t.Errorf("wrong latest audit state: decision=%q blocking=%d", stats.LatestDecision, stats.LatestBlockingCount)
	}
	if stats.EntriesLastHour != 3 || stats.BlockingEntriesLast24h != 2 {
		t.Errorf("wrong recent audit counts: hour=%d blocking24h=%d", stats.EntriesLastHour, stats.BlockingEntriesLast24h)
	}
	if len(stats.TopRules) < 1 || stats.TopRules[0].RuleID != "r1" || stats.TopRules[0].Count != 2 {
		t.Errorf("expected r1 as top rule with count 2, got %v", stats.TopRules)
	}
}

func TestRotationCreatesArchive(t *testing.T) {
	repo := t.TempDir()
	// One record fits, two do not, so the second append rotates first.
	_ = Append(repo, Entry{Event: "check"}, 400)
	_ = Append(repo, Entry{Event: "check"}, 400)

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

func TestRotationKeepsFixedArchiveRing(t *testing.T) {
	repo := t.TempDir()
	basePath := filepath.Join(repo, AuditFileRelative)
	for i := 0; i < 8; i++ {
		if err := Append(repo, Entry{Event: fmt.Sprintf("check-%d", i)}, 400); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(basePath + ".3"); !os.IsNotExist(err) {
		t.Fatalf("archive ring escaped fixed size: %v", err)
	}
	for _, path := range []string{basePath, basePath + ".1", basePath + ".2"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected ring member %s: %v", path, err)
		}
		if info.Size() > 400 {
			t.Fatalf("ring member %s exceeds cap: %d", path, info.Size())
		}
	}
	if report, err := Verify(repo); err != nil || !report.Valid {
		t.Fatalf("rotated chain must verify: %+v err=%v", report, err)
	}
}

func TestEnforceRetentionVerifiesWithoutRewritingChainedEvidence(t *testing.T) {
	repo := t.TempDir()
	for index := 0; index < 3; index++ {
		if err := Append(repo, Entry{Event: fmt.Sprintf("event-%d", index), Decision: "pass"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(repo, AuditFileRelative)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := EnforceRetention(repo)
	if err != nil {
		t.Fatal(err)
	}
	if result.BytesFreed != 0 || result.FilesRemoved != 0 {
		t.Fatalf("writer-owned audit retention unexpectedly rewrote evidence: %+v", result)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("retention changed chained audit evidence: err=%v", err)
	}
}

func TestVerifyRejectsModifiedReorderedMissingAndTruncatedRecords(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]string) []string
	}{
		{
			name: "modified",
			mutate: func(lines []string) []string {
				lines[0] = strings.Replace(lines[0], `"event":"event-0"`, `"event":"changed"`, 1)
				return lines
			},
		},
		{
			name: "reordered",
			mutate: func(lines []string) []string {
				lines[0], lines[1] = lines[1], lines[0]
				return lines
			},
		},
		{
			name: "missing",
			mutate: func(lines []string) []string {
				return append(lines[:1], lines[2:]...)
			},
		},
		{
			name: "unknown field",
			mutate: func(lines []string) []string {
				lines[0] = strings.TrimSuffix(lines[0], "}") + `,"unexpected":true}`
				return lines
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			for index := 0; index < 3; index++ {
				if err := Append(repo, Entry{Event: fmt.Sprintf("event-%d", index), Decision: "pass"}, 0); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(repo, AuditFileRelative)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
			lines = test.mutate(lines)
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Verify(repo); err == nil {
				t.Fatal("tampered audit chain must fail verification")
			}
		})
	}

	t.Run("truncated", func(t *testing.T) {
		repo := t.TempDir()
		if err := Append(repo, Entry{Event: "event", Decision: "pass"}, 0); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(repo, AuditFileRelative)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, bytes.TrimSuffix(data, []byte{'\n'}), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(repo); err == nil || !strings.Contains(err.Error(), "truncated") {
			t.Fatalf("truncated audit record must fail verification, got %v", err)
		}
	})
}

func TestAppendHotPathDefersInteriorVerificationUntilExplicitGate(t *testing.T) {
	repo := t.TempDir()
	for index := 0; index < 3; index++ {
		if err := Append(repo, Entry{Event: fmt.Sprintf("event-%d", index), Decision: "pass"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(repo, AuditFileRelative)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = bytes.Replace(body, []byte(`"event":"event-0"`), []byte(`"event":"changed"`), 1)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Append(repo, Entry{Event: "incremental", Decision: "pass"}, 0); err != nil {
		t.Fatalf("normal append should use the verified tail checkpoint: %v", err)
	}
	if _, err := Verify(repo); err == nil {
		t.Fatal("explicit verification must still detect interior tampering")
	}
}

func TestAppendRotationPerformsFullVerification(t *testing.T) {
	repo := t.TempDir()
	for index := 0; index < 3; index++ {
		if err := Append(repo, Entry{Event: fmt.Sprintf("event-%d", index), Decision: "pass"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(repo, AuditFileRelative)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = bytes.Replace(body, []byte(`"event":"event-0"`), []byte(`"event":"changed"`), 1)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Append(repo, Entry{Event: "rotation", Decision: "pass"}, int64(len(body)+1)); err == nil {
		t.Fatal("rotation must fully verify retained records before mutation")
	}
}

func TestAppendRejectsTailCheckpointDrift(t *testing.T) {
	repo := t.TempDir()
	if err := Append(repo, Entry{Event: "original", Decision: "pass"}, 0); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, AuditFileRelative)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = bytes.Replace(body, []byte(`"event":"original"`), []byte(`"event":"modified"`), 1)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Append(repo, Entry{Event: "next", Decision: "pass"}, 0); err == nil {
		t.Fatal("tampered live tail must block incremental append")
	}
}

func TestVerifyRejectsMissingDetachedHead(t *testing.T) {
	repo := t.TempDir()
	if err := Append(repo, Entry{Event: "check", Decision: "pass"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, AuditHeadRelative)); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(repo); err == nil || !strings.Contains(err.Error(), "no detached head") {
		t.Fatalf("missing detached head must fail verification, got %v", err)
	}
}

func TestVerifyRejectsUnknownDetachedHeadField(t *testing.T) {
	repo := t.TempDir()
	if err := Append(repo, Entry{Event: "check", Decision: "pass"}, 0); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, AuditHeadRelative)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSpace(string(data))
	body = strings.TrimSuffix(body, "}") + `,"unexpected":true}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(repo); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown detached-head field must fail verification, got %v", err)
	}
}

func TestTailSinceUsesParsedChronologyAcrossOffsets(t *testing.T) {
	repo := t.TempDir()
	if err := Append(repo, Entry{Timestamp: "2026-04-14T01:30:00+02:00", Event: "older"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := Append(repo, Entry{Timestamp: "2026-04-14T00:00:00Z", Event: "newer"}, 0); err != nil {
		t.Fatal(err)
	}
	entries, err := Tail(repo, TailOptions{Since: "2026-04-13T23:45:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Event != "newer" {
		t.Fatalf("timezone-aware since filter mismatch: %+v", entries)
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
	// Reports the serialised size of a representative Entry and locks the
	// bounded-record contract. Cross-process append safety comes from the
	// JSONL file lock rather than a platform-specific PIPE_BUF assumption.
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
		ReconcVersion:  "0.5.0",
	}
	data, err := json.Marshal(entry)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("typical audit record: %d bytes (hard ceiling: %d)", len(data), maxRecordBytes)
	if len(data)+1 > maxRecordBytes {
		b.Errorf("record size %d exceeds %d-byte ceiling", len(data)+1, maxRecordBytes)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(entry)
	}
}

func BenchmarkAuditAppendRetainedChain(b *testing.B) {
	for _, retained := range []int{0, 200} {
		b.Run(fmt.Sprintf("retained-%d", retained), func(b *testing.B) {
			repo := b.TempDir()
			for index := 0; index < retained; index++ {
				if err := Append(repo, Entry{Event: "prefill", Decision: "pass"}, 0); err != nil {
					b.Fatal(err)
				}
			}
			entry := Entry{Event: "benchmark", Decision: "pass", OK: true}
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				if err := Append(repo, entry, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkAuditReadMaximumRing(b *testing.B) {
	line := []byte(`{"event":"benchmark","decision":"pass"}` + "\n")
	recordsPerFile := (DefaultMaxSizeBytes - 1) / len(line)
	body := bytes.Repeat(line, recordsPerFile)
	path := filepath.Join(b.TempDir(), "audit.jsonl")
	for _, suffix := range []string{".2", ".1", ""} {
		if err := os.WriteFile(path+suffix, body, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(len(body) * (MaxArchiveFiles + 1)))
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		entries, err := readAuditEntries(path)
		if err != nil {
			b.Fatal(err)
		}
		if len(entries) != recordsPerFile*(MaxArchiveFiles+1) {
			b.Fatalf("read %d entries, want %d", len(entries), recordsPerFile*(MaxArchiveFiles+1))
		}
	}
}
