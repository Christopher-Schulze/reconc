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
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	reconbootstrap "reconc.dev/reconc/internal/bootstrap"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestHookRuntimeDevinNativeShapeBlocksDeniedWrite(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	_, _, _ = runWithStdin(t, fmt.Sprintf(`{"hook_event_name":"SessionStart","session_id":"devin-1","cwd":%q}`, repo),
		"hook", "runtime", "devin-session-start", repo)

	_, stderr, code := runWithStdin(t,
		fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"devin-1","cwd":%q,"tool_name":"edit","tool_input":{"file_path":"generated/blocked.go"}}`, repo),
		"hook", "runtime", "devin-pre-tool-use", repo)
	if code != 2 || !strings.Contains(stderr, "deny-gen") {
		t.Fatalf("Devin native payload must block denied write, code=%d stderr=%q", code, stderr)
	}
}

func TestHookRuntimeOMPNativeShapeBlocksDeniedWrite(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	payload := fmt.Sprintf(`{
		"hook_event_name":"tool_call",
		"session_id":"omp-1",
		"cwd":%q,
		"tool_name":"write",
		"tool_input":{"path":"generated/blocked.go"},
		"tool_call_id":"call-1"
	}`, repo)
	_, stderr, code := runWithStdin(t, payload,
		"hook", "runtime", "omp-pre-tool-use", repo)
	if code != 2 || !strings.Contains(stderr, "deny-gen") {
		t.Fatalf("OMP native payload must block denied write, code=%d stderr=%q", code, stderr)
	}
}

func TestHookRuntimePiNativeShapeBlocksDeniedWrite(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	payload := fmt.Sprintf(`{
		"hook_event_name":"tool_call",
		"session_id":"pi-1",
		"cwd":%q,
		"tool_name":"write",
		"tool_input":{"path":"generated/blocked.go"},
		"tool_call_id":"call-1"
	}`, repo)
	_, stderr, code := runWithStdin(t, payload,
		"hook", "runtime", "pi-pre-tool-use", repo)
	if code != 2 || !strings.Contains(stderr, "deny-gen") {
		t.Fatalf("Pi native payload must block denied write, code=%d stderr=%q", code, stderr)
	}
}

func TestHookRuntimeZCodeNativeShapeBlocksDeniedWrite(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	payload := fmt.Sprintf(`{
		"hook_event_name":"PreToolUse",
		"session_id":"zcode-1",
		"cwd":%q,
		"tool_name":"Write",
		"tool_input":{"file_path":"generated/blocked.go"},
		"tool_use_id":"call-1"
	}`, repo)
	_, stderr, code := runWithStdin(t, payload,
		"hook", "runtime", "zcode-pre-tool-use", repo)
	if code != 2 || !strings.Contains(stderr, "deny-gen") {
		t.Fatalf("ZCode native payload must block denied write, code=%d stderr=%q", code, stderr)
	}
}

func TestHookRuntimeZCodePermissionRequestUsesNativeDenyShape(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	payload := fmt.Sprintf(`{
		"hook_event_name":"PermissionRequest",
		"session_id":"zcode-permission-1",
		"cwd":%q,
		"tool_name":"Write",
		"tool_input":{"file_path":"generated/blocked.go"}
	}`, repo)
	stdout, stderr, code := runWithStdin(t, payload,
		"hook", "runtime", "zcode-permission-request", repo)
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"hookEventName":"PermissionRequest"`) ||
		!strings.Contains(stdout, `"behavior":"deny"`) || !strings.Contains(stdout, "deny-gen") {
		t.Fatalf("ZCode PermissionRequest must return native denial, code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestHookRuntimeDevinUserPromptSubmitCreatesSession(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	stdout, stderr, code := runWithStdin(t,
		fmt.Sprintf(`{"hook_event_name":"UserPromptSubmit","session_id":"devin-prompt","cwd":%q,"prompt":"continue the task"}`, repo),
		"hook", "runtime", "devin-user-prompt-submit", repo)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Devin UserPromptSubmit failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := agentsession.LoadSessionState(repo, "devin-prompt"); err != nil {
		t.Fatalf("Devin UserPromptSubmit did not establish session state: %v", err)
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

func TestHookRuntimeGitHubCopilotCompatibilityShapeBlocksDeniedWrite(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	payload := fmt.Sprintf(`{
		"hook_event_name":"PreToolUse",
		"session_id":"copilot-1",
		"cwd":%q,
		"tool_name":"Edit",
		"tool_input":{"file_path":"generated/blocked.go"}
	}`, repo)
	stdout, stderr, code := runWithStdin(t, payload,
		"hook", "runtime", "copilot-pre-tool-use", repo)
	if code != 0 || stderr != "" {
		t.Fatalf("Copilot explicit deny transport failed, code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var decision map[string]string
	if err := json.Unmarshal([]byte(stdout), &decision); err != nil {
		t.Fatalf("decode Copilot decision: %v\n%s", err, stdout)
	}
	if decision["permissionDecision"] != "deny" || !strings.Contains(decision["permissionDecisionReason"], "deny-gen") {
		t.Fatalf("Copilot denied write payload = %#v", decision)
	}
}

func TestHookRuntimeGitHubCopilotMalformedStopBlocksExplicitly(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	stdout, stderr, code := runWithStdin(t,
		fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"copilot-stop","cwd":%q}`, t.TempDir()),
		"hook", "runtime", "copilot-stop", repo)
	if code != 0 || stderr != "" {
		t.Fatalf("Copilot malformed Stop transport failed, code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var decision map[string]string
	if err := json.Unmarshal([]byte(stdout), &decision); err != nil {
		t.Fatalf("decode Copilot Stop decision: %v\n%s", err, stdout)
	}
	if decision["decision"] != "block" || decision["reason"] != "Reconc could not safely validate the hook payload for GitHub Copilot." {
		t.Fatalf("Copilot malformed Stop did not fail closed: %#v", decision)
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
	if decision["decision"] != "deny" || decision["reason"] != "Reconc could not safely validate the hook payload for Grok." {
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
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(hooks.WrapperPath))); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = runWithStdin(t, payload,
		"hook", "runtime", "claude-pre-tool-use", repo)
	if code != 2 || strings.Contains(stderr, "deduplicated") {
		t.Fatalf("Grok without an executable wrapper suppressed compatibility enforcement: code=%d stderr=%q", code, stderr)
	}
	if _, err := hooks.Install(hooks.KindGrok, repo, false); err != nil {
		t.Fatal(err)
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
		{name: "GitHub Copilot", event: "copilot-stop", payload: fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"copilot-run","cwd":%q,"stop_hook_active":false}`, repo), want: `"decision":"block"`},
		{name: "GitHub Copilot subagent", event: "copilot-subagent-stop", payload: fmt.Sprintf(`{"hook_event_name":"SubagentStop","session_id":"copilot-subagent-run","cwd":%q,"agent_name":"research","stop_reason":"end_turn"}`, repo), want: `"decision":"block"`},
		{name: "Cursor", event: "cursor-stop", payload: fmt.Sprintf(`{"sessionId":"cursor-run","cursor_version":"3.5.17","hook_event_name":"stop","workspace_roots":[%q]}`, repo), want: `"followup_message"`},
		{name: "OpenCode", event: "opencode-stop", payload: `{"session_id":"opencode-run","reconc_runtime":"opencode"}`, want: `"decision":"block"`},
		{name: "Devin CLI", event: "devin-stop", payload: fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"devin-run","cwd":%q}`, repo), want: `"decision":"block"`},
		{name: "Antigravity CLI", event: "antigravity-stop", payload: `{"session_id":"antigravity-run"}`, want: `"decision":"continue"`},
		{name: "Kilo", event: "kilo-stop", payload: `{"session_id":"kilo-run","reconc_runtime":"kilo"}`, want: `"decision":"block"`},
		{name: "Oh My Pi", event: "omp-stop", payload: fmt.Sprintf(`{"hook_event_name":"session_stop","session_id":"omp-run","cwd":%q,"stop_hook_active":false}`, repo), want: `"decision":"block"`},
		{name: "Pi", event: "pi-stop", payload: fmt.Sprintf(`{"hook_event_name":"agent_settled","session_id":"pi-run","cwd":%q,"stop_hook_active":false}`, repo), want: `"decision":"block"`},
		{name: "ZCode", event: "zcode-stop", payload: fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"zcode-run","cwd":%q,"stop_hook_active":false}`, repo), want: `"decision":"block"`},
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
	// The fake leader exercises Reconc's compatibility interjection path.
	// Isolate the native-capability probe from any real Grok installation on
	// the test host so this E2E contract stays deterministic.
	t.Setenv("GROK_HOME", t.TempDir())
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

func TestHookRuntimeGrokStopEmitsBlockWithoutLeader(t *testing.T) {
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
		t.Fatalf("no-leader Grok Stop output failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
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
	_, _, _ = runWithStdin(t, fmt.Sprintf(`{"hook_event_name":"SessionStart","session_id":"devin-compact","cwd":%q}`, repo),
		"hook", "runtime", "devin-session-start", repo)

	stdout, stderr, code := runWithStdin(t, fmt.Sprintf(`{"hook_event_name":"PostCompaction","session_id":"devin-compact","cwd":%q,"summary":"provider summary"}`, repo),
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
		"hook", "runtime", "claude-compaction-recovery", repo)
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

// TestBoundHookResultPreservesFailClosedControlEnvelope drives the shipped
// output bound for every platform whose deny/block decision travels in stdout.
// Clearing stdout there hands the host an undecided non-zero exit, and on
// GitHub Copilot the non-zero exit also re-triggers the installed shell
// fallback, so the decision must survive the byte budget as exit-0 JSON.
func TestBoundHookResultPreservesFailClosedControlEnvelope(t *testing.T) {
	const limit = 8 * 1024
	cases := []struct {
		name     string
		kind     string
		event    hooks.Event
		wantKeys map[string]interface{}
	}{
		{
			name: "cursor stop", kind: hooks.KindCursor, event: hooks.EventStop,
			wantKeys: map[string]interface{}{"continue": false},
		},
		{
			name: "cursor pre tool use", kind: hooks.KindCursor, event: hooks.EventPreToolUse,
			wantKeys: map[string]interface{}{"permission": "deny"},
		},
		{
			name: "copilot pre tool use", kind: hooks.KindGitHubCopilot, event: hooks.EventPreToolUse,
			wantKeys: map[string]interface{}{"permissionDecision": "deny"},
		},
		{
			name: "copilot permission request", kind: hooks.KindGitHubCopilot, event: hooks.EventPermissionRequest,
			wantKeys: map[string]interface{}{"behavior": "deny"},
		},
		{
			name: "copilot stop", kind: hooks.KindGitHubCopilot, event: hooks.EventStop,
			wantKeys: map[string]interface{}{"decision": "block"},
		},
		{
			name: "grok pre tool use", kind: hooks.KindGrok, event: hooks.EventPreToolUse,
			wantKeys: map[string]interface{}{"decision": "deny"},
		},
		{
			name: "grok stop", kind: hooks.KindGrok, event: hooks.EventStop,
			wantKeys: map[string]interface{}{"decision": "block"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := boundHookResult(
				agentsession.Result{ExitCode: 0, Stdout: strings.Repeat("x", limit)},
				hooks.RuntimeRoute{
					PlatformKind: tc.kind, Event: tc.event,
					MaxOutputBytes: limit, ErrorPolicy: hooks.FailureBlock,
				},
			)
			if result.ExitCode != 0 {
				t.Fatalf("decision-carrying platforms must keep the adapter exit code 0, got %d", result.ExitCode)
			}
			var envelope map[string]interface{}
			if err := json.Unmarshal([]byte(result.Stdout), &envelope); err != nil {
				t.Fatalf("fail-closed stdout must stay valid JSON: %v (%q)", err, result.Stdout)
			}
			for key, want := range tc.wantKeys {
				if envelope[key] != want {
					t.Fatalf("envelope %v is missing %s=%v", envelope, key, want)
				}
			}
			if len(result.Stdout)+len(result.Stderr) > limit {
				t.Fatalf("fail-closed envelope escaped budget: stdout=%d stderr=%d", len(result.Stdout), len(result.Stderr))
			}
		})
	}
}

// TestBoundHookResultUsesExitCodeWhereTheDecisionLivesThere covers the other
// half of the contract: platforms that read the exit code keep the historical
// empty-stdout shape, and a budget too small for any envelope degrades to that
// same fail-closed shape instead of emitting truncated JSON.
func TestBoundHookResultUsesExitCodeWhereTheDecisionLivesThere(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		limit int
	}{
		{name: "claude code", kind: hooks.KindClaudeCode, limit: 8 * 1024},
		{name: "codex", kind: hooks.KindCodex, limit: 8 * 1024},
		{name: "budget below any envelope", kind: hooks.KindGrok, limit: 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := boundHookResult(
				agentsession.Result{ExitCode: 0, Stdout: strings.Repeat("x", tc.limit*2)},
				hooks.RuntimeRoute{
					PlatformKind: tc.kind, Event: hooks.EventStop,
					MaxOutputBytes: tc.limit, ErrorPolicy: hooks.FailureBlock,
				},
			)
			if result.ExitCode != 2 {
				t.Fatalf("exit-code platforms must fail closed with exit 2, got %d", result.ExitCode)
			}
			if result.Stdout != "" {
				t.Fatalf("exit-code platforms must not invent a stdout envelope: %q", result.Stdout)
			}
			if len(result.Stderr) > tc.limit {
				t.Fatalf("diagnostic escaped byte budget: %d", len(result.Stderr))
			}
		})
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

func TestRunHookStatusJSONSeparatesGitHubCopilotConfigurationAndLiveness(t *testing.T) {
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	repo := t.TempDir()
	if _, err := hooks.Install(hooks.KindGitHubCopilot, repo, false); err != nil {
		t.Fatal(err)
	}
	if err := agentsession.RecordHookLiveness(repo, "copilot-session-start", "copilot-session-start"); err != nil {
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
		if report.Kind != hooks.KindGitHubCopilot {
			continue
		}
		if report.State != hooks.StateConfigured || !report.Generated || !report.Installed || !report.Executable || !report.Configured || !report.Live {
			t.Fatalf("GitHub Copilot status facts = %+v", report)
		}
		if len(report.LiveEvents) != 1 || report.LiveEvents[0] != "copilot-session-start" || len(report.UnseenEvents) == 0 {
			t.Fatalf("GitHub Copilot route liveness = %+v", report)
		}
		return
	}
	t.Fatal("GitHub Copilot status missing")
}

func TestRunHookStatusJSONReportsCursorSurfaceEvents(t *testing.T) {
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	repo := t.TempDir()
	if _, err := hooks.Install(hooks.KindCursor, repo, false); err != nil {
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
		if report.Kind != hooks.KindCursor {
			continue
		}
		want := []string{
			"cursor-session-start",
			"cursor-user-prompt-submit",
			"cursor-pre-tool-use",
			"cursor-post-tool-use",
			"cursor-stop",
			"cursor-session-end",
			"cursor-workspace-open",
		}
		if !slices.Equal(report.SurfaceEvents[hooks.HostSurfaceCursorCLIInteractive], want) {
			t.Fatalf("Cursor CLI surface events = %v, want %v", report.SurfaceEvents[hooks.HostSurfaceCursorCLIInteractive], want)
		}
		return
	}
	t.Fatal("Cursor status missing")
}

func TestHookStatusTextHidesUnseenEnumerationButJSONKeepsIt(t *testing.T) {
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	repo := t.TempDir()
	var textOut bytes.Buffer
	if err := runHookStatus([]string{repo}, &textOut); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(textOut.String(), "; unseen ") {
		t.Fatalf("human status enumerated unseen routes: %s", textOut.String())
	}
	var jsonOut bytes.Buffer
	if err := runHookStatus([]string{repo, "--json"}, &jsonOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut.String(), `"unseen_events"`) {
		t.Fatalf("JSON status lost unseen route evidence: %s", jsonOut.String())
	}
}

func TestRunBootstrapJSONIncludesActivationTruth(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
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
	if err := Run([]string{"init", repo, "--profile", "minimal", "--hook", hooks.KindDevinCLI, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("init: %v stderr=%s", err, stderr.String())
	}
	var payload reconbootstrap.InitReport
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode init JSON: %v\n%s", err, stdout.String())
	}
	if payload.Status != reconbootstrap.InitComplete {
		t.Fatalf("init should be complete: %s", stdout.String())
	}
	for _, check := range payload.Checks {
		if check.Name == "hook:"+hooks.KindDevinCLI && check.Status == "PASS" {
			return
		}
	}
	t.Fatalf("init did not report Devin configured: %s", stdout.String())
}

// TestBoundHookResultKeepsTheRuntimeReasonOnOversize covers what an operator
// actually reads. The oversized stream is stdout, so a bounded stderr still
// carries why the decision was made; replacing it with the byte-budget notice
// alone would report a symptom instead of a cause.
func TestBoundHookResultKeepsTheRuntimeReasonOnOversize(t *testing.T) {
	const limit = 8 * 1024
	const reason = "reconc blocked this Stop: TASK 143 has unmet acceptance criteria"
	cases := []struct {
		name  string
		route hooks.RuntimeRoute
	}{
		{
			name: "decision carried in json",
			route: hooks.RuntimeRoute{
				PlatformKind: hooks.KindGrok, Event: hooks.EventStop,
				MaxOutputBytes: limit, ErrorPolicy: hooks.FailureBlock,
			},
		},
		{
			name: "decision carried in the exit code",
			route: hooks.RuntimeRoute{
				PlatformKind: hooks.KindClaudeCode, Event: hooks.EventStop,
				MaxOutputBytes: limit, ErrorPolicy: hooks.FailureBlock,
			},
		},
		{
			name: "fail-open route",
			route: hooks.RuntimeRoute{
				PlatformKind: hooks.KindClaudeCode, Event: hooks.EventPostToolUse,
				MaxOutputBytes: limit, ErrorPolicy: hooks.FailureAllow,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := boundHookResult(
				agentsession.Result{Stdout: strings.Repeat("x", limit), Stderr: reason},
				tc.route,
			)
			if !strings.Contains(result.Stderr, "TASK 143 has unmet acceptance criteria") {
				t.Fatalf("the runtime reason was discarded: %q", result.Stderr)
			}
			if !strings.Contains(result.Stderr, "exceeded the platform byte budget") {
				t.Fatalf("the operator is not told output was dropped: %q", result.Stderr)
			}
			if len(result.Stdout)+len(result.Stderr) > limit {
				t.Fatalf("combined output escaped the budget: stdout=%d stderr=%d", len(result.Stdout), len(result.Stderr))
			}
		})
	}

	// Without a reason the notice stands alone rather than emitting an empty
	// diagnostic.
	result := boundHookResult(
		agentsession.Result{Stdout: strings.Repeat("x", limit)},
		hooks.RuntimeRoute{PlatformKind: hooks.KindClaudeCode, Event: hooks.EventStop, MaxOutputBytes: limit, ErrorPolicy: hooks.FailureBlock},
	)
	if result.Stderr != "reconc hook output exceeded the platform byte budget" {
		t.Fatalf("empty stderr diagnostic = %q", result.Stderr)
	}

	// A budget too small for reason plus marker still stays inside the budget.
	tight := boundHookResult(
		agentsession.Result{Stdout: strings.Repeat("x", 64), Stderr: reason},
		hooks.RuntimeRoute{PlatformKind: hooks.KindClaudeCode, Event: hooks.EventStop, MaxOutputBytes: 32, ErrorPolicy: hooks.FailureBlock},
	)
	if len(tight.Stdout)+len(tight.Stderr) > 32 {
		t.Fatalf("tight budget escaped: stdout=%d stderr=%d", len(tight.Stdout), len(tight.Stderr))
	}
}
