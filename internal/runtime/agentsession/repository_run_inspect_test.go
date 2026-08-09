package agentsession

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\"event\":\"stop\",\"unexpected\":true}\n"), 0o644); err != nil {
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"event":"stop"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRunDecisions(repo, 0); err == nil {
		t.Fatal("record without terminating newline must fail closed")
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
