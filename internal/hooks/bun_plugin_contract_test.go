package hooks

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bunDriverBudget bounds each Bun contract driver. A driver that keeps a live
// child would otherwise consume the whole package timeout and leave the worker
// process behind, turning one stuck run into a twenty-minute stall.
//
// This bounds a hang; it is not a performance assertion. Every driver step
// spawns the hook binary, and process creation on Windows costs an order of
// magnitude more than on Unix, so the same driver that finishes in forty
// seconds on macOS runs well past a minute and a half there. The budget stays
// far below the stall it exists to prevent while leaving that difference room.
const bunDriverBudget = 6 * time.Minute

type bunHookRecord struct {
	event   string
	payload map[string]interface{}
}

func TestGeneratedBunPluginsPreserveHostContracts(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("Bun is required to verify generated OpenCode and Kilo plugins: %v", err)
	}

	for _, kind := range []string{KindOpenCode, KindKilo} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			repo := t.TempDir()
			artifact, err := Generate(kind)
			if err != nil {
				t.Fatalf("generate %s plugin: %v", kind, err)
			}
			pluginPath := filepath.Join(repo, filepath.FromSlash(artifact.TargetPath))
			if err := os.MkdirAll(filepath.Dir(pluginPath), 0o755); err != nil {
				t.Fatalf("create plugin directory: %v", err)
			}
			if err := os.WriteFile(pluginPath, []byte(artifact.Content), 0o644); err != nil {
				t.Fatalf("write plugin: %v", err)
			}

			logPath := filepath.Join(repo, "hook-records.jsonl")
			wrapperPath := filepath.Join(repo, "tools", "reconc", "bin", "hook")
			if err := os.MkdirAll(filepath.Dir(wrapperPath), 0o755); err != nil {
				t.Fatalf("create wrapper directory: %v", err)
			}
			wrapper := `#!/bin/sh
set -eu
event="$1"
payload="$(cat)"
printf '%s\t%s\n' "$event" "$payload" >> "$RECONC_TEST_LOG"
case "$event" in
  *-permission-request) printf '%s\n' '{"permissionDecision":"deny"}' ;;
  *-pre-compaction) printf '%s\n' '{"hookSpecificOutput":{"additionalContext":"recovery-packet"}}' ;;
esac
`
			if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
				t.Fatalf("write hook wrapper: %v", err)
			}

			driverPath := filepath.Join(repo, "plugin-contract.js")
			driver := `const pluginPath = Bun.argv[2]
const kind = Bun.argv[3]
const pluginModule = await import("file://" + pluginPath + "?contract=" + Date.now())
if (kind === "opencode" && "default" in pluginModule) throw new Error("OpenCode plugin must not export a default descriptor")
if (kind === "kilo" && (pluginModule.default?.id !== "reconc" || typeof pluginModule.default?.server !== "function")) {
  throw new Error("Kilo plugin descriptor is invalid")
}
const factory = kind === "opencode" ? pluginModule.ReconcOpenCodePlugin : pluginModule.default.server
if (typeof factory !== "function") throw new Error("plugin factory missing")
const hooks = await factory({
  directory: Bun.argv[4],
  worktree: Bun.argv[4],
  client: { session: { prompt: async () => {} } },
})
const sessionID = "ses_contract"
await hooks["chat.message"](
  { sessionID, messageID: "msg_user" },
  { parts: [{ type: "text", text: "ship it" }] },
)
const shellCases = [
  ["exit-zero", { title: "completed", output: "actual-output", metadata: { exit: 0 } }],
  ["successful-stderr", { title: "completed", output: "warning on stderr", metadata: { exit: 0 } }],
  ["exit-one", { title: "failed", output: "failed", metadata: { exit: 1 } }],
  ["exit-two", { title: "failed", output: "failed", metadata: { exit: 2 } }],
  ["exit-126", { title: "failed", output: "failed", metadata: { exit: 126 } }],
  ["exit-127", { title: "failed", output: "failed", metadata: { exit: 127 } }],
  ["negative-exit", { title: "failed", output: "signal", metadata: { exit: -1 } }],
  ["timeout", { title: "timed out", output: "timeout diagnostic", metadata: { exit: null } }],
  ["abort", { title: "aborted", output: "abort diagnostic", metadata: { exit: null } }],
  ["missing-exit", { title: "unknown", output: "unknown", metadata: {} }],
  ["fractional-exit", { title: "invalid", output: "invalid", metadata: { exit: 1.5 } }],
  ["huge-exit", { title: "invalid", output: "invalid", metadata: { exit: Number.MAX_SAFE_INTEGER + 1 } }],
  ["numeric-string", { title: "invalid", output: "invalid", metadata: { exit: "1" } }],
  ["null-metadata", { title: "invalid", output: "invalid", metadata: null }],
  ["string-metadata", { title: "invalid", output: "invalid", metadata: "exit=0" }],
  ["array-metadata", { title: "invalid", output: "invalid", metadata: [{ exit: 0 }] }],
  ["inherited-exit", { title: "invalid", output: "invalid", metadata: Object.create({ exit: 0 }) }],
  ["explicit-error", { title: "failed", output: "failed", error: "host failure", metadata: { exit: 0 } }],
  ["conflicting-exit", { title: "invalid", output: "invalid", exitCode: 1, metadata: { exit: 0 } }],
]
for (const [name, output] of shellCases) {
  await hooks["tool.execute.after"](
    { sessionID, tool: name === "exit-zero" ? "shell" : "bash", callID: "call_" + name, args: { command: name } },
    output,
  )
}
for (const tool of ["read", "write", "custom", "mcp_fetch", "task"]) {
  await hooks["tool.execute.after"](
    { sessionID, tool, callID: "call_" + tool, args: { file_path: "src/app.go" } },
    { title: tool, output: "completed", metadata: { host: "preserved" } },
  )
}
const permissionOutput = { status: "ask" }
await hooks["permission.ask"](
  { sessionID, type: "bash", title: "rm protected", pattern: ["protected.txt"] },
  permissionOutput,
)
if (permissionOutput.status !== "deny") throw new Error("permission denial was not applied")
const compactionOutput = { context: [] }
await hooks["experimental.session.compacting"]({ sessionID }, compactionOutput)
if (!compactionOutput.context.includes("recovery-packet")) throw new Error("compaction context was not applied")
const failureEvent = {
  type: "message.part.updated",
  properties: {
    part: {
      type: "tool",
      sessionID,
      messageID: "msg_tool",
      callID: "call_failed",
      tool: "bash",
      state: {
        status: "error",
        input: { command: "false" },
        error: "exit status 1",
        metadata: { exitCode: 1 },
      },
    },
  },
}
await hooks.event({ event: failureEvent })
await hooks.event({ event: failureEvent })
`
			if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
				t.Fatalf("write Bun contract driver: %v", err)
			}

			runBunContractDriver(t, []string{"RECONC_TEST_LOG=" + logPath}, bun, driverPath, pluginPath, kind, repo)

			records := readBunHookRecords(t, logPath)
			prefix := kind
			assertBunHookCount(t, records, prefix+"-session-start", 1)
			assertBunHookCount(t, records, prefix+"-user-prompt-submit", 1)
			assertBunHookCount(t, records, prefix+"-post-tool-use", 24)
			assertBunHookCount(t, records, prefix+"-permission-request", 1)
			assertBunHookCount(t, records, prefix+"-pre-compaction", 1)
			assertBunHookCount(t, records, prefix+"-post-tool-use-failure", 1)

			posts := bunHookPayloads(records, prefix+"-post-tool-use")
			response := posts[0]["tool_response"].(map[string]interface{})
			if response["title"] != "completed" || response["output"] != "actual-output" {
				t.Fatalf("%s post-tool payload lost host output: %#v", kind, response)
			}
			metadata := response["metadata"].(map[string]interface{})
			if metadata["exit"] != float64(0) || response["exit_code"] != float64(0) || response["success"] != true {
				t.Fatalf("%s post-tool payload lost metadata: %#v", kind, metadata)
			}
			for index, post := range posts[:19] {
				response := post["tool_response"].(map[string]interface{})
				wantSuccess := index < 2
				if response["success"] != wantSuccess {
					t.Fatalf("%s shell case %d success = %#v, want %v; response=%#v", kind, index, response["success"], wantSuccess, response)
				}
				mcp := post["reconc_mcp"].(map[string]interface{})
				wantOutcome := "failure"
				if wantSuccess {
					wantOutcome = "success"
				}
				if mcp["outcome"] != wantOutcome {
					t.Fatalf("%s shell case %d MCP outcome = %#v, want %s", kind, index, mcp["outcome"], wantOutcome)
				}
				if wantSuccess {
					if response["exit_code"] != float64(0) || response["error"] != nil {
						t.Fatalf("%s successful shell case %d malformed: %#v", kind, index, response)
					}
				} else if response["error"] == nil && response["exit_code"] == float64(0) {
					t.Fatalf("%s failed shell case %d can appear successful: %#v", kind, index, response)
				}
			}
			for index, post := range posts[19:] {
				response := post["tool_response"].(map[string]interface{})
				if _, present := response["exit_code"]; present {
					t.Fatalf("%s non-shell case %d gained command exit status: %#v", kind, index, response)
				}
				if _, present := response["success"]; present {
					t.Fatalf("%s non-shell case %d gained command success state: %#v", kind, index, response)
				}
				metadata := response["metadata"].(map[string]interface{})
				if metadata["host"] != "preserved" {
					t.Fatalf("%s non-shell case %d lost metadata: %#v", kind, index, response)
				}
			}

			failure := bunHookPayload(t, records, prefix+"-post-tool-use-failure")
			if failure["error"] != "exit status 1" {
				t.Fatalf("%s failure payload lost error: %#v", kind, failure)
			}
			input := failure["tool_input"].(map[string]interface{})
			if input["command"] != "false" {
				t.Fatalf("%s failure payload lost input: %#v", kind, input)
			}
		})
	}
}

