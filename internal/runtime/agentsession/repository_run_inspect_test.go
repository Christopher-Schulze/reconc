package agentsession

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/jsonl"
	"reconc.dev/reconc/internal/repositorycontrol"
)

func TestReadRunDecisionsMissingIsEmpty(t *testing.T) {
	repo := t.TempDir()
	ds, err := ReadRunDecisions(repo, 0)
	if err != nil {
		t.Fatalf("missing log must not error: %v", err)
	}
	if len(ds) != 0 {
		t.Fatalf("missing log must be empty, got %d", len(ds))
	}
}

func FuzzDecodeRunDecisionLine(f *testing.F) {
	f.Add([]byte(`{"event":"stop","branch":"main"}`))
	f.Add([]byte(`{"event":"stop"} {"event":"stop"}`))
	f.Add([]byte(`{"unknown":true}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > runDecisionMaxRecordBytes {
			return
		}
		_, _ = decodeRunDecisionLine(body)
	})
}

func TestReadRunDecisionsOrderAndLimit(t *testing.T) {
	repo := t.TempDir()
	for _, b := range []string{"a", "b", "c"} {
		if err := appendRunDecision(repo, RunDecision{Event: "stop", Branch: b}); err != nil {
			t.Fatalf("append %s: %v", b, err)
		}
	}

	all, err := ReadRunDecisions(repo, 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d records", len(all))
	}
	if all[0].Branch != "a" || all[2].Branch != "c" {
		t.Fatalf("append order not preserved: %+v", all)
	}
	last2, err := ReadRunDecisions(repo, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(last2) != 2 || last2[0].Branch != "b" || last2[1].Branch != "c" {
		t.Fatalf("limit must return the last N in order, got %+v", last2)
	}
}

func TestReadRunDecisionsStreamsValidatedTail(t *testing.T) {
	repo := t.TempDir()
	for index := 0; index < 200; index++ {
		if err := appendRunDecision(repo, RunDecision{Event: "stop", Branch: fmt.Sprintf("branch-%03d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	decisions, err := ReadRunDecisions(repo, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 3 || decisions[0].Branch != "branch-197" || decisions[2].Branch != "branch-199" {
		t.Fatalf("streamed tail = %+v", decisions)
	}
	path, err := RunDecisionLogPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".2", []byte("{malformed}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRunDecisions(repo, 1); err == nil || !strings.Contains(err.Error(), "malformed run decision") {
		t.Fatalf("old malformed archive was not validated: %v", err)
	}
}

func TestReadRunDecisionsRejectsOversizeAndSymlinkFiles(t *testing.T) {
	repo := t.TempDir()
	path, err := RunDecisionLogPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(runDecisionMaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Chmod(0o600); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRunDecisions(repo, 1); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize error = %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRunDecisions(repo, 1); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestReadRunDecisionsRejectsMalformedRecord(t *testing.T) {
	repo := t.TempDir()
	if err := appendRunDecision(repo, RunDecision{Event: "stop", Branch: "valid"}); err != nil {
		t.Fatal(err)
	}
	path, err := runDecisionLogPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{ not valid json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	decisions, err := ReadRunDecisions(repo, 0)
	if err == nil {
		t.Fatal("malformed record must fail closed")
	}
	if decisions != nil {
		t.Fatalf("malformed snapshot leaked %d partial decisions", len(decisions))
	}
}

func TestReadRunDecisionsRejectsUnknownField(t *testing.T) {
	repo := t.TempDir()
	path, err := runDecisionLogPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\"event\":\"stop\",\"unexpected\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRunDecisions(repo, 0); err == nil {
		t.Fatal("unknown run-decision field must fail closed")
	}
}

func TestReadRunDecisionsRejectsTruncatedRecord(t *testing.T) {
	repo := t.TempDir()
	path, err := runDecisionLogPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"event":"stop"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRunDecisions(repo, 0); err == nil {
		t.Fatal("record without terminating newline must fail closed")
	}
}

func TestRunDecisionFollowerDistinguishesDuplicateOccurrences(t *testing.T) {
	repo := t.TempDir()
	decision := RunDecision{Timestamp: "2026-08-30T00:00:00Z", Event: "stop", Branch: "duplicate"}
	appendRunDecisionTestRecord(t, repo, decision)
	follower, initial, err := NewRunDecisionFollower(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial) != 1 {
		t.Fatalf("initial decisions = %d, want 1", len(initial))
	}
	appendRunDecisionTestRecord(t, repo, decision)
	next, err := follower.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0] != decision {
		t.Fatalf("duplicate occurrence was skipped or multiplied: %#v", next)
	}
	if next, err = follower.Poll(); err != nil || len(next) != 0 {
		t.Fatalf("idle poll = %#v, %v; want no decisions", next, err)
	}
}

func TestRunDecisionFollowerSurvivesRotation(t *testing.T) {
	repo := t.TempDir()
	appendRunDecisionTestRecord(t, repo, RunDecision{Event: "stop", Branch: "seed"})
	follower, _, err := NewRunDecisionFollower(repo)
	if err != nil {
		t.Fatal(err)
	}
	path, err := runDecisionLogPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appended := RunDecision{Event: "stop", Branch: "after_rotation"}
	appendRunDecisionTestRecord(t, repo, appended)
	next, err := follower.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0] != appended {
		t.Fatalf("rotation decisions = %#v, want appended record", next)
	}
}

func TestRunDecisionFollowerRejectsLostCursor(t *testing.T) {
	repo := t.TempDir()
	appendRunDecisionTestRecord(t, repo, RunDecision{Event: "stop", Branch: "seed"})
	follower, _, err := NewRunDecisionFollower(repo)
	if err != nil {
		t.Fatal(err)
	}
	path, err := runDecisionLogPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, filepath.Join(filepath.Dir(path), "detached.jsonl")); err != nil {
		t.Fatal(err)
	}
	appendRunDecisionTestRecord(t, repo, RunDecision{Event: "stop", Branch: "replacement"})
	if _, err := follower.Poll(); err == nil || !strings.Contains(err.Error(), "left the bounded decision-log window") {
		t.Fatalf("lost cursor must fail closed, got %v", err)
	}
}

func TestRunDecisionFollowerRejectsMalformedAppend(t *testing.T) {
	repo := t.TempDir()
	appendRunDecisionTestRecord(t, repo, RunDecision{Event: "stop", Branch: "seed"})
	follower, _, err := NewRunDecisionFollower(repo)
	if err != nil {
		t.Fatal(err)
	}
	path, err := runDecisionLogPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{malformed}\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := follower.Poll(); err == nil || !strings.Contains(err.Error(), "malformed run decision") {
		t.Fatalf("malformed append must fail closed, got %v", err)
	}
}

func TestRunDecisionFollowerRejectsSameMetadataCursorRewrite(t *testing.T) {
	repo := t.TempDir()
	appendRunDecisionTestRecord(t, repo, RunDecision{Event: "stop", Branch: "seed"})
	follower, _, err := NewRunDecisionFollower(repo)
	if err != nil {
		t.Fatal(err)
	}
	path, err := runDecisionLogPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	modified, err := json.Marshal(RunDecision{Event: "stop", Branch: "evil"})
	if err != nil {
		t.Fatal(err)
	}
	modified = append(modified, '\n')
	if int64(len(modified)) != follower.members[0].info.Size() {
		t.Fatalf("test rewrite size = %d, want %d", len(modified), follower.members[0].info.Size())
	}
	mtime := follower.members[0].info.ModTime()
	if err := os.WriteFile(path, modified, follower.members[0].info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Time{}, mtime); err != nil {
		t.Fatal(err)
	}
	if _, err := follower.Poll(); err == nil || !strings.Contains(err.Error(), "prefix changed") {
		t.Fatalf("same-metadata cursor rewrite must fail closed, got %v", err)
	}
}

func appendRunDecisionTestRecord(t testing.TB, repo string, decision RunDecision) {
	t.Helper()
	path, err := runDecisionLogPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositorycontrol.EnsureRunDirectory(repo); err != nil {
		t.Fatal(err)
	}
	if err := jsonl.AppendWithLayout(
		path, body,
		jsonl.Policy{MaxBytes: runDecisionMaxBytes, MaxArchives: runDecisionMaxArchives},
		repositorycontrol.RunDecisionLayout(path, agentSessionLockTimeout),
	); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkRunDecisionFollower(b *testing.B) {
	b.Run("full_snapshot_idle", benchmarkRunDecisionFullSnapshotIdle)
	b.Run("idle", benchmarkRunDecisionFollowerIdle)
	b.Run("append", benchmarkRunDecisionFollowerAppend)
	b.Run("duplicate", benchmarkRunDecisionFollowerDuplicate)
	b.Run("rotation", benchmarkRunDecisionFollowerRotation)
}

func benchmarkRunDecisionFullSnapshotIdle(b *testing.B) {
	repo := b.TempDir()
	writeRunDecisionBenchmarkFixture(b, repo, 2048)
	b.ReportAllocs()
	for b.Loop() {
		if decisions, err := ReadRunDecisions(repo, 0); err != nil || len(decisions) != 2048 {
			b.Fatalf("full snapshot = %d decisions, %v", len(decisions), err)
		}
	}
}

func benchmarkRunDecisionFollowerIdle(b *testing.B) {
	repo := b.TempDir()
	writeRunDecisionBenchmarkFixture(b, repo, 2048)
	follower, _, err := NewRunDecisionFollower(repo)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if decisions, err := follower.Poll(); err != nil || len(decisions) != 0 {
			b.Fatalf("idle poll = %#v, %v", decisions, err)
		}
	}
}

func writeRunDecisionBenchmarkFixture(b *testing.B, repo string, count int) {
	b.Helper()
	path, err := runDecisionLogPath(repo)
	if err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		b.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		b.Fatal(err)
	}
	var body strings.Builder
	for index := range count {
		encoded, err := json.Marshal(RunDecision{Event: "stop", Branch: fmt.Sprintf("seed-%04d", index)})
		if err != nil {
			b.Fatal(err)
		}
		body.Write(encoded)
		body.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		b.Fatal(err)
	}
}

func benchmarkRunDecisionFollowerAppend(b *testing.B) {
	repo := b.TempDir()
	appendRunDecisionTestRecord(b, repo, RunDecision{Event: "stop", Branch: "seed"})
	follower, _, err := NewRunDecisionFollower(repo)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := appendRunDecision(repo, RunDecision{Event: "stop", Branch: "append"}); err != nil {
			b.Fatal(err)
		}
		if decisions, err := follower.Poll(); err != nil || len(decisions) != 1 {
			b.Fatalf("append poll = %#v, %v", decisions, err)
		}
	}
}

func benchmarkRunDecisionFollowerDuplicate(b *testing.B) {
	repo := b.TempDir()
	decision := RunDecision{Timestamp: "2026-08-30T00:00:00Z", Event: "stop", Branch: "duplicate"}
	appendRunDecisionTestRecord(b, repo, decision)
	follower, _, err := NewRunDecisionFollower(repo)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		appendRunDecisionTestRecord(b, repo, decision)
		if decisions, err := follower.Poll(); err != nil || len(decisions) != 1 {
			b.Fatalf("duplicate poll = %#v, %v", decisions, err)
		}
	}
}

func benchmarkRunDecisionFollowerRotation(b *testing.B) {
	repo := b.TempDir()
	appendRunDecisionTestRecord(b, repo, RunDecision{Event: "stop", Branch: "seed"})
	follower, _, err := NewRunDecisionFollower(repo)
	if err != nil {
		b.Fatal(err)
	}
	path, err := runDecisionLogPath(repo)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := os.Remove(path + ".2"); err != nil && !os.IsNotExist(err) {
			b.Fatal(err)
		}
		if err := os.Rename(path+".1", path+".2"); err != nil && !os.IsNotExist(err) {
			b.Fatal(err)
		}
		if err := os.Rename(path, path+".1"); err != nil {
			b.Fatal(err)
		}
		appendRunDecisionTestRecord(b, repo, RunDecision{Event: "stop", Branch: "rotated"})
		if decisions, err := follower.Poll(); err != nil || len(decisions) != 1 {
			b.Fatalf("rotation poll = %#v, %v", decisions, err)
		}
	}
}

func TestReadRepositoryRunStatusReflectsState(t *testing.T) {
	repo := t.TempDir()
	info, err := ReadRepositoryRunStatus(repo)
	if err != nil {
		t.Fatalf("missing state must not error: %v", err)
	}
	if info.Enabled {
		t.Fatal("missing state must be disabled")
	}
	if err := saveRepositoryRunState(repo, repositoryRunState{
		Enabled:              true,
		AwaitingContinuation: true,
		NoProgressNudges:     2,
	}); err != nil {
		t.Fatal(err)
	}
	info, err = ReadRepositoryRunStatus(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Enabled || !info.AwaitingContinuation || info.NoProgressNudges != 2 {
		t.Fatalf("status snapshot mismatch: %+v", info)
	}
}
