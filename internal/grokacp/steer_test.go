//go:build !windows

package grokacp

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func steerTestRepo(t *testing.T) string {
	t.Helper()
	originalCapabilityProbe := nativeStopGateAvailable
	nativeStopGateAvailable = func() bool { return false }
	t.Cleanup(func() { nativeStopGateAvailable = originalCapabilityProbe })
	t.Setenv("RECONC_CLAUDE_STATE_DIR", t.TempDir())
	t.Setenv(leaderSocketEnv, "")
	t.Setenv(grokHomeEnv, t.TempDir())
	t.Setenv(SteerEnv, "")
	t.Setenv("GROK_SESSION_ID", "")
	return t.TempDir()
}

func steerSession(t *testing.T, sessionID string) {
	t.Helper()
	t.Setenv("GROK_SESSION_ID", sessionID)
}

func steerPayload(sessionID string, interrupt bool) []byte {
	payload := map[string]interface{}{
		"session_id":     sessionID,
		"reconc_runtime": "grok",
	}
	if interrupt {
		payload["is_interrupt"] = true
	}
	body, _ := json.Marshal(payload)
	return body
}

func continuationResult(reason string) agentsession.Result {
	body, _ := json.Marshal(map[string]string{"decision": "block", "reason": reason})
	return agentsession.Result{ExitCode: 0, Stdout: string(body)}
}

func TestSteerTUIStopGateConditions(t *testing.T) {
	repo := steerTestRepo(t)
	leader := newFakeLeader(t, serveInterject(`{"jsonrpc":"2.0","id":1,"result":{"status":"queued"}}`))

	tests := []struct {
		name    string
		setup   func(t *testing.T)
		payload []byte
		result  agentsession.Result
	}{
		{
			name: "clean stop stays silent",
			setup: func(t *testing.T) {
				t.Setenv(leaderSocketEnv, leader.socket)
				steerSession(t, "s-clean")
			},
			payload: steerPayload("s-clean", false),
			result:  agentsession.Result{ExitCode: 0},
		},
		{
			name: "stop errors stay passive",
			setup: func(t *testing.T) {
				t.Setenv(leaderSocketEnv, leader.socket)
				steerSession(t, "s-err")
			},
			payload: steerPayload("s-err", false),
			result:  agentsession.Result{ExitCode: 2, Stderr: "boom"},
		},
		{
			name: "user interrupt is never overridden",
			setup: func(t *testing.T) {
				t.Setenv(leaderSocketEnv, leader.socket)
				steerSession(t, "s-int")
			},
			payload: steerPayload("s-int", true),
			result:  continuationResult("finish the TASK"),
		},
		{
			name: "steering disabled by env",
			setup: func(t *testing.T) {
				t.Setenv(leaderSocketEnv, leader.socket)
				steerSession(t, "s-off")
				t.Setenv(SteerEnv, "0")
			},
			payload: steerPayload("s-off", false),
			result:  continuationResult("finish the TASK"),
		},
		{
			name:    "no leader socket stays silent",
			setup:   func(t *testing.T) { steerSession(t, "s-nosock") },
			payload: steerPayload("s-nosock", false),
			result:  continuationResult("finish the TASK"),
		},
		{
			name: "missing session id stays silent",
			setup: func(t *testing.T) {
				t.Setenv(leaderSocketEnv, leader.socket)
				steerSession(t, "s-missing")
			},
			payload: []byte(`{"reconc_runtime":"grok"}`),
			result:  continuationResult("finish the TASK"),
		},
		{
			name:    "no GROK_SESSION_ID means no live Grok dispatch",
			setup:   func(t *testing.T) { t.Setenv(leaderSocketEnv, leader.socket) },
			payload: steerPayload("s-noenv", false),
			result:  continuationResult("finish the TASK"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.setup(t)
			if note := SteerTUIStop(repo, test.payload, test.result); note != "" {
				t.Fatalf("steering must not act, got note %q", note)
			}
		})
	}
	if got := len(leader.messages()); got != 0 {
		t.Fatalf("gated cases must never reach the leader, saw %d messages", got)
	}
}

