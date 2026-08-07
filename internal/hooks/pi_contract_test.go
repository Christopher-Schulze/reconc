package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedPiExtensionPreservesNativeContracts(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("Bun is required to verify the generated Pi extension: %v", err)
	}
	repo := t.TempDir()
	artifact, err := Generate(KindPi)
	if err != nil {
		t.Fatal(err)
	}
	extensionPath := filepath.Join(repo, filepath.FromSlash(artifact.TargetPath))
	if err := os.MkdirAll(filepath.Dir(extensionPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extensionPath, []byte(artifact.Content), 0o644); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(repo, "pi-records.jsonl")
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
  pi-pre-tool-use)
    case "$payload" in
      *'"tool_name":"write"'*) printf '%s\n' 'policy denied write' >&2; exit 2 ;;
    esac
    ;;
  pi-user-bash)
    case "$payload" in
      *'"command":"rm generated.txt"'*) printf '%s\n' 'policy denied shell' >&2; exit 2 ;;
    esac
    ;;
  pi-stop) printf '%s\n' '{"decision":"block","reason":"finish the Reconc run"}' ;;
  pi-post-compaction) printf '%s\n' 'observation unavailable' >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}

	driverPath := filepath.Join(repo, "pi-contract.js")
	driver := `const extensionPath = Bun.argv[2]
const repo = Bun.argv[3]
const module = await import("file://" + extensionPath + "?contract=" + Date.now())
if (typeof module.default !== "function") throw new Error("Pi extension factory missing")
const handlers = new Map()
const sent = []
const diagnostics = []
const originalError = console.error
console.error = (message) => diagnostics.push(String(message))
const pi = {
  on: (event, handler) => {
    if (handlers.has(event)) throw new Error("duplicate Pi handler " + event)
    handlers.set(event, handler)
  },
  sendUserMessage: (content) => sent.push(content),
}
module.default(pi)
const expected = [
  "session_start", "input", "tool_call", "tool_result", "user_bash",
  "session_before_compact", "session_compact", "agent_settled", "session_shutdown",
]
if (JSON.stringify([...handlers.keys()]) !== JSON.stringify(expected)) {
  throw new Error("Pi handler set drift: " + JSON.stringify([...handlers.keys()]))
}
const ctx = {
  cwd: repo,
  signal: undefined,
  sessionManager: {
    getSessionId: () => "pi-contract",
    getSessionFile: () => repo + "/session.jsonl",
  },
}
await handlers.get("session_start")({ type: "session_start", reason: "startup" }, ctx)
await handlers.get("input")({ type: "input", text: "ship it", source: "interactive" }, ctx)
const allowed = await handlers.get("tool_call")({
  type: "tool_call", toolCallId: "call-read", toolName: "read", input: { path: "README.md" },
}, ctx)
if (allowed !== undefined) throw new Error("allowed Pi tool call gained a decision")
const denied = await handlers.get("tool_call")({
  type: "tool_call", toolCallId: "call-write", toolName: "write", input: { path: "generated/blocked.go", content: "x" },
}, ctx)
if (denied?.block !== true || !denied.reason.includes("policy denied write")) {
  throw new Error("Pi tool denial drift: " + JSON.stringify(denied))
}
const shellAllowed = await handlers.get("user_bash")({
  type: "user_bash", command: "git status", excludeFromContext: true, cwd: repo,
}, ctx)
if (shellAllowed !== undefined) throw new Error("allowed Pi user_bash gained a replacement")
const shellDenied = await handlers.get("user_bash")({
  type: "user_bash", command: "rm generated.txt", excludeFromContext: false, cwd: repo,
}, ctx)
if (shellDenied?.result?.exitCode !== 2 || !shellDenied.result.output.includes("policy denied shell")) {
  throw new Error("Pi user_bash denial drift: " + JSON.stringify(shellDenied))
}
await handlers.get("tool_result")({
  type: "tool_result", toolCallId: "call-bash-ok", toolName: "bash",
  input: { command: "true" }, content: [{ type: "text", text: "ok" }], details: undefined, isError: false,
}, ctx)
await handlers.get("tool_result")({
  type: "tool_result", toolCallId: "call-bash-fail", toolName: "bash",
  input: { command: "false" }, content: [{ type: "text", text: "failed" }], details: {}, isError: true,
}, ctx)
await handlers.get("session_before_compact")({
  type: "session_before_compact", reason: "threshold", willRetry: false, signal: new AbortController().signal,
}, ctx)
await handlers.get("session_compact")({
  type: "session_compact", reason: "threshold", willRetry: false, fromExtension: false,
}, ctx)
await handlers.get("agent_settled")({ type: "agent_settled" }, ctx)
if (sent.length !== 1 || sent[0] !== "finish the Reconc run") {
  throw new Error("Pi continuation request drift: " + JSON.stringify(sent))
}
await handlers.get("agent_settled")({ type: "agent_settled" }, ctx)
if (sent.length !== 1) throw new Error("repeated Pi settled event duplicated continuation")
const aborted = new AbortController()
aborted.abort()
ctx.signal = aborted.signal
const canceled = await handlers.get("tool_call")({
  type: "tool_call", toolCallId: "call-abort", toolName: "write", input: { path: "aborted" },
}, ctx)
if (canceled !== undefined) throw new Error("aborted Pi tool hook must yield to host cancellation")
ctx.signal = undefined
await handlers.get("session_shutdown")({ type: "session_shutdown", reason: "quit" }, ctx)
console.error = originalError
if (diagnostics.length !== 1 || !diagnostics[0].includes("pi-post-compaction")) {
  throw new Error("Pi observation failure diagnostics drift: " + JSON.stringify(diagnostics))
}
`
	if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	runBunContractDriver(t, []string{"RECONC_TEST_LOG=" + logPath}, bun, driverPath, extensionPath, repo)

	records := readBunHookRecords(t, logPath)
	for event, want := range map[string]int{
		"pi-session-start":           1,
		"pi-user-prompt-submit":      1,
		"pi-pre-tool-use":            2,
		"pi-user-bash":               2,
		"pi-post-tool-use":           1,
		"pi-post-tool-use-failure":   1,
		"pi-pre-compaction":          1,
		"pi-post-compaction":         1,
		"pi-stop":                    1,
		"pi-continuation-requested":  1,
		"pi-continuation-suppressed": 1,
		"pi-session-end":             1,
	} {
		assertBunHookCount(t, records, event, want)
	}
	input := bunHookPayload(t, records, "pi-user-prompt-submit")
	if input["prompt"] != "ship it" || input["input_source"] != "interactive" {
		t.Fatalf("Pi input payload = %#v", input)
	}
	success := bunHookPayload(t, records, "pi-post-tool-use")["tool_response"].(map[string]interface{})
	if success["success"] != true || success["exit_code"] != float64(0) {
		t.Fatalf("Pi successful Bash evidence = %#v", success)
	}
	failure := bunHookPayload(t, records, "pi-post-tool-use-failure")["tool_response"].(map[string]interface{})
	if failure["success"] != false || failure["error"] != "failed" {
		t.Fatalf("Pi failed Bash evidence = %#v", failure)
	}
	if _, fabricated := failure["exit_code"]; fabricated {
		t.Fatalf("Pi failed Bash evidence fabricated exit status: %#v", failure)
	}
}

