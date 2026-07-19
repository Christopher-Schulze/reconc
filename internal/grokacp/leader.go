package grokacp

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Grok's leader-follower IPC contract, verified against
// xai-org/grok-build@c68e39f: one leader process per machine listens on a Unix
// socket or Windows named pipe; every frame is a 4-byte big-endian length
// prefix followed by one JSON message. Clients register first, then exchange
// ACP JSON-RPC strings wrapped in {"type":"acp","payload":...} envelopes.
const (
	leaderSocketEnv = "GROK_LEADER_SOCKET"
	grokHomeEnv     = "GROK_HOME"

	leaderProtocolVersion uint32 = 1

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
	Type                  string  `json:"type"`
	Ready                 *bool   `json:"ready,omitempty"`
	LeaderProtocolVersion *uint32 `json:"leader_protocol_version,omitempty"`
	LeaderBinaryVersion   string  `json:"leader_binary_version,omitempty"`
	Payload               string  `json:"payload,omitempty"`
	Code                  int     `json:"code,omitempty"`
	Message               string  `json:"message,omitempty"`
}

type leaderRegistration struct {
	ProtocolVersion *uint32
	BinaryVersion   string
}

type leaderRPCError struct {
	Code    int
	Message string
	Data    json.RawMessage
}

func (e *leaderRPCError) Error() string {
	detail := strings.TrimSpace(e.Message)
	if data := rpcErrorDataText(e.Data); data != "" {
		if detail != "" {
			detail += ": "
		}
		detail += data
	}
	if detail == "" {
		detail = "unknown JSON-RPC error"
	}
	return fmt.Sprintf("%s (code %d)", detail, e.Code)
}

// LeaderProbe is the result of a non-mutating leader compatibility probe.
type LeaderProbe struct {
	// Endpoint is the probed Unix socket or Windows named pipe.
	Endpoint string
	// Reachable reports a completed register handshake.
	Reachable bool
	// Compatible reports the expected protocol version and interject method.
	Compatible bool
	// ProtocolVersion and BinaryVersion come from the registered response.
	ProtocolVersion *uint32
	BinaryVersion   string
	// Detail carries the latest transport or compatibility failure.
	Detail string
}

// ProbeLeaderSteering verifies the registration protocol and _x.ai/interject
// extension. It targets a cryptographically random nonexistent session and
// requires Grok's session-not-found response, so no live session is mutated.
func ProbeLeaderSteering(budget time.Duration) LeaderProbe {
	candidates, err := leaderSocketCandidates()
	if err != nil {
		return LeaderProbe{Detail: fmt.Sprintf("discover Grok leader endpoints: %v", err)}
	}
	if len(candidates) == 0 {
		return LeaderProbe{}
	}
	overallDeadline := time.Now().Add(budget)
	lastProbe := LeaderProbe{Endpoint: candidates[0]}
	var reachableProbe *LeaderProbe
	for index, endpoint := range candidates {
		probe := LeaderProbe{Endpoint: endpoint}
		deadline := fairCandidateDeadline(overallDeadline, len(candidates)-index)
		conn, err := steerDial(endpoint, deadline)
		if err != nil {
			probe.Detail = err.Error()
			lastProbe = probe
			continue
		}
		registration, err := conn.register()
		if err == nil {
			probe.Reachable = true
			probe.ProtocolVersion = registration.ProtocolVersion
			probe.BinaryVersion = registration.BinaryVersion
			err = verifyLeaderCompatibility(conn, registration)
		}
		conn.close()
		if err != nil {
			probe.Detail = err.Error()
			lastProbe = probe
			if probe.Reachable {
				snapshot := probe
				reachableProbe = &snapshot
			}
			continue
		}
		probe.Compatible = true
		probe.Detail = ""
		return probe
	}
	if reachableProbe != nil {
		return *reachableProbe
	}
	return lastProbe
}

type leaderConn struct {
	conn net.Conn
}