func TestPrepareStrictTUIStop(t *testing.T) {
	_ = steerTestRepo(t)
	leader := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {})
	t.Setenv(leaderSocketEnv, leader.socket)
	steerSession(t, "s-strict")

	prepared, strict, err := PrepareStrictTUIStop(steerPayload("s-strict", false))
	if err != nil {
		t.Fatal(err)
	}
	if !strict {
		t.Fatal("live leader stop must enable strict continuation")
	}
	payload, err := agentsession.ParsePayload(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if !payload.StrictContinuation {
		t.Fatalf("prepared payload = %s", prepared)
	}

	prepared, strict, err = PrepareStrictTUIStop(steerPayload("s-strict", true))
	if err != nil || strict {
		t.Fatalf("interrupt preparation = strict=%v err=%v payload=%s", strict, err, prepared)
	}
}

func TestSteerTUIStopInterjectsAndCounts(t *testing.T) {
	repo := steerTestRepo(t)
	leader := newFakeLeader(t, serveInterject(`{"jsonrpc":"2.0","id":1,"result":{"status":"queued"}}`))
	t.Setenv(leaderSocketEnv, leader.socket)

	steerSession(t, "s-go")
	note := SteerTUIStop(repo, steerPayload("s-go", false), continuationResult("run the tests"))
	if !strings.Contains(note, "continuation interjected (1/32)") {
		t.Fatalf("note = %q", note)
	}

	messages := leader.messages()
	if len(messages) < 2 {
		t.Fatalf("leader saw %d messages", len(messages))
	}
	var request struct {
		Method string `json:"method"`
		Params struct {
			SessionID string `json:"sessionId"`
			Text      string `json:"text"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(messages[1].Payload), &request); err != nil {
		t.Fatal(err)
	}
	if request.Params.SessionID != "s-go" || request.Params.Text != "run the tests" {
		t.Fatalf("interjected request = %+v", request)
	}

	state, err := agentsession.LoadSessionState(repo, "s-go")
	if err != nil {
		t.Fatal(err)
	}
	if state.GrokSteerAttempts != 1 {
		t.Fatalf("GrokSteerAttempts = %d, want 1", state.GrokSteerAttempts)
	}
}

func TestSteerTUIStopSkipsNativeStopCapableLeader(t *testing.T) {
	repo := steerTestRepo(t)
	nativeStopGateAvailable = func() bool { return true }
	leader := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {
		if _, err := f.read(conn); err != nil {
			return
		}
		f.write(conn, `{"type":"registered","client_id":7,"ready":true,"leader_protocol_version":1,"leader_binary_version":"0.2.106"}`)
		_, _ = f.read(conn)
	})
	t.Setenv(leaderSocketEnv, leader.socket)
	steerSession(t, "s-native")

	if note := SteerTUIStop(repo, steerPayload("s-native", false), continuationResult("run the tests")); note != "" {
		t.Fatalf("native Stop leader must not be interjected, note = %q", note)
	}
	for _, message := range leader.messages() {
		if message.Type == "acp" {
			t.Fatalf("native Stop leader received duplicate ACP interjection: %+v", message)
		}
	}
	state, err := agentsession.LoadSessionState(repo, "s-native")
	if err != nil {
		t.Fatal(err)
	}
	if state.GrokSteerAttempts != 0 {
		t.Fatalf("suppressed native leader consumed fallback budget: %d", state.GrokSteerAttempts)
	}
}

func TestSteerTUIStopDoesNotInferNativeStopFromVersion(t *testing.T) {
	repo := steerTestRepo(t)
	leader := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {
		if _, err := f.read(conn); err != nil {
			return
		}
		f.write(conn, `{"type":"registered","client_id":7,"ready":true,"leader_protocol_version":1,"leader_binary_version":"0.2.106"}`)
		if _, err := f.read(conn); err != nil {
			return
		}
		f.write(conn, `{"type":"acp","payload":"{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"status\":\"queued\"}}"}`)
		_, _ = f.read(conn)
	})
	t.Setenv(leaderSocketEnv, leader.socket)
	steerSession(t, "s-version")

	note := SteerTUIStop(repo, steerPayload("s-version", false), continuationResult("run the tests"))
	if !strings.Contains(note, "continuation interjected") {
		t.Fatalf("version-only capability must retain leader fallback, note = %q", note)
	}
}

func TestSteerTUIStopBudgetExhaustion(t *testing.T) {
	repo := steerTestRepo(t)
	leader := newFakeLeader(t, serveInterject(`{"jsonrpc":"2.0","id":1,"result":{"status":"queued"}}`))
	t.Setenv(leaderSocketEnv, leader.socket)

	if _, err := agentsession.MutateSessionState(repo, "s-cap", func(state agentsession.SessionState) agentsession.SessionState {
		state.GrokSteerAttempts = maxStopSteerAttempts
		state.GrokSteerContinuationKey = steerContinuationKey("more work")
		state.GrokSteerMaterialEvents = state.MaterialEvents
		return state
	}); err != nil {
		t.Fatal(err)
	}

	steerSession(t, "s-cap")
	note := SteerTUIStop(repo, steerPayload("s-cap", false), continuationResult("more work"))
	if !strings.Contains(note, "budget exhausted") {
		t.Fatalf("note = %q", note)
	}
	if got := len(leader.messages()); got != 0 {
		t.Fatalf("exhausted budget must not reach the leader, saw %d messages", got)
	}
}

func TestSteerBudgetResetsOnProgressNewBlockAndCleanStop(t *testing.T) {
	repo := steerTestRepo(t)
	leader := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {})
	t.Setenv(leaderSocketEnv, leader.socket)
	steerSession(t, "s-reset")

	firstReason := "reconc: block\nFeedback: RB-111"
	if attempts, allowed, err := recordSuccessfulSteerAttemptForTest(repo, "s-reset", firstReason); err != nil || !allowed || attempts != 1 {
		t.Fatalf("first attempt = attempts=%d allowed=%v err=%v", attempts, allowed, err)
	}
	if attempts, allowed, err := recordSuccessfulSteerAttemptForTest(repo, "s-reset", "same report\nFeedback: RB-111"); err != nil || !allowed || attempts != 2 {
		t.Fatalf("same-block attempt = attempts=%d allowed=%v err=%v", attempts, allowed, err)
	}

	if _, err := agentsession.MutateSessionState(repo, "s-reset", func(state agentsession.SessionState) agentsession.SessionState {
		state.MaterialEvents++
		return state
	}); err != nil {
		t.Fatal(err)
	}
	if attempts, allowed, err := recordSuccessfulSteerAttemptForTest(repo, "s-reset", firstReason); err != nil || !allowed || attempts != 1 {
		t.Fatalf("progress reset = attempts=%d allowed=%v err=%v", attempts, allowed, err)
	}
	if attempts, allowed, err := recordSuccessfulSteerAttemptForTest(repo, "s-reset", "different block\nFeedback: RB-222"); err != nil || !allowed || attempts != 1 {
		t.Fatalf("new-block reset = attempts=%d allowed=%v err=%v", attempts, allowed, err)
	}

	t.Setenv(leaderSocketEnv, "")
	if note := SteerTUIStop(repo, steerPayload("s-reset", false), agentsession.Result{ExitCode: 0}); note != "" {
		t.Fatalf("clean stop reset note = %q", note)
	}
	state, err := agentsession.LoadSessionState(repo, "s-reset")
	if err != nil {
		t.Fatal(err)
	}
	if state.GrokSteerAttempts != 0 || state.GrokSteerContinuationKey != "" || state.GrokSteerMaterialEvents != 0 {
		t.Fatalf("clean stop did not reset budget: %+v", state)
	}
}

func TestSteerTUIStopFailsOpenOnLeaderRejection(t *testing.T) {
	repo := steerTestRepo(t)
	leader := newFakeLeader(t, serveInterject(
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"Invalid params","data":"session not found: s-gone"}}`,
	))
	t.Setenv(leaderSocketEnv, leader.socket)

	steerSession(t, "s-gone")
	note := SteerTUIStop(repo, steerPayload("s-gone", false), continuationResult("more work"))
	if !strings.Contains(note, "steer failed") || !strings.Contains(note, "session not found") {
		t.Fatalf("note = %q", note)
	}
	state, err := agentsession.LoadSessionState(repo, "s-gone")
	if err != nil {
		t.Fatal(err)
	}
	if state.GrokSteerAttempts != 0 {
		t.Fatalf("transport failure must not consume the no-progress budget, got %d", state.GrokSteerAttempts)
	}
}

