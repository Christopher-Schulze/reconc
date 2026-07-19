package cli

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestHookRuntimeDevinNativeShapeBlocksDeniedWrite(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"session_id":"devin-1"}`,
		"hook", "runtime", "devin-session-start", repo)

	_, stderr, code := runWithStdin(t,
		`{"session_id":"devin-1","tool_name":"edit","tool_input":{"file_path":"generated/blocked.go"}}`,
		"hook", "runtime", "devin-pre-tool-use", repo)
	if code != 2 || !strings.Contains(stderr, "deny-gen") {
		t.Fatalf("Devin native payload must block denied write, code=%d stderr=%q", code, stderr)
	}
}

func TestHookRuntimeKiloAdapterShapeBlocksDeniedWrite(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	payload := `{"session_id":"kilo-1","reconc_runtime":"kilo","tool_name":"Write","tool_input":{"file_path":"generated/blocked.go"}}`
	_, stderr, code := runWithStdin(t, payload,
		"hook", "runtime", "kilo-pre-tool-use", repo)
	if code != 2 || !strings.Contains(stderr, "deny-gen") {
		t.Fatalf("Kilo adapter payload must block denied write, code=%d stderr=%q", code, stderr)
	}
}

func TestHookRuntimeGrokNativeShapeBlocksDeniedWrite(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	payload := fmt.Sprintf(`{
		"hookEventName":"pre_tool_use",
		"sessionId":"grok-1",
		"workspaceRoot":%q,
		"toolName":"search_replace",
		"toolUseId":"call-1",
		"toolInput":{"path":"generated/blocked.go","old_string":"a","new_string":"b"},
		"toolInputTruncated":false
	}`, repo)
	stdout, stderr, code := runWithStdin(t, payload,
		"hook", "runtime", "grok-pre-tool-use", repo)
	if code != 0 || stderr != "" {
		t.Fatalf("Grok explicit deny transport failed, code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var decision map[string]string
	if err := json.Unmarshal([]byte(stdout), &decision); err != nil {
		t.Fatalf("decode Grok decision: %v\n%s", err, stdout)
	}
	if decision["decision"] != "deny" || !strings.Contains(decision["reason"], "deny-gen") {
		t.Fatalf("Grok denied write payload = %#v", decision)
	}
}

func TestHookRuntimeGrokMalformedPreToolFailsClosedExplicitly(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	stdout, stderr, code := runWithStdin(t,
		fmt.Sprintf(`{"hookEventName":"pre_tool_use","sessionId":"grok-1","workspaceRoot":%q,"toolInputTruncated":true}`, repo),
		"hook", "runtime", "grok-pre-tool-use", repo)
	if code != 0 || stderr != "" {
		t.Fatalf("Grok malformed payload transport failed, code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var decision map[string]string
	if err := json.Unmarshal([]byte(stdout), &decision); err != nil {
		t.Fatalf("decode Grok decision: %v\n%s", err, stdout)
	}
	if decision["decision"] != "deny" || !strings.Contains(decision["reason"], "truncated") {
		t.Fatalf("Grok malformed payload did not fail closed: %#v", decision)
	}
}

func TestHookRuntimeGrokCompatibilityRouteDeduplicatesOnlyWhenNativeInstalled(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	payload := fmt.Sprintf(`{
		"hookEventName":"pre_tool_use",
		"sessionId":"grok-dedup",
		"workspaceRoot":%q,
		"toolName":"search_replace",
		"toolInput":{"path":"generated/blocked.go"}
	}`, repo)
	_, stderr, code := runWithStdin(t, payload,
		"hook", "runtime", "claude-pre-tool-use", repo)
	if code != 2 || !strings.Contains(stderr, "session_id") {
		t.Fatalf("without native Grok hook, the incompatible Claude route must fail visibly: code=%d stderr=%q", code, stderr)
	}
	if _, err := hooks.Install(hooks.KindGrok, repo, false); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runWithStdin(t, payload,
		"hook", "runtime", "claude-pre-tool-use", repo)
	if code != 0 || stdout != "" || !strings.Contains(stderr, "deduplicated") {
		t.Fatalf("native Grok hook must own duplicate route: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	target := filepath.Join(repo, filepath.FromSlash(hooks.GrokHooksPath))
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = runWithStdin(t, payload,
		"hook", "runtime", "claude-pre-tool-use", repo)
	if code != 2 || strings.Contains(stderr, "deduplicated") {
		t.Fatalf("drifted native Grok hook must not suppress compatibility enforcement: code=%d stderr=%q", code, stderr)
	}
}

func TestRepositoryRunControlReturnsContinuationForEveryAgentAdapter(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	writeHookRuntimeTaskFixture(t, repo)
	tests := []struct {
		name    string
		event   string
		payload string
		want    string
	}{
		{name: "Claude Code", event: "claude-stop", payload: `{"session_id":"claude-run"}`, want: `"decision":"block"`},
		{name: "Codex", event: "codex-stop", payload: `{"session_id":"codex-run"}`, want: `"decision":"block"`},
		{name: "Cursor", event: "cursor-stop", payload: fmt.Sprintf(`{"sessionId":"cursor-run","cursor_version":"3.5.17","hook_event_name":"stop","workspace_roots":[%q]}`, repo), want: `"followup_message"`},
		{name: "OpenCode", event: "opencode-stop", payload: `{"session_id":"opencode-run","reconc_runtime":"opencode"}`, want: `"decision":"block"`},
		{name: "Devin CLI", event: "devin-stop", payload: `{"session_id":"devin-run"}`, want: `"decision":"block"`},
		{name: "Antigravity CLI", event: "antigravity-stop", payload: `{"session_id":"antigravity-run"}`, want: `"decision":"continue"`},
		{name: "Kilo", event: "kilo-stop", payload: `{"session_id":"kilo-run","reconc_runtime":"kilo"}`, want: `"decision":"block"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := agentsession.SetRepositoryRun(repo, false); err != nil {
				t.Fatal(err)
			}
			if _, err := agentsession.SetRepositoryRun(repo, true); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, code := runWithStdin(t, test.payload, "hook", "runtime", test.event, repo)
			if code != 0 || stderr != "" {
				t.Fatalf("adapter continuation failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if !strings.Contains(stdout, test.want) || !strings.Contains(stdout, "Reconc run is ON") {
				t.Fatalf("adapter continuation missing: want=%q stdout=%q", test.want, stdout)
			}
		})
	}
}

