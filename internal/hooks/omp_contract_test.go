package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedOMPExtensionPreservesNativeContracts(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("Bun is required to verify the generated OMP extension: %v", err)
	}
	repo := t.TempDir()
	artifact, err := Generate(KindOMP)
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

	logPath := filepath.Join(repo, "omp-records.jsonl")
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
  omp-user-bash)
    case "$payload" in
      *'"command":"rm generated.txt"'*) printf '%s\n' 'policy denied shell' >&2; exit 2 ;;
    esac
    ;;
  omp-pre-tool-use)
    case "$payload" in
      *'"tool_name":"write"'*) printf '%s\n' 'policy denied write' >&2; exit 2 ;;
    esac
    ;;
  omp-permission-request) printf '%s\n' 'observation unavailable' >&2; exit 2 ;;
  omp-stop) printf '%s\n' '{"decision":"block","reason":"finish the Reconc run"}' ;;
esac
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}

	driverPath := filepath.Join(repo, "omp-contract.js")
	driver := `const extensionPath = Bun.argv[2]
const repo = Bun.argv[3]
const module = await import("file://" + extensionPath + "?contract=" + Date.now())
if (typeof module.default !== "function") throw new Error("OMP extension factory missing")
const handlers = new Map()
const warnings = []
const pi = {
  logger: { warn: (message, metadata) => warnings.push({ message, metadata }) },
  on: (event, handler) => {
    if (handlers.has(event)) throw new Error("duplicate OMP handler " + event)
    handlers.set(event, handler)
  },
}
module.default(pi)
const expected = [
  "session_start", "input", "tool_call", "user_bash", "tool_approval_requested",
  "tool_approval_resolved", "tool_result", "session_stop",
  "auto_compaction_start", "auto_compaction_end", "session_shutdown",
]
if (JSON.stringify([...handlers.keys()]) !== JSON.stringify(expected)) {
  throw new Error("OMP handler set drift: " + JSON.stringify([...handlers.keys()]))
}
const ctx = {
  cwd: repo,
  sessionManager: {
    getSessionId: () => "omp-contract",
    getSessionFile: () => repo + "/session.jsonl",
  },
}
await handlers.get("session_start")({ type: "session_start" }, ctx)
await handlers.get("input")({ type: "input", text: "ship it", source: "interactive" }, ctx)
const allowed = await handlers.get("tool_call")({
  type: "tool_call", toolCallId: "call-read", toolName: "read", input: { path: "README.md" },
}, ctx)
if (allowed !== undefined) throw new Error("allowed OMP tool call gained a decision")
const denied = await handlers.get("tool_call")({
  type: "tool_call", toolCallId: "call-write", toolName: "write", input: { path: "generated/blocked.go", content: "x" },
}, ctx)
if (denied?.block !== true || !denied.reason.includes("policy denied write")) {
  throw new Error("OMP tool denial drift: " + JSON.stringify(denied))
}
const shellAllowed = await handlers.get("user_bash")({
  type: "user_bash", command: "ls", excludeFromContext: false, cwd: repo,
}, ctx)
if (shellAllowed !== undefined) throw new Error("allowed OMP shell command gained a replacement result")
const shellDenied = await handlers.get("user_bash")({
  type: "user_bash", command: "rm generated.txt", excludeFromContext: false, cwd: repo,
}, ctx)
if (shellDenied?.result?.exitCode !== 2 || !shellDenied.result.output.includes("policy denied shell")) {
  throw new Error("OMP user_bash denial drift: " + JSON.stringify(shellDenied))
}
const approvalObservation = await handlers.get("tool_approval_requested")({
  type: "tool_approval_requested", sessionId: "omp-contract", toolCallId: "call-write",
  toolName: "write", reason: "outside cwd", approvalMode: "always-ask",
}, ctx)
if (approvalObservation !== undefined) throw new Error("observational OMP approval gained a decision")
await handlers.get("tool_approval_resolved")({
  type: "tool_approval_resolved", sessionId: "omp-contract", toolCallId: "call-write",
  toolName: "write", approved: false, reason: "denied",
}, ctx)
await handlers.get("tool_result")({
  type: "tool_result", toolCallId: "call-bash-ok", toolName: "bash",
  input: { command: "true" }, content: [{ type: "text", text: "ok" }], details: undefined, isError: false,
}, ctx)
await handlers.get("tool_result")({
  type: "tool_result", toolCallId: "call-bash-fail", toolName: "bash",
  input: { command: "false" }, content: [{ type: "text", text: "failed" }], details: { exitCode: 1 }, isError: true,
}, ctx)
const stop = await handlers.get("session_stop")({
  type: "session_stop", session_id: "omp-contract", session_file: repo + "/session.jsonl",
  turn_id: 7, stop_hook_active: false, signal: new AbortController().signal,
}, ctx)
if (stop?.decision !== "block" || stop.reason !== "finish the Reconc run") {
  throw new Error("OMP Stop decision drift: " + JSON.stringify(stop))
}
await handlers.get("auto_compaction_start")({
  type: "auto_compaction_start", reason: "threshold", action: "context-full",
}, ctx)
await handlers.get("auto_compaction_end")({
  type: "auto_compaction_end", action: "context-full", result: undefined,
  aborted: false, willRetry: false, skipped: false,
}, ctx)
await handlers.get("session_shutdown")({ type: "session_shutdown" }, ctx)
if (warnings.length !== 1 || warnings[0]?.metadata?.event !== "omp-permission-request") {
  throw new Error("OMP observation failure did not fail open with one bounded warning: " + JSON.stringify(warnings))
}
const aborted = new AbortController()
aborted.abort()
const canceled = await handlers.get("session_stop")({
  type: "session_stop", session_id: "omp-contract", turn_id: 8,
  stop_hook_active: true, signal: aborted.signal,
}, ctx)
if (canceled !== undefined) throw new Error("aborted OMP Stop must yield to the host")
`
	if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	runBunContractDriver(t, []string{"RECONC_TEST_LOG=" + logPath}, bun, driverPath, extensionPath, repo)

	records := readBunHookRecords(t, logPath)
	for event, want := range map[string]int{
		"omp-session-start":         1,
		"omp-user-prompt-submit":    1,
		"omp-pre-tool-use":          2,
		"omp-permission-request":    1,
		"omp-permission-result":     1,
		"omp-post-tool-use":         1,
		"omp-post-tool-use-failure": 1,
		"omp-stop":                  1,
		"omp-pre-compaction":        1,
		"omp-post-compaction":       1,
		"omp-session-end":           1,
	} {
		assertBunHookCount(t, records, event, want)
	}
	input := bunHookPayload(t, records, "omp-user-prompt-submit")
	if input["prompt"] != "ship it" || input["input_source"] != "interactive" {
		t.Fatalf("OMP input payload = %#v", input)
	}
	resolved := bunHookPayload(t, records, "omp-permission-result")
	if resolved["approved"] != false || resolved["tool_name"] != "write" {
		t.Fatalf("OMP approval payload = %#v", resolved)
	}
	success := bunHookPayload(t, records, "omp-post-tool-use")["tool_response"].(map[string]interface{})
	if success["success"] != true || success["exit_code"] != float64(0) {
		t.Fatalf("OMP successful Bash evidence = %#v", success)
	}
	failure := bunHookPayload(t, records, "omp-post-tool-use-failure")
	response := failure["tool_response"].(map[string]interface{})
	if failure["error"] != "failed" || response["success"] != false || response["exit_code"] != float64(1) {
		t.Fatalf("OMP failed Bash evidence = %#v", failure)
	}
	stop := bunHookPayload(t, records, "omp-stop")
	if stop["turn_id"] != float64(7) || stop["stop_hook_active"] != false {
		t.Fatalf("OMP Stop payload = %#v", stop)
	}
}