func TestSteerTUIStopRejectsIncompatibleLeaderProtocol(t *testing.T) {
	repo := steerTestRepo(t)
	leader := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {
		if _, err := f.read(conn); err != nil {
			return
		}
		f.write(conn, `{"type":"registered","client_id":1,"ready":true,"leader_protocol_version":2}`)
		_, _ = f.read(conn)
	})
	t.Setenv(leaderSocketEnv, leader.socket)
	steerSession(t, "s-protocol")

	note := SteerTUIStop(repo, steerPayload("s-protocol", false), continuationResult("more work"))
	if !strings.Contains(note, "protocol 2") || !strings.Contains(note, "steer failed") {
		t.Fatalf("note = %q", note)
	}
	state, err := agentsession.LoadSessionState(repo, "s-protocol")
	if err != nil {
		t.Fatal(err)
	}
	if state.GrokSteerAttempts != 0 {
		t.Fatalf("protocol failure must not consume the no-progress budget, got %d", state.GrokSteerAttempts)
	}
}

func recordSuccessfulSteerAttemptForTest(repo, sessionID, reason string) (uint64, bool, error) {
	attempt, allowed, err := prepareSteerAttempt(repo, sessionID, reason)
	if err != nil || !allowed {
		return 0, allowed, err
	}
	attempts, counted, err := commitSteerAttempt(repo, sessionID, attempt)
	return attempts, counted, err
}