// e2eFakeGrokLeader serves one leader IPC connection: register, one
// interjection (answered "queued"), disconnect. Returns the socket path and a
// channel delivering the interjected JSON-RPC payload.
func e2eFakeGrokLeader(t *testing.T) (string, <-chan string) {
	t.Helper()
	// Deliberately rooted at /tmp: bootstrapE2ERepo points TMPDIR at a deep
	// per-test dir, and Unix socket paths are capped at ~104 bytes.
	dir, err := os.MkdirTemp("/tmp", "grke2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "leader.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	interjected := make(chan string, 4)
	readFrame := func(conn net.Conn) (map[string]interface{}, error) {
		var lengthPrefix [4]byte
		if _, err := io.ReadFull(conn, lengthPrefix[:]); err != nil {
			return nil, err
		}
		body := make([]byte, binary.BigEndian.Uint32(lengthPrefix[:]))
		if _, err := io.ReadFull(conn, body); err != nil {
			return nil, err
		}
		var message map[string]interface{}
		return message, json.Unmarshal(body, &message)
	}
	writeFrame := func(conn net.Conn, raw string) {
		frame := make([]byte, 4+len(raw))
		binary.BigEndian.PutUint32(frame[:4], uint32(len(raw)))
		copy(frame[4:], raw)
		_, _ = conn.Write(frame)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if _, err := readFrame(conn); err == nil {
				writeFrame(conn, `{"type":"registered","client_id":1,"ready":true,"leader_protocol_version":1}`)
				if message, readErr := readFrame(conn); readErr == nil {
					payload, _ := message["payload"].(string)
					interjected <- payload
					response, _ := json.Marshal(`{"jsonrpc":"2.0","id":1,"result":{"status":"queued"}}`)
					writeFrame(conn, `{"type":"acp","payload":`+string(response)+`}`)
					_, _ = readFrame(conn) // disconnect
				}
			}
			_ = conn.Close()
		}
	}()
	return socket, interjected
}