// register performs the client handshake and waits until the leader is ready
// to forward ACP traffic.
func (c *leaderConn) register() (leaderRegistration, error) {
	err := c.writeMessage(leaderClientMessage{
		Type:         "register",
		ClientType:   "reconc-hook",
		Mode:         "stdio",
		Capabilities: &leaderClientCapabilities{},
	})
	if err != nil {
		return leaderRegistration{}, fmt.Errorf("register with Grok leader: %w", err)
	}
	var registration leaderRegistration
	ready := false
	registered := false
	for !registered || !ready {
		message, err := c.readMessage()
		if err != nil {
			return leaderRegistration{}, fmt.Errorf("read Grok leader registration: %w", err)
		}
		switch message.Type {
		case "registered":
			registered = true
			registration.ProtocolVersion = message.LeaderProtocolVersion
			registration.BinaryVersion = strings.TrimSpace(message.LeaderBinaryVersion)
			// Leaders that predate the ready field are already initialised.
			ready = message.Ready == nil || *message.Ready
		case "leader_ready":
			ready = true
		case "error":
			return leaderRegistration{}, fmt.Errorf("grok leader rejected registration: %s", message.Message)
		case "shutting_down", "shutdown":
			return leaderRegistration{}, fmt.Errorf("grok leader is shutting down")
		default:
			// Pongs and broadcast ACP traffic may interleave; skip them.
		}
	}
	return registration, nil
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
				Error  *leaderRPCError `json:"error"`
			}
			if json.Unmarshal([]byte(message.Payload), &response) != nil ||
				response.ID == nil || *response.ID != 1 {
				// Broadcast notifications and foreign responses interleave.
				continue
			}
			if response.Error != nil {
				return fmt.Errorf("grok leader rejected interjection: %w", response.Error)
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
	return writeFull(c.conn, frame)
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

func verifyLeaderCompatibility(conn *leaderConn, registration leaderRegistration) error {
	if err := validateLeaderProtocol(registration); err != nil {
		return err
	}
	sessionID, err := randomProbeSessionID()
	if err != nil {
		return err
	}
	err = conn.interject(sessionID, "reconc leader compatibility probe")
	if err == nil {
		return fmt.Errorf("%s unexpectedly accepted a nonexistent probe session", interjectMethod)
	}
	var rpcErr *leaderRPCError
	if !errors.As(err, &rpcErr) {
		return fmt.Errorf("%s compatibility probe failed: %w", interjectMethod, err)
	}
	if rpcErr.Code != -32602 || !strings.Contains(strings.ToLower(rpcErr.Error()), "session not found") {
		return fmt.Errorf("%s compatibility probe returned %w", interjectMethod, rpcErr)
	}
	return nil
}

func validateLeaderProtocol(registration leaderRegistration) error {
	if registration.ProtocolVersion == nil {
		return fmt.Errorf("grok leader registration omitted protocol version")
	}
	if *registration.ProtocolVersion != leaderProtocolVersion {
		return fmt.Errorf("grok leader protocol %d is incompatible with supported protocol %d", *registration.ProtocolVersion, leaderProtocolVersion)
	}
	return nil
}

func randomProbeSessionID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate Grok leader probe session: %w", err)
	}
	return "reconc-doctor-probe-" + hex.EncodeToString(value[:]), nil
}

func rpcErrorDataText(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	return string(raw)
}

func writeFull(writer io.Writer, body []byte) error {
	for len(body) > 0 {
		written, err := writer.Write(body)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		if written > len(body) {
			return io.ErrShortWrite
		}
		body = body[written:]
	}
	return nil
}

func fairCandidateDeadline(overallDeadline time.Time, remainingCandidates int) time.Time {
	if remainingCandidates <= 1 {
		return overallDeadline
	}
	remaining := time.Until(overallDeadline)
	if remaining <= 0 {
		return time.Now()
	}
	return time.Now().Add(remaining / time.Duration(remainingCandidates))
}
