//go:build !windows

package grokacp

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeLeader is a scripted Grok leader IPC server bound to a real Unix
// socket. Each accepted connection is served by handle.
type fakeLeader struct {
	t        *testing.T
	socket   string
	listener net.Listener
	wg       sync.WaitGroup

	mu       sync.Mutex
	received []leaderClientMessage
}

func newFakeLeader(t *testing.T, handle func(*fakeLeader, net.Conn)) *fakeLeader {
	t.Helper()
	dir, err := os.MkdirTemp("", "grk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "leader.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	leader := &fakeLeader{t: t, socket: socket, listener: listener}
	leader.wg.Add(1)
	go func() {
		defer leader.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			handle(leader, conn)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		leader.wg.Wait()
	})
	return leader
}

func (f *fakeLeader) read(conn net.Conn) (leaderClientMessage, error) {
	var lengthPrefix [4]byte
	if _, err := io.ReadFull(conn, lengthPrefix[:]); err != nil {
		return leaderClientMessage{}, err
	}
	body := make([]byte, binary.BigEndian.Uint32(lengthPrefix[:]))
	if _, err := io.ReadFull(conn, body); err != nil {
		return leaderClientMessage{}, err
	}
	var message leaderClientMessage
	if err := json.Unmarshal(body, &message); err != nil {
		return leaderClientMessage{}, err
	}
	f.mu.Lock()
	f.received = append(f.received, message)
	f.mu.Unlock()
	return message, nil
}

func (f *fakeLeader) write(conn net.Conn, raw string) {
	frame := make([]byte, 4+len(raw))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(raw)))
	copy(frame[4:], raw)
	if _, err := conn.Write(frame); err != nil {
		f.t.Logf("fake leader write: %v", err)
	}
}

func (f *fakeLeader) messages() []leaderClientMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]leaderClientMessage(nil), f.received...)
}

// serveInterject registers the client and answers one interjection with the
// given JSON-RPC response body (a raw payload string).
func serveInterject(response string, before ...string) func(*fakeLeader, net.Conn) {
	return func(f *fakeLeader, conn net.Conn) {
		if _, err := f.read(conn); err != nil {
			return
		}
		f.write(conn, `{"type":"registered","client_id":7,"ready":true,"leader_protocol_version":1,"leader_binary_version":"test"}`)
		if _, err := f.read(conn); err != nil {
			return
		}
		for _, noise := range before {
			f.write(conn, noise)
		}
		f.write(conn, `{"type":"acp","payload":`+encodeJSONString(response)+`}`)
		_, _ = f.read(conn) // disconnect
	}
}

func encodeJSONString(raw string) string {
	body, _ := json.Marshal(raw)
	return string(body)
}

