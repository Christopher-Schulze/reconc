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
	"unicode/utf8"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/repositorycontrol"
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

func appendRunDecisionRaw(t *testing.T, repo string, d agentsession.RunDecision) {
	t.Helper()
	path := filepath.Join(repo, ".reconc", "run", "decisions.jsonl")
	line, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
}

func TestFollowRunLogTailsNewRecords(t *testing.T) {
	repo := t.TempDir()
	// Seed one record so the log exists; follow baselines its offset to the
	// current size, so the seed must NOT be reprinted - only live appends are.
	writeRunDecisions(t, repo, []agentsession.RunDecision{{Event: "stop", Branch: "seed_record"}})

	var mu sync.Mutex
	var buf bytes.Buffer
	sw := &syncWriter{mu: &mu, w: &buf}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		done <- followRunLog(ctx, repo, "", "", false, 5*time.Millisecond, ready, sw)
	}()

	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("follow exited before becoming ready: %v", err)
	case <-time.After(2 * time.Second):
		cancel()
		<-done
		t.Fatal("follow did not baseline the run log in time")
	}
	appendRunDecisionRaw(t, repo, agentsession.RunDecision{Event: "stop", Branch: "live_tail_branch", Runtime: "cursor"})

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		mu.Lock()
		got := buf.String()
		mu.Unlock()
		if strings.Contains(got, "live_tail_branch") {
			break
		}
		select {
		case <-deadline.C:
			cancel()
			<-done
			t.Fatalf("follow did not tail the live record in time, got: %q", got)
		default:
			time.Sleep(time.Millisecond)
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

func writeRunDecisions(t *testing.T, repo string, ds []agentsession.RunDecision) {
	t.Helper()
	dir := filepath.Join(repo, ".reconc", "run")
	if err := repositorycontrol.EnsureRunDirectory(repo); err != nil {
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
	if err := os.WriteFile(filepath.Join(dir, "decisions.jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunStatusTextAndJSON(t *testing.T) {
	repo := t.TempDir()
	if _, err := agentsession.SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runRunStatus([]string{repo}, &out, &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), "enabled=true") || !strings.Contains(out.String(), "awaiting=false") {
		t.Fatalf("text status missing fields: %s", out.String())
	}

	out.Reset()
	if err := runRunStatus([]string{repo, "--json"}, &out, &out); err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var info agentsession.RepositoryRunStatus
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &info); err != nil {
		t.Fatalf("json parse: %v (%s)", err, out.String())
	}
	if !info.Enabled || info.AwaitingContinuation {
		t.Fatalf("json status mismatch: %+v", info)
	}
}

func TestRunLogRenderFilterLimit(t *testing.T) {
	repo := t.TempDir()
	writeRunDecisions(t, repo, []agentsession.RunDecision{
		{Event: "stop", Branch: "policy_block", Runtime: "cursor", SessionID: "sess-1", PolicyBlocked: true, ViolationCount: 2},
		{Event: "stop", Branch: "policy_block_released_on_repeat", Runtime: "cursor", SessionID: "sess-1"},
		{Event: "command", Branch: "run_command_off", SessionID: "sess-2"},
	})

	var out bytes.Buffer
	if err := runRunLog([]string{repo}, &out, &out); err != nil {
		t.Fatalf("log: %v", err)
	}
	if !strings.Contains(out.String(), "policy_block_released_on_repeat") || !strings.Contains(out.String(), "[policy_block viol=2]") {
		t.Fatalf("log render missing expected content: %s", out.String())
	}

	out.Reset()
	if err := runRunLog([]string{repo, "--branch", "run_command_off"}, &out, &out); err != nil {
		t.Fatalf("log --branch: %v", err)
	}
	if strings.Contains(out.String(), "/policy_block") || !strings.Contains(out.String(), "run_command_off") {
		t.Fatalf("branch filter wrong: %s", out.String())
	}

	out.Reset()
	if err := runRunLog([]string{repo, "--session", "sess-2"}, &out, &out); err != nil {
		t.Fatalf("log --session: %v", err)
	}
	if strings.Contains(out.String(), "sess=sess-1") || !strings.Contains(out.String(), "run_command_off") {
		t.Fatalf("session filter wrong: %s", out.String())
	}

	out.Reset()
	if err := runRunLog([]string{repo, "-n", "1"}, &out, &out); err != nil {
		t.Fatalf("log -n: %v", err)
	}
	if got := strings.Count(strings.TrimSpace(out.String()), "\n"); got != 0 {
		t.Fatalf("-n 1 must render exactly one record, got %d extra lines: %s", got, out.String())
	}
	if !strings.Contains(out.String(), "run_command_off") {
		t.Fatalf("-n 1 must render the last record: %s", out.String())
	}

	out.Reset()
	if err := runRunLog([]string{repo, "--branch", "policy"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("branch filtering must use exact identity, got: %s", out.String())
	}
}

func TestFollowRunLogAfterDoesNotLoseRecordAppendedAfterSnapshot(t *testing.T) {
	repo := t.TempDir()
	seed := agentsession.RunDecision{Event: "stop", Branch: "seed"}
	writeRunDecisions(t, repo, []agentsession.RunDecision{seed})
	follower, _, err := agentsession.NewRunDecisionFollower(repo)
	if err != nil {
		t.Fatal(err)
	}
	appendRunDecisionRaw(t, repo, agentsession.RunDecision{Event: "stop", Branch: "between_snapshot_and_follow"})

	var mu sync.Mutex
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- followRunLogAfter(ctx, follower, "", "", false, 5*time.Millisecond, &syncWriter{mu: &mu, w: &buf})
	}()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		mu.Lock()
		got := buf.String()
		mu.Unlock()
		if strings.Contains(got, "between_snapshot_and_follow") {
			break
		}
		select {
		case <-deadline.C:
			cancel()
			<-done
			t.Fatalf("record appended after snapshot was lost: %q", got)
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestShortIDTruncatesUnicodeByRunes(t *testing.T) {
	const sessionID = "äöüß世界abc"
	got := shortID(sessionID)
	if got != "äöüß世界ab" {
		t.Fatalf("shortID(%q) = %q", sessionID, got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("shortID returned invalid UTF-8: %q", got)
	}
}

func TestRunRejectsUnknownSubcommand(t *testing.T) {
	var out bytes.Buffer
	err := runRunControl([]string{"frobnicate"}, &out, &out)
	if err == nil {
		t.Fatal("unknown subcommand must error")
	}
}

func TestLegacyRunloopCommandIsRemoved(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"runloop", "status", "."}, "test", &out, &out); err == nil {
		t.Fatal("removed runloop compatibility command unexpectedly succeeded")
	}
}

func TestRunControlOnOffIsIdempotent(t *testing.T) {
	repo := t.TempDir()
	var out bytes.Buffer
	for range 2 {
		out.Reset()
		if err := runRunControl([]string{"on", repo, "--force", "--json"}, &out, &out); err != nil {
			t.Fatalf("run on: %v", err)
		}
		var info agentsession.RepositoryRunStatus
		if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &info); err != nil {
			t.Fatalf("decode run on: %v", err)
		}
		if !info.Enabled || info.TaskDisposition != "absent" {
			t.Fatalf("unexpected run on status: %+v", info)
		}
	}
	for range 2 {
		out.Reset()
		if err := runRunControl([]string{"off", repo}, &out, &out); err != nil {
			t.Fatalf("run off: %v", err)
		}
		if !strings.Contains(out.String(), "enabled=false") || !strings.Contains(out.String(), "reason=command_off") {
			t.Fatalf("unexpected run off status: %s", out.String())
		}
	}
	decisions, err := agentsession.ReadRunDecisions(repo, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 || decisions[0].Branch != "run_command_on" || decisions[1].Branch != "run_command_off" {
		t.Fatalf("idempotent switches must log only transitions: %#v", decisions)
	}
}

func TestRunControlDispatchAndArguments(t *testing.T) {
	repo := t.TempDir()
	var out bytes.Buffer
	if err := Run([]string{"run", "on", repo, "--force"}, "test", &out, &out); err != nil {
		t.Fatalf("dispatch run on: %v", err)
	}
	if !strings.Contains(out.String(), "enabled=true") {
		t.Fatalf("canonical status missing enabled state: %s", out.String())
	}
	if err := runRunControl([]string{"on", repo, repo}, &out, &out); err == nil {
		t.Fatal("multiple repo paths must fail")
	}
	if err := runRunControl([]string{"status", repo, repo}, &out, &out); err == nil {
		t.Fatal("multiple status repo paths must fail")
	}
	if err := runRunControl([]string{"log", repo, repo}, &out, &out); err == nil {
		t.Fatal("multiple log repo paths must fail")
	}
	if err := runRunControl([]string{"unknown"}, &out, &out); err == nil {
		t.Fatal("unknown run subcommand must fail")
	}
}

func TestRunOnPreflightRefusesMissingPolicyWithoutStateMutation(t *testing.T) {
	repo := t.TempDir()
	var out bytes.Buffer
	err := runRunControl([]string{"on", repo}, &out, &out)
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "policy sources are not ready") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("missing-policy preflight mismatch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".reconc", "run", "state.bin")); !os.IsNotExist(err) {
		t.Fatalf("failed preflight mutated run state: %v", err)
	}
}

func TestRunOnPreflightAcceptsReadyPolicyAndExecutableTask(t *testing.T) {
	repo := makeTaskCLIRepo(t, "- [~] 001 Active work -> tasks/001-active-work.md", "- [~] Build it")
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runRunControl([]string{"on", repo}, &out, &out); err != nil {
		t.Fatalf("ready preflight: %v", err)
	}
	if !strings.Contains(out.String(), "enabled=true") {
		t.Fatalf("run on status mismatch: %s", out.String())
	}
}

func TestRunOnPreflightRefusesInvalidTaskAndForceIsExplicit(t *testing.T) {
	repo := makeTaskCLIRepo(t, "- [~] 001 Active work -> tasks/001-active-work.md", "- [?] broken")
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runRunControl([]string{"on", repo}, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "TASK plane is invalid") || !strings.Contains(err.Error(), "task validate") {
		t.Fatalf("invalid TASK preflight mismatch: %v", err)
	}
	if err := runRunControl([]string{"on", repo, "--force"}, &out, &out); err != nil {
		t.Fatalf("explicit override failed: %v", err)
	}
}

func TestRunStatusVerboseKeepsDefaultAndJSONContracts(t *testing.T) {
	repo := t.TempDir()
	if _, err := agentsession.SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runRunStatus([]string{repo}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "run: enabled=true task=absent/- open=0 awaiting=false nudges=0 reason=-\n" {
		t.Fatalf("terse status contract drifted: %q", got)
	}
	out.Reset()
	if err := runRunStatus([]string{repo, "--json"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "{\"enabled\":true,\"awaiting_continuation\":false,\"no_progress_nudges\":0,\"task_disposition\":\"absent\",\"open_tasks\":0}\n" {
		t.Fatalf("JSON status contract drifted: %q", got)
	}
	out.Reset()
	if err := runRunStatus([]string{repo, "--verbose"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "last decision:") || !strings.Contains(out.String(), "run_command_on") {
		t.Fatalf("verbose status lacks decision context: %s", out.String())
	}
	if err := runRunStatus([]string{repo, "--verbose", "--json"}, &out, &out); err == nil {
		t.Fatal("verbose JSON combination must fail rather than alter JSON")
	}
}

func TestRunResetCLIRecoversCorruptState(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".reconc", "run", "state.bin")
	if err := repositorycontrol.EnsureRunDirectory(repo); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runRunControl([]string{"reset", repo, "--json"}, &out, &out); err != nil {
		t.Fatalf("run reset: %v", err)
	}
	if !strings.Contains(out.String(), `"enabled":false`) || !strings.Contains(out.String(), `"disabled_reason":"command_off"`) {
		t.Fatalf("run reset JSON mismatch: %s", out.String())
	}
	status, err := agentsession.ReadRepositoryRunStatus(repo)
	if err != nil || status.Enabled {
		t.Fatalf("reset state is not readable and disabled: %+v err=%v", status, err)
	}
}