func TestGeneratedBunPluginTransportIsCombinedAndBounded(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("Bun is required to verify generated plugin transport: %v", err)
	}
	for _, kind := range []string{KindOpenCode, KindKilo} {
		for _, mode := range []string{"large", "invalid-utf8", "timeout", "spawn-failure"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				repo := t.TempDir()
				artifact, err := Generate(kind)
				if err != nil {
					t.Fatal(err)
				}
				content := artifact.Content
				if mode == "timeout" {
					prefix := kind + `-pre-tool-use":{"timeoutMilliseconds":10000`
					replacement := kind + `-pre-tool-use":{"timeoutMilliseconds":50`
					if !strings.Contains(content, prefix) {
						t.Fatalf("pre-tool timeout contract missing from %s plugin", kind)
					}
					content = strings.Replace(content, prefix, replacement, 1)
				}
				pluginPath := filepath.Join(repo, filepath.FromSlash(artifact.TargetPath))
				if err := os.MkdirAll(filepath.Dir(pluginPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(pluginPath, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
				if mode != "spawn-failure" {
					wrapperPath := filepath.Join(repo, "tools", "reconc", "bin", "hook")
					if err := os.MkdirAll(filepath.Dir(wrapperPath), 0o755); err != nil {
						t.Fatal(err)
					}
					wrapper := `#!/bin/sh
event="$1"
case "$event" in
  *-session-start) exit 0 ;;
  *-pre-tool-use)
    case "$RECONC_TRANSPORT_MODE" in
      large)
        # One writer exercises both pipes without racing two process creations on Windows.
        perl -e 'print "o" x 1048576; print STDERR "e" x 1048576'
        exit 1
        ;;
      invalid-utf8)
        printf '\377\376broken\n' >&2
        exit 0
        ;;
      timeout)
        exec sleep 2
        ;;
    esac
    ;;
esac
exit 0
`
					if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				driverPath := filepath.Join(repo, "transport-contract.js")
				driver := `const pluginPath = Bun.argv[2]
const kind = Bun.argv[3]
const mode = Bun.argv[4]
if (mode === "spawn-failure") process.env.PATH = ""
const pluginModule = await import("file://" + pluginPath + "?transport=" + Date.now())
const factory = kind === "opencode" ? pluginModule.ReconcOpenCodePlugin : pluginModule.default.server
const hooks = await factory({ directory: Bun.argv[5], worktree: Bun.argv[5], client: {} })
const started = Date.now()
let failure = ""
try {
  await hooks["tool.execute.before"](
    { sessionID: "ses_transport", tool: "shell", callID: "call_transport", args: { command: "false" } },
    {},
  )
} catch (error) {
  failure = String(error?.message || error)
}
if (!failure) throw new Error("blocking transport failure was not surfaced")
const size = new TextEncoder().encode(failure).length
if (size > 8192) throw new Error("combined output exceeded route budget: " + size)
if (mode === "large" && !failure.includes("[reconc output truncated]")) throw new Error("large output has no truncation marker")
if (mode === "invalid-utf8" && !failure.includes("[reconc invalid UTF-8 output]")) throw new Error("invalid UTF-8 was not rejected")
if (mode === "timeout" && Date.now() - started > 1500) throw new Error("timeout did not kill the child promptly")
// The plugin owns a session worker for as long as a session is open. Ending
// the session releases it; without this the driver exits its own work and then
// waits on a live child forever.
if (typeof hooks.event === "function") {
  try {
    await hooks.event({ event: { type: "session.deleted", properties: { info: { id: "ses_transport" } } } })
  } catch {}
}
`
				if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
					t.Fatal(err)
				}
				runBunContractDriver(t, []string{"RECONC_TRANSPORT_MODE=" + mode}, bun, driverPath, pluginPath, kind, mode, repo)
			})
		}
	}
}

