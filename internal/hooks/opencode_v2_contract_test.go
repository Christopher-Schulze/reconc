package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The 2.x host is a protocol mock built from the published @opencode/plugin
// 2.0.32 domain types; the 1.x contract stays covered by the Bun plugin tests.
func TestGeneratedOpenCodePluginServesTheV2HostAPI(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("Bun is required to verify the generated OpenCode plugin: %v", err)
	}
	repo := t.TempDir()
	artifact, err := Generate(KindOpenCode)
	if err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(repo, filepath.FromSlash(artifact.TargetPath))
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pluginPath, []byte(artifact.Content), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(repo, "hook-records.jsonl")
	wrapperPath := filepath.Join(repo, "tools", "reconc", "bin", "hook")
	if err := os.MkdirAll(filepath.Dir(wrapperPath), 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := `#!/bin/sh
set -eu
event="$1"
payload="$(cat)"
printf '%s\t%s\n' "$event" "$payload" >> "$RECONC_TEST_LOG"
case "$event" in
  *-pre-tool-use)
    case "$payload" in
      *generated/*) printf '%s\n' 'policy denied write' >&2; exit 2 ;;
    esac ;;
  *-permission-request) printf '%s\n' '{"permissionDecision":"deny"}' ;;
  *-pre-compaction) printf '%s\n' '{"hookSpecificOutput":{"additionalContext":"recovery-packet"}}' ;;
  *-stop) printf '%s\n' '{"reason":"finish the Reconc run"}' ;;
esac
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	driverPath := filepath.Join(repo, "v2-contract.js")
	driver := `const pluginModule = await import("file://" + Bun.argv[2] + "?contract=" + Date.now())
const repo = Bun.argv[3]
const registrations = new Map()
const prompts = []
const register = (domain) => async (name, callback) => {
  if (registrations.has(domain + "." + name)) throw new Error("duplicate 2.x hook " + domain + "." + name)
  registrations.set(domain + "." + name, callback)
  return { dispose: async () => {} }
}
const sessionID = "ses_v2"
// An event that throws while it is read must not end lifecycle handling.
let poisoned = false
const poison = { type: "session.execution.succeeded", get data() { poisoned = true; throw new Error("unreadable event") } }
const queued = [
  poison,
  { type: "session.execution.succeeded", data: { sessionID: "ses_other" } },
  { type: "session.execution.succeeded", data: { sessionID }, location: { directory: "/elsewhere" } },
  { type: "session.created", data: { sessionID } },
  { type: "session.execution.interrupted", data: { sessionID, reason: "user" } },
  { type: "session.status", data: { sessionID, status: { type: "idle" } } },
  { type: "session.idle", data: { sessionID } },
  { type: "session.execution.succeeded", data: { sessionID } },
  { type: "session.compaction.ended", data: { sessionID } },
]
let release
const drained = new Promise((resolve) => { release = resolve })
let startEvents
const eventsGo = new Promise((resolve) => { startEvents = resolve })
const ctx = {
  location: { directory: repo, worktree: repo },
  session: {
    hook: register("session"),
    prompt: async (input) => { prompts.push(input); return { data: { id: "msg_admitted" } } },
  },
  tool: { hook: register("tool") },
  permission: { hook: register("permission") },
  event: {
    subscribe: ({ signal }) => (async function* () {
      await eventsGo
      for (const event of queued) yield event
      release()
      await new Promise((resolve) => signal.addEventListener("abort", resolve, { once: true }))
    })(),
  },
}
const cleanup = await pluginModule.default.setup(ctx)
const expected = ["session.prompt", "tool.execute.before", "tool.execute.after", "permission.evaluate", "session.compaction"]
if (JSON.stringify([...registrations.keys()]) !== JSON.stringify(expected)) {
  throw new Error("2.x hook set drift: " + JSON.stringify([...registrations.keys()]))
}
await registrations.get("session.prompt")({ sessionID, messageID: "msg_user", prompt: { text: "ship it" }, delivery: "steer" })
await registrations.get("tool.execute.before")({ tool: "write", sessionID, id: "call_write", messageID: "msg_a", input: { path: "src/app.go", content: "x" } })
let blocked = false
try {
  await registrations.get("tool.execute.before")({ tool: "write", sessionID, id: "call_denied", messageID: "msg_a", input: { path: "generated/out.go", content: "x" } })
} catch (error) {
  blocked = String(error?.message || error).includes("policy denied write")
}
if (!blocked) throw new Error("a denied 2.x write must reject the call")
await registrations.get("tool.execute.before")({ tool: "patch", sessionID, id: "call_patch", messageID: "msg_a", input: { patchText: "*** Begin Patch\n*** Add File: src/new.go\n+package src\n*** End Patch" } })
await registrations.get("tool.execute.after")({ tool: "shell", sessionID, id: "call_shell", messageID: "msg_a", input: { command: "go test" }, status: "completed", result: { content: [{ type: "text", text: "ok" }], metadata: { exit: 0 } } })
await registrations.get("tool.execute.after")({ tool: "shell", sessionID, id: "call_failed", messageID: "msg_a", input: { command: "false" }, status: "error", error: { message: "exit status 1" } })
const permission = { sessionID, action: "bash", resources: ["protected.txt"], effect: "ask" }
await registrations.get("permission.evaluate")(permission)
if (permission.effect !== "deny") throw new Error("2.x permission denial was not applied: " + JSON.stringify(permission))
const compaction = { sessionID, system: [] }
await registrations.get("session.compaction")(compaction)
if (compaction.system.length !== 1 || compaction.system[0].text !== "recovery-packet") throw new Error("2.x compaction context drift: " + JSON.stringify(compaction.system))
startEvents()
const drainedInTime = await Promise.race([drained.then(() => true), new Promise((resolve) => setTimeout(() => resolve(false), 10000))])
if (!drainedInTime) throw new Error("lifecycle handling stopped before the event stream drained")
await cleanup()
if (!poisoned) throw new Error("the unreadable event was never delivered")
if (prompts.length !== 1 || prompts[0].sessionID !== sessionID || prompts[0].delivery !== "queue" ||
    prompts[0].text !== "finish the Reconc run" || !String(prompts[0].metadata?.reconc_message_id).startsWith("msg_reconc_")) {
  throw new Error("2.x continuation drift: " + JSON.stringify(prompts))
}
`
	if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	runBunContractDriver(t, []string{"RECONC_TEST_LOG=" + logPath}, bun, driverPath, pluginPath, repo)

	records := readBunHookRecords(t, logPath)
	for event, want := range map[string]int{
		"opencode-session-start":         1,
		"opencode-user-prompt-submit":    1,
		"opencode-pre-tool-use":          3,
		"opencode-post-tool-use":         1,
		"opencode-post-tool-use-failure": 1,
		"opencode-permission-request":    1,
		"opencode-pre-compaction":        1,
		"opencode-post-compaction":       1,
		"opencode-stop":                  1,
		"opencode-continuation-accepted": 1,
	} {
		assertBunHookCount(t, records, event, want)
	}
	pre := bunHookPayloads(records, "opencode-pre-tool-use")
	if pre[0]["tool_name"] != "Write" || pre[0]["tool_input"].(map[string]interface{})["path"] != "src/app.go" {
		t.Fatalf("2.x write payload = %#v", pre[0])
	}
	patch := pre[2]["tool_input"].(map[string]interface{})
	if pre[2]["tool_name"] != "apply_patch" || !strings.Contains(patch["command"].(string), "*** Add File: src/new.go") {
		t.Fatalf("2.x patch must reach the apply_patch path parser: %#v", pre[2])
	}
	post := bunHookPayload(t, records, "opencode-post-tool-use")["tool_response"].(map[string]interface{})
	if post["exit_code"] != float64(0) || post["success"] != true {
		t.Fatalf("2.x shell outcome = %#v", post)
	}
}

// The 2.x tracker keeps the most recently active sessions: a session's own
// hooks refresh it, so eviction drops the least recently active one.
func TestOpenCodeSessionTrackerEvictsTheLeastRecentlyActiveSession(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("Bun is required to verify the generated OpenCode plugin: %v", err)
	}
	artifact, err := Generate(KindOpenCode)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(artifact.Content, openCodeSessionTracker) {
		t.Fatal("the generated plugin does not ship the tested session tracker")
	}
	driver := openCodeSessionTracker + `
const sessions = trackedSessions(2)
sessions.track("a")
sessions.track("b")
sessions.track("a")
sessions.track("c")
if (!sessions.has("a") || sessions.has("b") || !sessions.has("c")) throw new Error("tracker evicted an active session")
sessions.forget("a")
if (sessions.has("a")) throw new Error("a deleted session stays tracked")
sessions.track("")
sessions.track(42)
if (sessions.has("") || sessions.has(42)) throw new Error("tracker accepted an invalid session id")
`
	path := filepath.Join(t.TempDir(), "tracker.js")
	if err := os.WriteFile(path, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(bun, path).CombinedOutput(); err != nil {
		t.Fatalf("session tracker: %v\n%s", err, output)
	}
}