func TestSteerTUIStopFallsBackAcrossCandidates(t *testing.T) {
	repo := steerTestRepo(t)
	// Short path: macOS caps sun_path at 104 bytes.
	home, err := os.MkdirTemp("", "grkfb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv(grokHomeEnv, home)

	// leader.sock is dead (bound then closed), leader-live.sock answers.
	dead, err := net.Listen("unix", home+"/leader.sock")
	if err != nil {
		t.Fatal(err)
	}
	_ = dead.Close()
	live := newFakeLeader(t, serveInterject(`{"jsonrpc":"2.0","id":1,"result":{"status":"queued"}}`))
	// Relocate the live leader socket under the Grok home glob.
	liveSocket := home + "/leader-live.sock"
	if err := os.Symlink(live.socket, liveSocket); err != nil {
		t.Skipf("cannot symlink socket: %v", err)
	}

	steerSession(t, "s-fb")
	note := SteerTUIStop(repo, steerPayload("s-fb", false), continuationResult("continue"))
	if !strings.Contains(note, "continuation interjected") {
		t.Fatalf("note = %q", note)
	}
}

func TestSteerTUIStopGivesLaterCandidatesTheirOwnDeadline(t *testing.T) {
	repo := steerTestRepo(t)
	home, err := os.MkdirTemp("", "grkfair")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv(grokHomeEnv, home)

	silent := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {
		_, _ = f.read(conn)
		time.Sleep(2 * time.Second)
	})
	live := newFakeLeader(t, serveInterject(`{"jsonrpc":"2.0","id":1,"result":{"status":"queued"}}`))
	if err := os.Symlink(silent.socket, home+"/leader.sock"); err != nil {
		t.Skipf("cannot symlink socket: %v", err)
	}
	if err := os.Symlink(live.socket, home+"/leader-live.sock"); err != nil {
		t.Skipf("cannot symlink socket: %v", err)
	}

	steerSession(t, "s-fair")
	start := time.Now()
	note := SteerTUIStop(repo, steerPayload("s-fair", false), continuationResult("continue fairly"))
	if !strings.Contains(note, "continuation interjected") {
		t.Fatalf("note = %q", note)
	}
	if elapsed := time.Since(start); elapsed >= steerBudget {
		t.Fatalf("later candidate received no usable budget; elapsed=%s", elapsed)
	}
}