func TestGeneratedPiExtensionTransportBoundaries(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("Bun is required to verify the generated Pi extension: %v", err)
	}
	for _, mode := range []string{"invalid-utf8", "oversized", "timeout", "spawn-failure"} {
		t.Run(mode, func(t *testing.T) {
			repo := t.TempDir()
			artifact, err := Generate(KindPi)
			if err != nil {
				t.Fatal(err)
			}
			content := strings.Replace(artifact.Content, `"pi-pre-tool-use":{"timeoutMilliseconds":10000`, `"pi-pre-tool-use":{"timeoutMilliseconds":50`, 1)
			content = strings.Replace(content, `"pi-user-bash":{"timeoutMilliseconds":10000`, `"pi-user-bash":{"timeoutMilliseconds":50`, 1)
			content = strings.Replace(content, `"pi-stop":{"timeoutMilliseconds":30000`, `"pi-stop":{"timeoutMilliseconds":50`, 1)
			content = strings.Replace(content, `"pi-continuation-failed":{"timeoutMilliseconds":5000`, `"pi-continuation-failed":{"timeoutMilliseconds":50`, 1)
			extensionPath := filepath.Join(repo, filepath.FromSlash(artifact.TargetPath))
			if err := os.MkdirAll(filepath.Dir(extensionPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(extensionPath, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if mode != "spawn-failure" {
				wrapperPath := filepath.Join(repo, "tools", "reconc", "bin", "hook")
				if err := os.MkdirAll(filepath.Dir(wrapperPath), 0o755); err != nil {
					t.Fatal(err)
				}
				wrapper := `#!/bin/sh
case "$RECONC_PI_TRANSPORT_MODE" in
  invalid-utf8) printf '\377\376broken\n' >&2; exit 0 ;;
  oversized) head -c 9000 /dev/zero | tr '\000' x; exit 0 ;;
  timeout) sleep 2; exit 0 ;;
esac
`
				if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			driverPath := filepath.Join(repo, "pi-transport.js")
			driver := `const module = await import("file://" + Bun.argv[2] + "?transport=" + Date.now())
const handlers = new Map()
const sent = []
module.default({ on: (event, handler) => handlers.set(event, handler), sendUserMessage: (value) => sent.push(value) })
if (Bun.argv[4] === "spawn-failure") process.env.PATH = ""
const ctx = {
  cwd: Bun.argv[3], signal: undefined,
  sessionManager: { getSessionId: () => "pi-transport", getSessionFile: () => undefined },
}
const started = Date.now()
const blocked = await handlers.get("tool_call")({
  type: "tool_call", toolCallId: "call-1", toolName: "write", input: { path: "blocked" },
}, ctx)
if (blocked?.block !== true || typeof blocked.reason !== "string" || blocked.reason.length === 0) {
  throw new Error("Pi transport failure did not block tool_call: " + JSON.stringify(blocked))
}
const shell = await handlers.get("user_bash")({
  type: "user_bash", command: "rm blocked", excludeFromContext: false, cwd: Bun.argv[3],
}, ctx)
if (shell?.result?.exitCode !== 2) throw new Error("Pi transport failure did not block user_bash")
await handlers.get("agent_settled")({ type: "agent_settled" }, ctx)
if (sent.length !== 0) throw new Error("Pi transport failure invented continuation delivery")
if (Bun.argv[4] === "timeout" && Date.now() - started > 1500) throw new Error("Pi timeouts did not kill promptly")
// The extension owns a session worker until the session ends. Releasing it
// here keeps the driver from exiting its own work and then waiting on a live
// child. It runs after the timing assertion so shutdown never counts toward it.
try { await handlers.get("session_shutdown")({ type: "session_shutdown" }, ctx) } catch {}
`
			if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
				t.Fatal(err)
			}
			runBunContractDriver(t, []string{"RECONC_PI_TRANSPORT_MODE=" + mode}, bun, driverPath, extensionPath, repo, mode)
		})
	}
}
