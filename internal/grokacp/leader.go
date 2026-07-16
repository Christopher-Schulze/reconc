package grokacp

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Grok's leader-follower IPC contract, verified against
// xai-org/grok-build@c68e39f: one leader process per machine listens on a Unix
// socket; every frame is a 4-byte big-endian length prefix followed by one
// JSON message. Clients register first, then exchange ACP JSON-RPC strings
// wrapped in {"type":"acp","payload":...} envelopes.
const (
	leaderSocketEnv = "GROK_LEADER_SOCKET"
	grokHomeEnv     = "GROK_HOME"

	// Grok caps frames at 64 MiB; every message this client exchanges is a
	// few hundred bytes, so a much smaller read cap bounds memory while
	// still tolerating interleaved broadcast traffic.
	maxLeaderFrameBytes = 16 << 20

	interjectMethod = "_x.ai/interject"
)

type leaderClientMessage struct {
	Type         string                    `json:"type"`
	ClientType   string                    `json:"client_type,omitempty"`
	Mode         string                    `json:"mode,omitempty"`
	Capabilities *leaderClientCapabilities `json:"capabilities,omitempty"`
	Payload      string                    `json:"payload,omitempty"`
}

// leaderClientCapabilities mirrors Grok's ClientCapabilities. Every advertised
// capability stays false: this client never creates sessions and must not
// change approval modes or terminal/filesystem routing for anyone.
type leaderClientCapabilities struct {
	YoloMode bool `json:"yolo_mode"`
	AutoMode bool `json:"auto_mode"`
	Terminal bool `json:"terminal"`
	FsRead   bool `json:"fs_read"`
	FsWrite  bool `json:"fs_write"`
}

