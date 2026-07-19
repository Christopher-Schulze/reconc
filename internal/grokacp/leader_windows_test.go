//go:build windows

package grokacp

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	winio "github.com/Microsoft/go-winio"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

type windowsFakeLeader struct {
	endpoint string
	listener net.Listener
	wg       sync.WaitGroup
}

func newWindowsFakeLeader(t *testing.T, handle func(net.Conn)) *windowsFakeLeader {
	t.Helper()
	endpoint := windowsPipeRoot + "grok-leader-reconc-" + strings.ReplaceAll(t.Name(), "/", "-")
	listener, err := winio.ListenPipe(endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	leader := &windowsFakeLeader{endpoint: endpoint, listener: listener}
	leader.wg.Add(1)
	go func() {
		defer leader.wg.Done()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		handle(conn)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		leader.wg.Wait()
	})
	return leader
}

func readWindowsLeaderMessage(conn net.Conn) (leaderClientMessage, error) {
	var lengthPrefix [4]byte
	if _, err := io.ReadFull(conn, lengthPrefix[:]); err != nil {
		return leaderClientMessage{}, err
	}
	body := make([]byte, binary.BigEndian.Uint32(lengthPrefix[:]))
	if _, err := io.ReadFull(conn, body); err != nil {
		return leaderClientMessage{}, err
	}
	var message leaderClientMessage
	return message, json.Unmarshal(body, &message)
}

func writeWindowsLeaderMessage(t *testing.T, conn net.Conn, raw string) {
	t.Helper()
	frame := make([]byte, 4+len(raw))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(raw)))
	copy(frame[4:], raw)
	if err := writeFull(conn, frame); err != nil {
		t.Errorf("write fake leader frame: %v", err)
	}
}

func windowsSteerPayload(sessionID string) []byte {
	body, _ := json.Marshal(map[string]interface{}{
		"session_id":     sessionID,
		"reconc_runtime": "grok",
	})
	return body
}

func windowsContinuationResult(reason string) agentsession.Result {
	body, _ := json.Marshal(map[string]string{"decision": "block", "reason": reason})
	return agentsession.Result{ExitCode: 0, Stdout: string(body)}
}

func TestWindowsLeaderPipeDiscovery(t *testing.T) {
	original := findWindowsLeaderPipes
	findWindowsLeaderPipes = func() ([]string, error) {
		return []string{
			windowsPipeRoot + "grok-leader-bbbb",
			windowsPipeRoot + "grok-leader-aaaa",
		}, nil
	}
	t.Cleanup(func() { findWindowsLeaderPipes = original })
	t.Setenv(leaderSocketEnv, "")

	candidates, err := leaderSocketCandidates()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{windowsPipeRoot + "grok-leader-aaaa", windowsPipeRoot + "grok-leader-bbbb"}
	if strings.Join(candidates, "\n") != strings.Join(want, "\n") {
		t.Fatalf("candidates = %v, want %v", candidates, want)
	}
}

func TestWindowsLeaderPipeEnumerationFindsLivePipe(t *testing.T) {
	leader := newWindowsFakeLeader(t, func(net.Conn) {})
	candidates, err := listWindowsLeaderPipes()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if strings.EqualFold(candidate, leader.endpoint) {
			return
		}
	}
	t.Fatalf("live Grok leader pipe %q missing from %v", leader.endpoint, candidates)
}

func TestWindowsLeaderSteeringUsesNamedPipe(t *testing.T) {
	interjected := make(chan string, 1)
	leader := newWindowsFakeLeader(t, func(conn net.Conn) {
		if _, err := readWindowsLeaderMessage(conn); err != nil {
			return
		}
		writeWindowsLeaderMessage(t, conn, `{"type":"registered","client_id":1,"ready":true,"leader_protocol_version":1}`)
		message, err := readWindowsLeaderMessage(conn)
		if err != nil {
			return
		}
		interjected <- message.Payload
		writeWindowsLeaderMessage(t, conn, `{"type":"acp","payload":"{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"status\":\"queued\"}}"}`)
		_, _ = readWindowsLeaderMessage(conn)
	})

	repo := t.TempDir()
	t.Setenv("RECONC_CLAUDE_STATE_DIR", t.TempDir())
	t.Setenv(leaderSocketEnv, leader.endpoint)
	t.Setenv("GROK_SESSION_ID", "windows-session")
	t.Setenv(SteerEnv, "")
	payload := windowsSteerPayload("windows-session")
	prepared, strict, err := PrepareStrictTUIStop(payload)
	if err != nil || !strict {
		t.Fatalf("strict preparation = strict=%v err=%v", strict, err)
	}
	parsed, err := agentsession.ParsePayload(prepared)
	if err != nil || !parsed.StrictContinuation {
		t.Fatalf("prepared payload = %s err=%v", prepared, err)
	}

	note := SteerTUIStop(repo, prepared, windowsContinuationResult("continue on Windows"))
	if !strings.Contains(note, "continuation interjected (1/32)") {
		t.Fatalf("note = %q", note)
	}
	select {
	case raw := <-interjected:
		if !strings.Contains(raw, interjectMethod) || !strings.Contains(raw, "windows-session") {
			t.Fatalf("interjection = %s", raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("named-pipe leader did not receive interjection")
	}
}

func TestWindowsLeaderProbeVerifiesProtocolAndInterject(t *testing.T) {
	leader := newWindowsFakeLeader(t, func(conn net.Conn) {
		if _, err := readWindowsLeaderMessage(conn); err != nil {
			return
		}
		writeWindowsLeaderMessage(t, conn, `{"type":"registered","client_id":1,"ready":true,"leader_protocol_version":1,"leader_binary_version":"windows-test"}`)
		if _, err := readWindowsLeaderMessage(conn); err != nil {
			return
		}
		writeWindowsLeaderMessage(t, conn, `{"type":"acp","payload":"{\"jsonrpc\":\"2.0\",\"id\":1,\"error\":{\"code\":-32602,\"message\":\"Invalid params\",\"data\":\"session not found\"}}"}`)
		_, _ = readWindowsLeaderMessage(conn)
	})
	t.Setenv(leaderSocketEnv, leader.endpoint)

	probe := ProbeLeaderSteering(2 * time.Second)
	if !probe.Reachable || !probe.Compatible || probe.Endpoint != leader.endpoint {
		t.Fatalf("probe = %+v", probe)
	}
	if probe.ProtocolVersion == nil || *probe.ProtocolVersion != 1 || probe.BinaryVersion != "windows-test" {
		t.Fatalf("probe metadata = %+v", probe)
	}
}
