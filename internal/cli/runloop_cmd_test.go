package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

type syncWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func appendRunLoopDecisionRaw(t *testing.T, repo string, d agentsession.RunLoopDecision) {
	t.Helper()
	path := filepath.Join(repo, ".reconc", "runloop", "decisions.jsonl")
	line, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
}

func TestFollowRunLoopLogTailsNewRecords(t *testing.T) {
	repo := t.TempDir()
	// Seed one record so the log exists; follow baselines its offset to the
	// current size, so the seed must NOT be reprinted - only live appends are.
	writeRunLoopDecisions(t, repo, []agentsession.RunLoopDecision{{Event: "stop", Branch: "seed_record"}})

	var mu sync.Mutex
	var buf bytes.Buffer
	sw := &syncWriter{mu: &mu, w: &buf}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- followRunLoopLog(ctx, repo, "", "", false, 5*time.Millisecond, sw)
	}()

	// Append a NEW record after the follow loop is running.
	time.Sleep(20 * time.Millisecond)
	appendRunLoopDecisionRaw(t, repo, agentsession.RunLoopDecision{Event: "stop", Branch: "live_tail_branch", Runtime: "cursor"})

	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		got := buf.String()
		mu.Unlock()
		if strings.Contains(got, "live_tail_branch") {
			break
		}
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("follow did not tail the live record in time, got: %q", got)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("follow returned error: %v", err)
	}
	mu.Lock()
	final := buf.String()
	mu.Unlock()
	if strings.Contains(final, "seed_record") {
		t.Fatalf("follow must not reprint pre-existing records, got: %q", final)
	}
}

func writeRunLoopDecisions(t *testing.T, repo string, ds []agentsession.RunLoopDecision) {
	t.Helper()
	dir := filepath.Join(repo, ".reconc", "runloop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, d := range ds {
		line, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "decisions.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunRunloopStatusTextAndJSON(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".reconc", "runloop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"),
		[]byte(`{"enabled":true,"runtime":"cursor","session_id":"sess-1","active_run_id":"sess-1","awaiting_continuation":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runRunloop([]string{"status", repo}, &out, &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), "enabled=true") || !strings.Contains(out.String(), "runtime=cursor") || !strings.Contains(out.String(), "awaiting=true") {
		t.Fatalf("text status missing fields: %s", out.String())
	}

	out.Reset()
	if err := runRunloop([]string{"status", repo, "--json"}, &out, &out); err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var info agentsession.RunLoopStatusInfo
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &info); err != nil {
		t.Fatalf("json parse: %v (%s)", err, out.String())
	}
	if !info.Enabled || info.Runtime != "cursor" || !info.AwaitingContinuation {
		t.Fatalf("json status mismatch: %+v", info)
	}
}

func TestRunRunloopLogRenderFilterLimit(t *testing.T) {
	repo := t.TempDir()
	writeRunLoopDecisions(t, repo, []agentsession.RunLoopDecision{
		{Event: "stop", Branch: "policy_block", Runtime: "cursor", SessionID: "sess-1", PolicyBlocked: true, ViolationCount: 2},
		{Event: "stop", Branch: "policy_block_released_on_repeat", Runtime: "cursor", SessionID: "sess-1"},
		{Event: "user_prompt", Branch: "disable_user_prompt", Runtime: "claude", SessionID: "sess-2"},
	})

	var out bytes.Buffer
	if err := runRunloop([]string{"log", repo}, &out, &out); err != nil {
		t.Fatalf("log: %v", err)
	}
	if !strings.Contains(out.String(), "policy_block_released_on_repeat") || !strings.Contains(out.String(), "[policy_block viol=2]") {
		t.Fatalf("log render missing expected content: %s", out.String())
	}

	out.Reset()
	if err := runRunloop([]string{"log", repo, "--branch", "disable_user_prompt"}, &out, &out); err != nil {
		t.Fatalf("log --branch: %v", err)
	}
	if strings.Contains(out.String(), "/policy_block") || !strings.Contains(out.String(), "disable_user_prompt") {
		t.Fatalf("branch filter wrong: %s", out.String())
	}

	out.Reset()
	if err := runRunloop([]string{"log", repo, "--session", "sess-2"}, &out, &out); err != nil {
		t.Fatalf("log --session: %v", err)
	}
	if strings.Contains(out.String(), "sess=sess-1") || !strings.Contains(out.String(), "disable_user_prompt") {
		t.Fatalf("session filter wrong: %s", out.String())
	}

	out.Reset()
	if err := runRunloop([]string{"log", repo, "-n", "1"}, &out, &out); err != nil {
		t.Fatalf("log -n: %v", err)
	}
	if got := strings.Count(strings.TrimSpace(out.String()), "\n"); got != 0 {
		t.Fatalf("-n 1 must render exactly one record, got %d extra lines: %s", got, out.String())
	}
	if !strings.Contains(out.String(), "disable_user_prompt") {
		t.Fatalf("-n 1 must render the last record: %s", out.String())
	}
}

func TestRunRunloopRejectsUnknownSubcommand(t *testing.T) {
	var out bytes.Buffer
	err := runRunloop([]string{"frobnicate"}, &out, &out)
	if err == nil {
		t.Fatal("unknown subcommand must error")
	}
}
