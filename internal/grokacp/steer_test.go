package grokacp

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func steerTestRepo(t *testing.T) string {
	t.Helper()
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

func TestSteerTUIStopBudgetExhaustion(t *testing.T) {
	repo := steerTestRepo(t)
	leader := newFakeLeader(t, serveInterject(`{"jsonrpc":"2.0","id":1,"result":{"status":"queued"}}`))
	t.Setenv(leaderSocketEnv, leader.socket)

	if _, err := agentsession.MutateSessionState(repo, "s-cap", func(state agentsession.SessionState) agentsession.SessionState {
		state.GrokSteerAttempts = maxStopSteerAttempts
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
	if state.GrokSteerAttempts != 1 {
		t.Fatalf("failed attempts must still count, got %d", state.GrokSteerAttempts)
	}
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