func TestHookRuntimeGrokStopSteersLeaderContinuation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix fake leader; Windows named-pipe transport is covered in internal/grokacp")
	}
	repo := bootstrapE2ERepo(t)
	writeHookRuntimeTaskFixture(t, repo)
	if _, err := agentsession.SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	socket, interjected := e2eFakeGrokLeader(t)
	t.Setenv("GROK_LEADER_SOCKET", socket)
	t.Setenv("GROK_SESSION_ID", "grok-steer")

	payload := fmt.Sprintf(`{"hookEventName":"stop","sessionId":"grok-steer","workspaceRoot":%q,"reason":"completed"}`, repo)
	stdout, stderr, code := runWithStdin(t, payload, "hook", "runtime", "grok-stop", repo)
	if code != 0 || !strings.Contains(stdout, `"decision":"block"`) || !strings.Contains(stdout, "Reconc run is ON") {
		t.Fatalf("Grok stop wire contract violated: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "continuation interjected (1/32)") {
		t.Fatalf("steering note missing from stderr: %q", stderr)
	}

	select {
	case raw := <-interjected:
		var request struct {
			Method string `json:"method"`
			Params struct {
				SessionID string `json:"sessionId"`
				Text      string `json:"text"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			t.Fatalf("decode interjected payload: %v\n%s", err, raw)
		}
		if request.Method != "_x.ai/interject" || request.Params.SessionID != "grok-steer" {
			t.Fatalf("interjected request = %+v", request)
		}
		if !strings.Contains(request.Params.Text, "Reconc run is ON") {
			t.Fatalf("interjected text must carry the continuation prompt: %q", request.Params.Text)
		}
	default:
		t.Fatal("leader never received the interjection")
	}
}

func TestHookRuntimeGrokRepeatedPolicyBlockStaysStrict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix fake leader; Windows named-pipe transport is covered in internal/grokacp")
	}
	repo := bootstrapE2ERepo(t)
	if _, err := agentsession.MutateSessionState(repo, "grok-strict", func(state agentsession.SessionState) agentsession.SessionState {
		return agentsession.AppendWritePath(state, "generated/out.go")
	}); err != nil {
		t.Fatal(err)
	}
	socket, interjected := e2eFakeGrokLeader(t)
	t.Setenv("GROK_LEADER_SOCKET", socket)
	t.Setenv("GROK_SESSION_ID", "grok-strict")
	payload := fmt.Sprintf(`{"hookEventName":"stop","sessionId":"grok-strict","workspaceRoot":%q,"reason":"completed"}`, repo)

	for attempt := 1; attempt <= 2; attempt++ {
		stdout, stderr, code := runWithStdin(t, payload, "hook", "runtime", "grok-stop", repo)
		if code != 0 || !strings.Contains(stdout, `"decision":"block"`) || !strings.Contains(stdout, "deny-gen") {
			t.Fatalf("strict Grok stop %d failed: code=%d stdout=%q stderr=%q", attempt, code, stdout, stderr)
		}
		want := fmt.Sprintf("continuation interjected (%d/32)", attempt)
		if !strings.Contains(stderr, want) {
			t.Fatalf("strict Grok stop %d missing %q: %q", attempt, want, stderr)
		}
		select {
		case <-interjected:
		default:
			t.Fatalf("strict Grok stop %d did not reach leader", attempt)
		}
	}
}

func TestHookRuntimeGrokStopBlocksWithoutLeader(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	writeHookRuntimeTaskFixture(t, repo)
	if _, err := agentsession.SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GROK_LEADER_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
	t.Setenv("GROK_SESSION_ID", "grok-native")

	payload := fmt.Sprintf(`{"hookEventName":"stop","sessionId":"grok-native","workspaceRoot":%q,"reason":"end_turn","stopHookActive":true}`, repo)
	stdout, stderr, code := runWithStdin(t, payload, "hook", "runtime", "grok-stop", repo)
	if code != 0 || !strings.Contains(stdout, `"decision":"block"`) || !strings.Contains(stdout, "Reconc run is ON") {
		t.Fatalf("native no-leader Grok Stop failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stderr, "interjected") {
		t.Fatalf("no-leader Stop attempted duplicate steering: %q", stderr)
	}
}

func TestHookRuntimeGrokStopInterruptStaysPassive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix fake leader; Windows named-pipe transport is covered in internal/grokacp")
	}
	repo := bootstrapE2ERepo(t)
	writeHookRuntimeTaskFixture(t, repo)
	if _, err := agentsession.SetRepositoryRun(repo, true); err != nil {
		t.Fatal(err)
	}
	socket, interjected := e2eFakeGrokLeader(t)
	t.Setenv("GROK_LEADER_SOCKET", socket)
	t.Setenv("GROK_SESSION_ID", "grok-int")

	payload := fmt.Sprintf(`{"hookEventName":"stop","sessionId":"grok-int","workspaceRoot":%q,"reason":"cancelled"}`, repo)
	stdout, stderr, code := runWithStdin(t, payload, "hook", "runtime", "grok-stop", repo)
	if code != 0 || stdout != "" {
		t.Fatalf("Grok interrupt stop must stay clean: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stderr, "interjected") {
		t.Fatalf("interrupt must never steer: %q", stderr)
	}
	select {
	case raw := <-interjected:
		t.Fatalf("leader must not be contacted on interrupt, got %s", raw)
	default:
	}
}

func TestHookRuntimeDevinPostCompactionReturnsRecoveryPacket(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"session_id":"devin-compact"}`,
		"hook", "runtime", "devin-session-start", repo)

	stdout, stderr, code := runWithStdin(t, `{"session_id":"devin-compact","summary":"provider summary"}`,
		"hook", "runtime", "devin-post-compaction", repo)
	if code != 0 || stderr != "" {
		t.Fatalf("Devin compaction should fail open with clean input, code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "reconc-context-v1") || !strings.Contains(stdout, "additionalContext") {
		t.Fatalf("Devin compaction recovery packet missing: %q", stdout)
	}
}

func TestHookRuntimeClaudeCompactSessionReturnsRecoveryPacket(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, `{"session_id":"claude-compact","source":"startup"}`,
		"hook", "runtime", "claude-session-start", repo)

	stdout, stderr, code := runWithStdin(t, `{"session_id":"claude-compact","source":"compact","compact_summary":"provider summary"}`,
		"hook", "runtime", "claude-post-compaction", repo)
	if code != 0 || stderr != "" {
		t.Fatalf("Claude compact SessionStart should fail open with clean input, code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "reconc-context-v1") || !strings.Contains(stdout, `"hookEventName":"SessionStart"`) {
		t.Fatalf("Claude compaction recovery packet missing or uses the wrong native event: %q", stdout)
	}
}

func TestBoundHookResultKeepsUTF8Valid(t *testing.T) {
	const limit = 64
	result := boundHookResult(
		agentsession.Result{Stderr: strings.Repeat("ä", limit)},
		hooks.RuntimeRoute{MaxOutputBytes: limit, ErrorPolicy: hooks.FailureAllow},
	)
	if !utf8.ValidString(result.Stderr) || !strings.Contains(result.Stderr, "truncated") || len(result.Stderr) > limit/2 {
		t.Fatalf("bounded stderr must remain valid UTF-8: %q", result.Stderr)
	}
}

func TestBoundHookResultCapsCombinedOutput(t *testing.T) {
	const limit = 8 * 1024
	result := boundHookResult(
		agentsession.Result{Stdout: strings.Repeat("o", limit/2), Stderr: strings.Repeat("e", limit)},
		hooks.RuntimeRoute{MaxOutputBytes: limit, ErrorPolicy: hooks.FailureAllow},
	)
	if len(result.Stdout)+len(result.Stderr) > limit {
		t.Fatalf("combined hook output escaped byte budget: stdout=%d stderr=%d", len(result.Stdout), len(result.Stderr))
	}
}

func TestRunHookStatusJSONReportsActivePlugin(t *testing.T) {
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	repo := t.TempDir()
	if _, err := hooks.Install(hooks.KindKilo, repo, false); err != nil {
		t.Fatal(err)
	}
	if err := agentsession.RecordHookLiveness(repo, "kilo-session-start", "kilo-session-start"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "status", repo, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var reports []hooks.PlatformStatus
	if err := json.Unmarshal(stdout.Bytes(), &reports); err != nil {
		t.Fatalf("decode hook status: %v\n%s", err, stdout.String())
	}
	for _, report := range reports {
		if report.Kind == hooks.KindKilo {
			if report.State != hooks.StateConfigured {
				t.Fatalf("Kilo status = %s, want configured: %+v", report.State, report)
			}
			if len(report.LiveEvents) != 1 || report.LiveEvents[0] != "kilo-session-start" || len(report.UnseenEvents) == 0 {
				t.Fatalf("Kilo per-route liveness missing: %+v", report)
			}
			return
		}
	}
	t.Fatal("Kilo status missing")
}

func TestRunBootstrapJSONIncludesActivationTruth(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".devin"), 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(repo, "tools", "reconc", "bin", "hook")
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"bootstrap", repo, "--skip-git-hook", "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("bootstrap: %v stderr=%s", err, stderr.String())
	}
	var payload struct {
		Healthy      bool                   `json:"healthy"`
		HookStatuses []hooks.PlatformStatus `json:"hook_statuses"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode bootstrap JSON: %v\n%s", err, stdout.String())
	}
	if !payload.Healthy {
		t.Fatalf("bootstrap should be healthy: %s", stdout.String())
	}
	for _, report := range payload.HookStatuses {
		if report.Kind == hooks.KindDevinCLI && report.State == hooks.StateConfigured {
			return
		}
	}
	t.Fatalf("bootstrap did not report Devin configured: %s", stdout.String())
}