func TestLeaderInterjectHappyPath(t *testing.T) {
	leader := newFakeLeader(t, serveInterject(`{"jsonrpc":"2.0","id":1,"result":{"status":"queued"}}`))

	conn, err := dialLeader(leader.socket, time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.close()
	if _, err := conn.register(); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := conn.interject("session-1", "keep going"); err != nil {
		t.Fatalf("interject: %v", err)
	}
	conn.close()

	messages := leader.messages()
	if len(messages) < 2 {
		t.Fatalf("leader saw %d messages, want register+acp", len(messages))
	}
	register := messages[0]
	if register.Type != "register" || register.ClientType != "reconc-hook" || register.Mode != "stdio" {
		t.Fatalf("register envelope = %+v", register)
	}
	if register.Capabilities == nil || register.Capabilities.YoloMode || register.Capabilities.Terminal {
		t.Fatalf("capabilities must be present and all-false: %+v", register.Capabilities)
	}
	var request struct {
		Method string `json:"method"`
		Params struct {
			SessionID string `json:"sessionId"`
			Text      string `json:"text"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(messages[1].Payload), &request); err != nil {
		t.Fatalf("decode interject payload: %v", err)
	}
	if request.Method != "_x.ai/interject" || request.Params.SessionID != "session-1" || request.Params.Text != "keep going" {
		t.Fatalf("interject request = %+v", request)
	}
}

func TestLeaderRegisterWaitsForLeaderReady(t *testing.T) {
	leader := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {
		if _, err := f.read(conn); err != nil {
			return
		}
		f.write(conn, `{"type":"registered","client_id":7,"ready":false}`)
		f.write(conn, `{"type":"pong"}`)
		f.write(conn, `{"type":"leader_ready"}`)
		if _, err := f.read(conn); err != nil {
			return
		}
		f.write(conn, `{"type":"acp","payload":`+encodeJSONString(`{"jsonrpc":"2.0","id":1,"result":{"status":"queued"}}`)+`}`)
	})

	conn, err := dialLeader(leader.socket, time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.close()
	if _, err := conn.register(); err != nil {
		t.Fatalf("register must wait for leader_ready: %v", err)
	}
	if err := conn.interject("session-1", "go"); err != nil {
		t.Fatalf("interject: %v", err)
	}
}

func TestLeaderInterjectSkipsBroadcastNoise(t *testing.T) {
	leader := newFakeLeader(t, serveInterject(
		`{"jsonrpc":"2.0","id":1,"result":{"status":"queued"}}`,
		`{"type":"pong"}`,
		`{"type":"acp","payload":"{\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{}}"}`,
		`{"type":"acp","payload":"{\"jsonrpc\":\"2.0\",\"id\":99,\"result\":{}}"}`,
	))

	conn, err := dialLeader(leader.socket, time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.close()
	if _, err := conn.register(); err != nil {
		t.Fatal(err)
	}
	if err := conn.interject("session-1", "go"); err != nil {
		t.Fatalf("interject must skip unrelated frames: %v", err)
	}
}

func TestLeaderInterjectSurfacesJSONRPCError(t *testing.T) {
	leader := newFakeLeader(t, serveInterject(
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"Invalid params","data":"session not found: session-1"}}`,
	))

	conn, err := dialLeader(leader.socket, time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.close()
	if _, err := conn.register(); err != nil {
		t.Fatal(err)
	}
	err = conn.interject("session-1", "go")
	if err == nil || !strings.Contains(err.Error(), "session not found") {
		t.Fatalf("interject error = %v, want session-not-found detail", err)
	}
}

func TestLeaderRegisterRejectionAndShutdownFail(t *testing.T) {
	tests := []struct {
		name  string
		frame string
		want  string
	}{
		{name: "error", frame: `{"type":"error","code":1,"message":"nope"}`, want: "nope"},
		{name: "shutdown", frame: `{"type":"shutting_down","reason":"manual","delay_ms":0}`, want: "shutting down"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			leader := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {
				if _, err := f.read(conn); err != nil {
					return
				}
				f.write(conn, test.frame)
			})
			conn, err := dialLeader(leader.socket, time.Now().Add(2*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.close()
			_, err = conn.register()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("register error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLeaderReadRejectsOversizedFrame(t *testing.T) {
	leader := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {
		if _, err := f.read(conn); err != nil {
			return
		}
		var lengthPrefix [4]byte
		binary.BigEndian.PutUint32(lengthPrefix[:], maxLeaderFrameBytes+1)
		_, _ = conn.Write(lengthPrefix[:])
	})
	conn, err := dialLeader(leader.socket, time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.close()
	_, err = conn.register()
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized frame must fail, got %v", err)
	}
}

func TestLeaderDeadlineBoundsSilentServer(t *testing.T) {
	leader := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {
		_, _ = f.read(conn)
		time.Sleep(2 * time.Second) // never answers
	})
	conn, err := dialLeader(leader.socket, time.Now().Add(150*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.close()
	start := time.Now()
	if _, err := conn.register(); err == nil {
		t.Fatal("register against a silent leader must time out")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("deadline not enforced, register took %s", elapsed)
	}
}

func TestLeaderSocketCandidatesEnvOverride(t *testing.T) {
	leader := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {})
	t.Setenv(leaderSocketEnv, leader.socket)
	candidates, err := leaderSocketCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0] != leader.socket {
		t.Fatalf("override candidates = %v", candidates)
	}

	t.Setenv(leaderSocketEnv, filepath.Join(t.TempDir(), "missing.sock"))
	if candidates, err := leaderSocketCandidates(); err != nil || candidates != nil {
		t.Fatalf("missing override socket must yield none, got %v", candidates)
	}
}

func TestLeaderSocketCandidatesGrokHomeGlob(t *testing.T) {
	dir, err := os.MkdirTemp("", "grkh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv(leaderSocketEnv, "")
	t.Setenv(grokHomeEnv, dir)

	if candidates, err := leaderSocketCandidates(); err != nil || len(candidates) != 0 {
		t.Fatalf("empty home must yield no candidates, got %v", candidates)
	}

	// A plain file named like a socket must not count.
	if err := os.WriteFile(filepath.Join(dir, "leader-aaaa.sock"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	suffixed, err := net.Listen("unix", filepath.Join(dir, "leader-zzzz.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer suffixed.Close()
	defaultListener, err := net.Listen("unix", filepath.Join(dir, "leader.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer defaultListener.Close()

	candidates, err := leaderSocketCandidates()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "leader.sock"), filepath.Join(dir, "leader-zzzz.sock")}
	if fmt.Sprint(candidates) != fmt.Sprint(want) {
		t.Fatalf("candidates = %v, want default first then suffixed: %v", candidates, want)
	}
}

func TestLeaderSocketCandidatesReportsUnreadableHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(home, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(leaderSocketEnv, "")
	t.Setenv(grokHomeEnv, home)
	if _, err := leaderSocketCandidates(); err == nil {
		t.Fatal("leader discovery error was hidden")
	}
	probe := ProbeLeaderSteering(time.Second)
	if !strings.Contains(probe.Detail, "discover Grok leader endpoints") {
		t.Fatalf("probe did not surface discovery failure: %+v", probe)
	}
}

func TestProbeLeaderSteering(t *testing.T) {
	t.Run("no endpoint", func(t *testing.T) {
		t.Setenv(leaderSocketEnv, "")
		t.Setenv(grokHomeEnv, t.TempDir())
		probe := ProbeLeaderSteering(time.Second)
		if probe.Endpoint != "" || probe.Reachable || probe.Compatible {
			t.Fatalf("probe without endpoint = %+v", probe)
		}
	})
	t.Run("compatible", func(t *testing.T) {
		leader := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {
			if _, err := f.read(conn); err != nil {
				return
			}
			f.write(conn, `{"type":"registered","client_id":1,"ready":true,"leader_protocol_version":1,"leader_binary_version":"0.2.101"}`)
			if _, err := f.read(conn); err != nil {
				return
			}
			f.write(conn, `{"type":"acp","payload":`+encodeJSONString(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"Invalid params","data":"session not found"}}`)+`}`)
			_, _ = f.read(conn) // disconnect
		})
		t.Setenv(leaderSocketEnv, leader.socket)
		probe := ProbeLeaderSteering(time.Second)
		if !probe.Reachable || !probe.Compatible || probe.Endpoint != leader.socket || probe.Detail != "" {
			t.Fatalf("probe = %+v", probe)
		}
		if probe.ProtocolVersion == nil || *probe.ProtocolVersion != 1 || probe.BinaryVersion != "0.2.101" {
			t.Fatalf("probe metadata = %+v", probe)
		}
	})
	t.Run("handshake failure", func(t *testing.T) {
		leader := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {
			if _, err := f.read(conn); err != nil {
				return
			}
			f.write(conn, `{"type":"error","code":9,"message":"protocol mismatch"}`)
		})
		t.Setenv(leaderSocketEnv, leader.socket)
		probe := ProbeLeaderSteering(time.Second)
		if probe.Reachable || !strings.Contains(probe.Detail, "protocol mismatch") {
			t.Fatalf("probe = %+v", probe)
		}
	})
	t.Run("protocol mismatch", func(t *testing.T) {
		leader := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {
			if _, err := f.read(conn); err != nil {
				return
			}
			f.write(conn, `{"type":"registered","client_id":1,"ready":true,"leader_protocol_version":2}`)
			_, _ = f.read(conn)
		})
		t.Setenv(leaderSocketEnv, leader.socket)
		probe := ProbeLeaderSteering(time.Second)
		if !probe.Reachable || probe.Compatible || !strings.Contains(probe.Detail, "protocol 2") {
			t.Fatalf("probe = %+v", probe)
		}
	})
	t.Run("interject method missing", func(t *testing.T) {
		leader := newFakeLeader(t, func(f *fakeLeader, conn net.Conn) {
			if _, err := f.read(conn); err != nil {
				return
			}
			f.write(conn, `{"type":"registered","client_id":1,"ready":true,"leader_protocol_version":1}`)
			if _, err := f.read(conn); err != nil {
				return
			}
			f.write(conn, `{"type":"acp","payload":`+encodeJSONString(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}`)+`}`)
			_, _ = f.read(conn)
		})
		t.Setenv(leaderSocketEnv, leader.socket)
		probe := ProbeLeaderSteering(time.Second)
		if !probe.Reachable || probe.Compatible || !strings.Contains(probe.Detail, interjectMethod) {
			t.Fatalf("probe = %+v", probe)
		}
	})
}

type shortWriteConn struct {
	net.Conn
	maxBytes int
}

func (c shortWriteConn) Write(body []byte) (int, error) {
	if len(body) > c.maxBytes {
		body = body[:c.maxBytes]
	}
	return c.Conn.Write(body)
}

func TestLeaderWriteMessageCompletesShortWrites(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn := &leaderConn{conn: shortWriteConn{Conn: client, maxBytes: 3}}
	read := make(chan leaderClientMessage, 1)
	go func() {
		var lengthPrefix [4]byte
		if _, err := io.ReadFull(server, lengthPrefix[:]); err != nil {
			return
		}
		body := make([]byte, binary.BigEndian.Uint32(lengthPrefix[:]))
		if _, err := io.ReadFull(server, body); err != nil {
			return
		}
		var message leaderClientMessage
		if json.Unmarshal(body, &message) == nil {
			read <- message
		}
	}()
	if err := conn.writeMessage(leaderClientMessage{Type: "disconnect"}); err != nil {
		t.Fatalf("writeMessage: %v", err)
	}
	select {
	case message := <-read:
		if message.Type != "disconnect" {
			t.Fatalf("message = %+v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("short-write frame was not completed")
	}
}

func TestFairCandidateDeadlineReservesTimeForLaterLeaders(t *testing.T) {
	start := time.Now()
	overall := start.Add(300 * time.Millisecond)
	first := fairCandidateDeadline(overall, 3)
	firstShare := first.Sub(start)
	if firstShare < 75*time.Millisecond || firstShare > 150*time.Millisecond {
		t.Fatalf("first candidate share = %s, want about one third", firstShare)
	}
	if final := fairCandidateDeadline(overall, 1); !final.Equal(overall) {
		t.Fatalf("final candidate deadline = %s, want %s", final, overall)
	}
}