func TestGeneratedOMPExtensionTransportFailsClosedWithinBudget(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("Bun is required to verify the generated OMP extension: %v", err)
	}
	for _, mode := range []string{"large", "invalid-utf8", "timeout", "spawn-failure", "abort"} {
		t.Run(mode, func(t *testing.T) {
			repo := t.TempDir()
			artifact, err := Generate(KindOMP)
			if err != nil {
				t.Fatal(err)
			}
			content := strings.Replace(
				artifact.Content,
				`"omp-pre-tool-use":{"timeoutMilliseconds":10000`,
				`"omp-pre-tool-use":{"timeoutMilliseconds":50`,
				1,
			)
			content = strings.Replace(
				content,
				`"omp-stop":{"timeoutMilliseconds":29000`,
				`"omp-stop":{"timeoutMilliseconds":50`,
				1,
			)
			// The user's own shell command runs under the same blocking budget
			// as a tool call and must fail closed the same way.
			content = strings.Replace(
				content,
				`"omp-user-bash":{"timeoutMilliseconds":10000`,
				`"omp-user-bash":{"timeoutMilliseconds":50`,
				1,
			)
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
case "$RECONC_OMP_TRANSPORT_MODE" in
  large)
    perl -e 'print "o" x 1048576' &
    perl -e 'print STDERR "e" x 1048576' &
    wait
    exit 1
    ;;
  invalid-utf8) printf '\377\376broken\n' >&2; exit 0 ;;
  timeout) sleep 2; exit 0 ;;
  abort) sleep 2; exit 0 ;;