type leaderServerMessage struct {
	Type    string `json:"type"`
	Ready   *bool  `json:"ready,omitempty"`
	Payload string `json:"payload,omitempty"`
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// leaderSocketCandidates lists the sockets a running Grok leader may be bound
// to, most specific first. Hooks dispatched by a leader-hosted session inherit
// the leader's environment, so GROK_LEADER_SOCKET is authoritative when set;
// otherwise every leader<suffix>.sock under the Grok home is a candidate
// (non-default relay URLs derive suffixed socket names).
func leaderSocketCandidates() []string {
	if override := strings.TrimSpace(os.Getenv(leaderSocketEnv)); override != "" {
		if socketExists(override) {
			return []string{override}
		}
		return nil
	}
	home := strings.TrimSpace(os.Getenv(grokHomeEnv))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		home = filepath.Join(userHome, ".grok")
	}
	matches, err := filepath.Glob(filepath.Join(home, "leader*.sock"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	defaultSocket := filepath.Join(home, "leader.sock")
	candidates := make([]string, 0, len(matches))
	for _, match := range matches {
		if match == defaultSocket && socketExists(match) {
			candidates = append([]string{match}, candidates...)
			continue
		}
		if socketExists(match) {
			candidates = append(candidates, match)
		}
	}
	return candidates
}

// socketExists reports whether path exists as a Unix socket (symlinks are
// followed). On Windows Grok uses named pipes that never appear on the
// filesystem, so discovery degrades to "no leader" there and steering stays
// passive by construction.
func socketExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

// LeaderProbe is the result of a read-only leader reachability probe.
type LeaderProbe struct {
	// SocketPath is the probed socket, or "" when none was found.
	SocketPath string
	// Reachable reports a completed register handshake.
	Reachable bool
	// Detail carries the failure reason when a socket exists but the
	// handshake failed.
	Detail string
}

// ProbeLeaderSteering checks whether a Grok leader is reachable, i.e. whether
// TUI stop steering can act. It registers and immediately disconnects; it
// never spawns a leader and never touches sessions.
func ProbeLeaderSteering(budget time.Duration) LeaderProbe {
	candidates := leaderSocketCandidates()
	if len(candidates) == 0 {
		return LeaderProbe{}
	}
	deadline := time.Now().Add(budget)
	probe := LeaderProbe{SocketPath: candidates[0]}
	for _, socketPath := range candidates {
		probe.SocketPath = socketPath
		conn, err := steerDial(socketPath, deadline)
		if err != nil {
			probe.Detail = err.Error()
			continue
		}
		err = conn.register()
		conn.close()
		if err != nil {
			probe.Detail = err.Error()
			continue
		}
		probe.Reachable = true
		probe.Detail = ""
		return probe
	}
	return probe
}

type leaderConn struct {
	conn net.Conn
}

// dialLeader connects to one leader socket and applies deadline to every
// subsequent read and write on the connection.
func dialLeader(socketPath string, deadline time.Time) (*leaderConn, error) {
	dialer := net.Dialer{Deadline: deadline}
	conn, err := dialer.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial Grok leader %s: %w", socketPath, err)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("set Grok leader deadline: %w", err)
	}
	return &leaderConn{conn: conn}, nil
}

// register performs the client handshake and waits until the leader is ready
// to forward ACP traffic.
func (c *leaderConn) register() error {
	err := c.writeMessage(leaderClientMessage{
		Type:         "register",
		ClientType:   "reconc-hook",
		Mode:         "stdio",
		Capabilities: &leaderClientCapabilities{},
	})
	if err != nil {
		return fmt.Errorf("register with Grok leader: %w", err)
	}
	ready := false
	registered := false
	for !registered || !ready {
		message, err := c.readMessage()
		if err != nil {
			return fmt.Errorf("read Grok leader registration: %w", err)
		}
		switch message.Type {
		case "registered":
			registered = true
			// Leaders that predate the ready field are already initialised.
			ready = message.Ready == nil || *message.Ready
		case "leader_ready":
			ready = true
		case "error":
			return fmt.Errorf("grok leader rejected registration: %s", message.Message)
		case "shutting_down", "shutdown":
			return fmt.Errorf("grok leader is shutting down")
		default:
			// Pongs and broadcast ACP traffic may interleave; skip them.
		}
	}
	return nil
}

// interject queues text as a mid-turn interjection into sessionID. Grok merges
// it into a running turn, or converts it into an immediately-started prompt
// turn when the session is idle, which is exactly the state after a Stop hook.
func (c *leaderConn) interject(sessionID, text string) error {
	request, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  interjectMethod,
		"params": map[string]string{
			"sessionId": sessionID,
			"text":      text,
		},
	})
	if err != nil {
		return fmt.Errorf("encode Grok interject request: %w", err)
	}
	if err := c.writeMessage(leaderClientMessage{Type: "acp", Payload: string(request)}); err != nil {
		return fmt.Errorf("send Grok interject request: %w", err)
	}
	for {
		message, err := c.readMessage()
		if err != nil {
			return fmt.Errorf("read Grok interject response: %w", err)
		}
		switch message.Type {
		case "acp":
			var response struct {
				ID     *int64          `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
					Data    string `json:"data"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(message.Payload), &response) != nil ||
				response.ID == nil || *response.ID != 1 {
				// Broadcast notifications and foreign responses interleave.
				continue
			}
			if response.Error != nil {
				detail := response.Error.Message
				if response.Error.Data != "" {
					detail += ": " + response.Error.Data
				}
				return fmt.Errorf("grok leader rejected interjection: %s", detail)
			}
			return nil
		case "error":
			return fmt.Errorf("grok leader error: %s", message.Message)
		case "shutting_down", "shutdown":
			return fmt.Errorf("grok leader is shutting down")
		default:
			// Skip pongs and other envelope types.
		}
	}
}

// close sends a best-effort disconnect and closes the socket.
func (c *leaderConn) close() {
	_ = c.writeMessage(leaderClientMessage{Type: "disconnect"})
	_ = c.conn.Close()
}

func (c *leaderConn) writeMessage(message leaderClientMessage) error {
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if len(body) > maxLeaderFrameBytes {
		return fmt.Errorf("leader frame exceeds %d bytes", maxLeaderFrameBytes)
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	_, err = c.conn.Write(frame)
	return err
}

func (c *leaderConn) readMessage() (leaderServerMessage, error) {
	var lengthPrefix [4]byte
	if _, err := io.ReadFull(c.conn, lengthPrefix[:]); err != nil {
		return leaderServerMessage{}, err
	}
	length := binary.BigEndian.Uint32(lengthPrefix[:])
	if length > maxLeaderFrameBytes {
		return leaderServerMessage{}, fmt.Errorf("leader frame of %d bytes exceeds %d-byte cap", length, maxLeaderFrameBytes)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return leaderServerMessage{}, err
	}
	var message leaderServerMessage
	if err := json.Unmarshal(body, &message); err != nil {
		return leaderServerMessage{}, fmt.Errorf("decode leader message: %w", err)
	}
	return message, nil
}