func TestGeneratedBunPluginsUseBoundedAsyncIdleContinuation(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("Bun is required to verify generated idle continuation: %v", err)
	}
	for _, kind := range []string{KindOpenCode, KindKilo} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			repo := t.TempDir()
			artifact, err := Generate(kind)
			if err != nil {
				t.Fatal(err)
			}
			content := artifact.Content
			stopBudget := kind + `-stop":{"timeoutMilliseconds":30000`
			if !strings.Contains(content, stopBudget) {
				t.Fatalf("%s Stop budget missing", kind)
			}
			if !strings.Contains(content, "const maxContinuationSessions = 1024") {
				t.Fatalf("%s continuation capacity contract missing", kind)
			}
			content = strings.Replace(content, stopBudget, kind+`-stop":{"timeoutMilliseconds":5000`, 1)
			content = strings.Replace(content, "const maxContinuationSessions = 1024", "const maxContinuationSessions = 4", 1)
			pluginPath := filepath.Join(repo, filepath.FromSlash(artifact.TargetPath))
			if err := os.MkdirAll(filepath.Dir(pluginPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(pluginPath, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(repo, "idle-records.jsonl")
			modePath := filepath.Join(repo, "idle-mode")
			wrapperPath := filepath.Join(repo, "tools", "reconc", "bin", "hook")
			if err := os.MkdirAll(filepath.Dir(wrapperPath), 0o755); err != nil {
				t.Fatal(err)
			}
			wrapper := `#!/bin/sh
set -eu
event="$1"
payload="$(cat)"
printf '%s\t%s\n' "$event" "$payload" >> "$RECONC_TEST_LOG"
mode="$(cat "$RECONC_IDLE_MODE" 2>/dev/null || true)"
case "$event:$mode" in
  *-stop:reason) printf '%s\n' '{"reason":"continue safely"}' ;;
  *-stop:invalid) printf '%s\n' 'not-json' ;;
  *-stop:nonzero) exit 1 ;;
  *-stop:truncated) perl -e 'print "{\"reason\":\"", "x" x 20000, "\"}\n"' ;;
  *-stop:timeout) sleep 30 ;;
  *-stop:*) printf '%s\n' '{}' ;;
esac
`
			if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
				t.Fatal(err)
			}
			driverPath := filepath.Join(repo, "idle-contract.js")
			driver := `const pluginPath = Bun.argv[2]
const kind = Bun.argv[3]
const repo = Bun.argv[4]
const modePath = Bun.argv[5]
let nonce = 0
let syncCalls = 0
const accepted = () => ({ response: { ok: true, status: 204 } })
const load = async (session, promptAsync, includeSync = true) => {
  const module = await import("file://" + pluginPath + "?idle=" + (++nonce))
  const factory = kind === "opencode" ? module.ReconcOpenCodePlugin : module.default.server
  const client = { session: {} }
  if (promptAsync) client.session.promptAsync = promptAsync
  if (includeSync) client.session.prompt = async () => { syncCalls += 1 }
  const hooks = await factory({ directory: repo, worktree: repo, client })
  return { hooks, session }
}
const mode = async (value) => Bun.write(modePath, value)
const idle = (target) => target.hooks.event({ event: { type: "session.idle", properties: { sessionID: target.session } } })
const activity = (target) => target.hooks["tool.execute.before"](
  { sessionID: target.session, tool: "read", callID: "activity-" + nonce, args: { file_path: "src/app.go" } },
  {},
)

await mode("empty")
let calls = []
let target = await load("ses_empty", async (request) => { calls.push(request); return accepted() })
await idle(target)
if (calls.length !== 0) throw new Error("empty Stop reason submitted a continuation")

await mode("reason")
calls = []
target = await load("ses_accept", async (request) => { calls.push(request); return accepted() })
await target.hooks["chat.message"]({ sessionID: target.session }, { parts: [{ type: "text", text: "external" }] })
await idle(target)
if (calls.length !== 1) throw new Error("accepted continuation count " + calls.length)
const request = calls[0]
if (request.sessionID !== "ses_accept" ||
    !/^msg_reconc_[0-9a-f]{32}$/.test(request.messageID) ||
    JSON.stringify(request.parts) !== JSON.stringify([{ type: "text", text: "continue safely" }]) ||
    Object.keys(request).sort().join(",") !== "messageID,parts,sessionID") {
  throw new Error("async request shape drift: " + JSON.stringify(request))
}
await idle(target)
if (calls.length !== 1) throw new Error("repeated idle duplicated continuation")
await target.hooks["chat.message"](
  { sessionID: target.session, messageID: request.messageID },
  { parts: [{ type: "text", text: "continue safely" }] },
)
await idle(target)
if (calls.length !== 1) throw new Error("injected message reopened the continuation generation")
await target.hooks["chat.message"](
  { sessionID: target.session, messageID: "msg_external" },
  { parts: [{ type: "text", text: "external follow-up" }] },
)
await idle(target)
if (calls.length !== 2) throw new Error("external message did not open a new generation")
const delayedInjectedMessageID = calls[1].messageID
await activity(target)
await idle(target)
if (calls.length !== 3) throw new Error("real tool activity did not open a new generation")
await target.hooks["chat.message"](
  { sessionID: target.session, messageID: delayedInjectedMessageID },
  { parts: [{ type: "text", text: "continue safely" }] },
)
await idle(target)
if (calls.length !== 3) throw new Error("delayed injected message reopened the continuation generation")
await target.hooks["chat.message"](
  { sessionID: target.session, messageID: calls[2].messageID },
  { parts: [{ type: "text", text: "continue safely" }] },
)
await idle(target)
if (calls.length !== 3) throw new Error("latest injected message reopened the continuation generation")

let release
calls = []
target = await load("ses_parallel", async (request) => {
  calls.push(request)
  return await new Promise((resolve) => { release = () => resolve(accepted()) })
})
await activity(target)
const firstIdle = idle(target)
while (calls.length === 0) await Bun.sleep(1)
await Promise.race([
  idle(target),
  Bun.sleep(250).then(() => { throw new Error("re-entrant idle waited on in-flight submission") }),
])
if (calls.length !== 1) throw new Error("re-entrant idle started another request")
release()
await firstIdle

target = await load("ses_unavailable", undefined, true)
await activity(target)
await idle(target)

target = await load("ses_rejected", async () => { throw new Error("credential-bearing transport failure") })
await activity(target)
await idle(target)

target = await load("ses_malformed", async () => ({}))
await activity(target)
await idle(target)

target = await load("ses_closed", async () => ({
  data: undefined,
  error: { name: "NotFoundError" },
  response: { ok: false, status: 404 },
}))
await activity(target)
await idle(target)

for (const failureMode of ["invalid", "nonzero", "truncated", "timeout"]) {
  await mode(failureMode)
  target = await load("ses_" + failureMode, async () => accepted())
  await activity(target)
  await idle(target)
}

await mode("reason")
calls = []
target = await load("ses_deleted", async (request) => { calls.push(request); return accepted() })
await activity(target)
await idle(target)
await target.hooks.event({ event: { type: "session.deleted", properties: { sessionID: target.session } } })
await idle(target)
if (calls.length !== 2) throw new Error("session deletion did not clear continuation state")

calls = []
target = await load("ses_limit", async (request) => { calls.push(request); return accepted() })
for (let index = 0; index < 11; index += 1) {
  await activity(target)
  await idle(target)
}
if (calls.length !== 10) throw new Error("continuation limit accepted " + calls.length + " requests")

await mode("empty")
target = await load("ses_capacity_0", async () => accepted())
for (let index = 0; index < 5; index += 1) {
  await target.hooks.event({ event: { type: "session.idle", properties: { sessionID: "ses_capacity_" + index } } })
}
if (syncCalls !== 0) throw new Error("synchronous prompt fallback was called")
`
			if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
				t.Fatal(err)
			}
			runBunContractDriver(t, []string{"RECONC_TEST_LOG=" + logPath, "RECONC_IDLE_MODE=" + modePath},
				bun, driverPath, pluginPath, kind, repo, modePath)
			records := readBunHookRecords(t, logPath)
			assertBunHookCount(t, records, kind+"-continuation-accepted", 16)
			failedPayloads := bunHookPayloads(records, kind+"-continuation-failed")
			if got := len(failedPayloads); got != 7 {
				t.Fatalf("%s failed continuation diagnostics = %d, want 7", kind, got)
			}
			expectedFailedSessions := map[string]bool{
				"ses_rejected":  false,
				"ses_malformed": false,
				"ses_closed":    false,
				"ses_invalid":   false,
				"ses_nonzero":   false,
				"ses_truncated": false,
				"ses_timeout":   false,
			}
			for _, payload := range failedPayloads {
				sessionID, ok := payload["session_id"].(string)
				if !ok {
					t.Fatalf("%s failed continuation payload has no session_id: %#v", kind, payload)
				}
				seen, expected := expectedFailedSessions[sessionID]
				if !expected || seen {
					t.Fatalf("%s unexpected or duplicate failed continuation session %q", kind, sessionID)
				}
				expectedFailedSessions[sessionID] = true
			}
			assertBunHookCount(t, records, kind+"-continuation-unavailable", 1)
			if got := len(bunHookPayloads(records, kind+"-continuation-suppressed")); got < 2 {
				t.Fatalf("%s suppressed continuation diagnostics = %d, want at least 2", kind, got)
			}
			for _, event := range []string{
				kind + "-continuation-accepted",
				kind + "-continuation-failed",
				kind + "-continuation-unavailable",
				kind + "-continuation-suppressed",
			} {
				for _, payload := range bunHookPayloads(records, event) {
					body, err := json.Marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(body), "credential-bearing") || strings.Contains(string(body), "continue safely") {
						t.Fatalf("%s leaked prompt or transport error: %s", event, body)
					}
				}
			}
		})
	}
}