esac
`
				if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			driverPath := filepath.Join(repo, "omp-transport.js")
			driver := `const module = await import("file://" + Bun.argv[2] + "?transport=" + Date.now())
const handlers = new Map()
module.default({ logger: { warn: () => {} }, on: (event, handler) => handlers.set(event, handler) })
if (Bun.argv[4] === "spawn-failure") process.env.PATH = ""
const ctx = {
  cwd: Bun.argv[3],
  sessionManager: { getSessionId: () => "omp-transport", getSessionFile: () => undefined },
}
const started = Date.now()
if (Bun.argv[4] === "abort") {
  const controller = new AbortController()
  const pending = handlers.get("session_stop")({
    type: "session_stop", session_id: "omp-transport", turn_id: 1,
    stop_hook_active: true, signal: controller.signal,
  }, ctx)
  setTimeout(() => controller.abort(), 20)
  const canceled = await pending
  if (canceled !== undefined) throw new Error("in-flight aborted OMP Stop must yield to the host")
  if (Date.now() - started > 1500) throw new Error("in-flight OMP Stop abort did not kill promptly")
  process.exit(0)
}
const result = await handlers.get("tool_call")({
  type: "tool_call", toolCallId: "call-1", toolName: "write", input: { path: "blocked" },
}, ctx)
if (result?.block !== true || typeof result.reason !== "string" || result.reason.length === 0) {
  throw new Error("OMP transport failure did not block: " + JSON.stringify(result))
}
if (new TextEncoder().encode(result.reason).length > 8192) throw new Error("OMP failure exceeded output budget")
const shell = await handlers.get("user_bash")({
  type: "user_bash", command: "rm blocked", excludeFromContext: false, cwd: Bun.argv[3],
}, ctx)
if (shell?.result?.exitCode !== 2) throw new Error("OMP transport failure did not block user_bash")
const stop = await handlers.get("session_stop")({
  type: "session_stop", session_id: "omp-transport", turn_id: 1,
  stop_hook_active: false, signal: new AbortController().signal,
}, ctx)
if (stop?.decision !== "block" || typeof stop.reason !== "string" || stop.reason.length === 0) {
  throw new Error("OMP Stop transport failure did not block: " + JSON.stringify(stop))
}
if (new TextEncoder().encode(stop.reason).length > 8192) throw new Error("OMP Stop failure exceeded output budget")
if (Bun.argv[4] === "timeout" && Date.now() - started > 1500) throw new Error("OMP timeouts did not kill promptly")
// The extension owns a session worker until the session ends. Releasing it
// here keeps the driver from exiting its own work and then waiting on a live
// child. It runs after the timing assertion so shutdown never counts toward it.
try { await handlers.get("session_shutdown")({ type: "session_shutdown" }, ctx) } catch {}
`
			if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
				t.Fatal(err)
			}
			runBunContractDriver(t, []string{"RECONC_OMP_TRANSPORT_MODE=" + mode}, bun, driverPath, extensionPath, repo, mode)
		})
	}
}