func readBunHookRecords(t *testing.T, path string) []bunHookRecord {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read hook records: %v", err)
	}
	var records []bunHookRecord
	for lineNumber, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		event, payloadText, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("record line %d has no event separator: %q", lineNumber+1, line)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
			t.Fatalf("decode record line %d: %v", lineNumber+1, err)
		}
		records = append(records, bunHookRecord{event: event, payload: payload})
	}
	return records
}

func assertBunHookCount(t *testing.T, records []bunHookRecord, event string, want int) {
	t.Helper()
	got := 0
	for _, record := range records {
		if record.event == event {
			got++
		}
	}
	if got != want {
		t.Fatalf("hook event %s count = %d, want %d; records=%#v", event, got, want, records)
	}
}

func bunHookPayload(t *testing.T, records []bunHookRecord, event string) map[string]interface{} {
	t.Helper()
	for _, record := range records {
		if record.event == event {
			return record.payload
		}
	}
	t.Fatalf("hook event %s not recorded", event)
	return nil
}

func bunHookPayloads(records []bunHookRecord, event string) []map[string]interface{} {
	payloads := []map[string]interface{}{}
	for _, record := range records {
		if record.event == event {
			payloads = append(payloads, record.payload)
		}
	}
	return payloads
}

// runBunContractDriver executes one Bun contract driver under a bound.
//
// These drivers load a generated plugin and exercise its hook surface. A plugin
// keeps a session-owned worker alive until the session ends, so a driver that
// leaves a session open leaves a live child, and an unbounded CombinedOutput
// then waits for it until the package timeout expires, taking the whole suite
// with it and orphaning the worker. The bound turns that into a fast, named
// failure.
func runBunContractDriver(t *testing.T, environment []string, bun string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), bunDriverBudget)
	defer cancel()
	command := exec.CommandContext(ctx, bun, args...)
	if len(environment) > 0 {
		command.Env = append(os.Environ(), environment...)
	}
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	if err == nil {
		return
	}
	if ctx.Err() != nil {
		t.Fatalf("Bun contract driver did not finish within %s; the plugin left a live child:\n%s", bunDriverBudget, output)
	}
	t.Fatalf("Bun contract driver failed: %v\n%s", err, output)
}
