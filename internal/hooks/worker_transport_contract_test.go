package hooks

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPersistentAdaptersReuseOneSessionOwnedWorker(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("Bun is required to verify generated persistent transports: %v", err)
	}
	for _, kind := range []string{KindOpenCode, KindKilo, KindOMP, KindPi} {
		t.Run(kind, func(t *testing.T) {
			repo := t.TempDir()
			artifact, err := Generate(kind)
			if err != nil {
				t.Fatal(err)
			}
			artifactPath := filepath.Join(repo, filepath.FromSlash(artifact.TargetPath))
			if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(artifactPath, []byte(artifact.Content), 0o644); err != nil {
				t.Fatal(err)
			}

			logPath := filepath.Join(repo, "worker-frames.jsonl")
			wrapperLogPath := filepath.Join(repo, "wrapper-invocations.log")
			workerProgramPath := filepath.Join(repo, "fake-worker.js")
			if err := os.WriteFile(workerProgramPath, []byte(fakeBunHookWorker), 0o644); err != nil {
				t.Fatal(err)
			}
			wrapperPath := filepath.Join(repo, filepath.FromSlash(WrapperPath))
			if err := os.MkdirAll(filepath.Dir(wrapperPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(wrapperPath, []byte(fakeWorkerWrapper), 0o755); err != nil {
				t.Fatal(err)
			}
			driverPath := filepath.Join(repo, "worker-driver.js")
			if err := os.WriteFile(driverPath, []byte(persistentWorkerDriver), 0o644); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(bun, driverPath, artifactPath, kind, repo)
			command.Env = append(os.Environ(),
				"RECONC_WORKER_TEST_LOG="+logPath,
				"RECONC_WORKER_TEST_PROGRAM="+workerProgramPath,
				"RECONC_WORKER_WRAPPER_LOG="+wrapperLogPath,
			)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("%s persistent worker contract: %v\n%s", kind, err, output)
			}
			if _, err := os.Stat(logPath); err != nil {
				wrapperCalls, _ := os.ReadFile(wrapperLogPath)
				t.Fatalf("%s worker did not create its protocol log: %v\nwrapper calls:\n%s\n%s", kind, err, wrapperCalls, output)
			}
			records := readWorkerTransportRecords(t, logPath)
			if len(records) < 5 {
				t.Fatalf("%s worker records=%v", kind, records)
			}
			if records[0]["record"] != "start" || records[0]["command"] != "worker" {
				t.Fatalf("%s did not start one worker: %v", kind, records[0])
			}
			starts := 0
			requests := 0
			shutdowns := 0
			for _, record := range records {
				switch record["record"] {
				case "start":
					starts++
				case "request":
					requests++
				case "shutdown":
					shutdowns++
				}
			}
			if starts != 1 || requests < 3 || shutdowns != 1 {
				t.Fatalf("%s worker lifecycle starts=%d requests=%d shutdowns=%d records=%v", kind, starts, requests, shutdowns, records)
			}
		})
	}
}

func TestPersistentAdapterCrashFallsBackAndRestarts(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("Bun is required to verify worker crash recovery: %v", err)
	}
	repo := t.TempDir()
	artifact, err := Generate(KindOpenCode)
	if err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(repo, filepath.FromSlash(artifact.TargetPath))
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte(artifact.Content), 0o644); err != nil {
		t.Fatal(err)
	}
	workerProgramPath := filepath.Join(repo, "crash-worker.js")
	if err := os.WriteFile(workerProgramPath, []byte(crashingBunHookWorker), 0o644); err != nil {
		t.Fatal(err)
	}
	wrapperPath := filepath.Join(repo, filepath.FromSlash(WrapperPath))
	if err := os.MkdirAll(filepath.Dir(wrapperPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapperPath, []byte(crashRecoveryWrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	driverPath := filepath.Join(repo, "crash-driver.js")
	if err := os.WriteFile(driverPath, []byte(workerCrashDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(repo, "crash-records.jsonl")
	command := exec.Command(bun, driverPath, artifactPath, repo)
	command.Env = append(os.Environ(),
		"RECONC_WORKER_TEST_LOG="+logPath,
		"RECONC_WORKER_TEST_PROGRAM="+workerProgramPath,
		"RECONC_WORKER_CRASH_MARKER="+filepath.Join(repo, "crashed-once"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("worker crash recovery contract: %v\n%s", err, output)
	}
	records := readWorkerTransportRecords(t, logPath)
	starts := 0
	fallbacks := 0
	shutdowns := 0
	for _, record := range records {
		switch record["record"] {
		case "start":
			starts++
		case "fallback":
			fallbacks++
		case "shutdown":
			shutdowns++
		}
	}
	if starts != 2 || fallbacks != 1 || shutdowns != 1 {
		t.Fatalf("crash lifecycle starts=%d fallbacks=%d shutdowns=%d records=%v", starts, fallbacks, shutdowns, records)
	}
}

func readWorkerTransportRecords(t *testing.T, path string) []map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	records := []map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record map[string]string
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode worker record: %v: %q", err, scanner.Text())
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

const fakeWorkerWrapper = `#!/bin/sh
printf '%s\n' "$1" >> "$RECONC_WORKER_WRAPPER_LOG"
if [ "$1" != "__worker_v1__" ]; then
  echo "unexpected one-shot invocation: $*" >&2
  exit 64
fi
exec bun "$RECONC_WORKER_TEST_PROGRAM"
`

const fakeBunHookWorker = `
import { appendFileSync } from "node:fs"
const record = (value) => appendFileSync(process.env.RECONC_WORKER_TEST_LOG, JSON.stringify(value) + "\n")
record({ record: "start", command: "worker" })
const decoder = new TextDecoder("utf-8", { fatal: true })
let buffered = ""
for await (const chunk of Bun.stdin.stream()) {
  buffered += decoder.decode(chunk, { stream: true })
  while (true) {
    const newline = buffered.indexOf("\n")
    if (newline < 0) break
    const line = buffered.slice(0, newline)
    buffered = buffered.slice(newline + 1)
    const frame = JSON.parse(line)
    record({ record: frame.type, id: frame.id, event: frame.event || "" })
    if (frame.type === "shutdown") {
      console.log(JSON.stringify({ format_version: 1, type: "shutdown", id: frame.id, code: 0 }))
      process.exit(0)
    }
    console.log(JSON.stringify({ format_version: 1, type: "response", id: frame.id, code: 0 }))
  }
}
`

const persistentWorkerDriver = `const artifactPath = Bun.argv[2]
const kind = Bun.argv[3]
const repo = Bun.argv[4]
const module = await import("file://" + artifactPath + "?worker=" + Date.now())
if (kind === "opencode" || kind === "kilo") {
  const factory = kind === "opencode" ? module.ReconcOpenCodePlugin : module.default.server
  const hooks = await factory({ directory: repo, worktree: repo, client: {} })
  await hooks["chat.message"]({ sessionID: "ses_worker", messageID: "msg-1" }, { parts: [{ type: "text", text: "go" }] })
  await hooks["tool.execute.after"](
    { sessionID: "ses_worker", tool: "read", callID: "call-1", args: { path: "README.md" } },
    { title: "read", output: "ok", metadata: {} },
  )
  await hooks.event({ event: { type: "session.deleted", properties: { sessionID: "ses_worker" } } })
} else {
  const handlers = new Map()
  const host = kind === "omp"
    ? { logger: { warn: () => {} }, on: (event, handler) => handlers.set(event, handler) }
    : { on: (event, handler) => handlers.set(event, handler), sendUserMessage: () => {} }
  module.default(host)
  const ctx = {
    cwd: repo,
    signal: undefined,
    sessionManager: { getSessionId: () => kind + "-worker", getSessionFile: () => undefined },
  }
  await handlers.get("session_start")({ type: "session_start", reason: "startup" }, ctx)
  await handlers.get("input")({ type: "input", text: "go", source: "interactive" }, ctx)
  await handlers.get("session_shutdown")({ type: "session_shutdown", reason: "quit" }, ctx)
}
`

const crashRecoveryWrapper = `#!/bin/sh
if [ "$1" = "__worker_v1__" ]; then
  exec bun "$RECONC_WORKER_TEST_PROGRAM"
fi
printf '%s\n' '{"record":"fallback","event":"'$1'"}' >> "$RECONC_WORKER_TEST_LOG"
printf '%s\n' 'one-shot fallback denied the tool' >&2
exit 2
`

const crashingBunHookWorker = `
import { appendFileSync, existsSync } from "node:fs"
const record = (value) => appendFileSync(process.env.RECONC_WORKER_TEST_LOG, JSON.stringify(value) + "\n")
record({ record: "start" })
let buffered = ""
for await (const chunk of Bun.stdin.stream()) {
  buffered += new TextDecoder().decode(chunk)
  while (true) {
    const newline = buffered.indexOf("\n")
    if (newline < 0) break
    const frame = JSON.parse(buffered.slice(0, newline))
    buffered = buffered.slice(newline + 1)
    if (frame.type === "shutdown") {
      record({ record: "shutdown" })
      console.log(JSON.stringify({ format_version: 1, type: "shutdown", id: frame.id, code: 0 }))
      process.exit(0)
    }
    if (frame.type === "request" && frame.event === "opencode-pre-tool-use" && !existsSync(process.env.RECONC_WORKER_CRASH_MARKER)) {
      appendFileSync(process.env.RECONC_WORKER_CRASH_MARKER, "crashed\n")
      record({ record: "crash", event: frame.event })
      process.exit(17)
    }
    if (frame.type === "request") record({ record: "request", event: frame.event })
    console.log(JSON.stringify({ format_version: 1, type: "response", id: frame.id, code: 0 }))
  }
}
`

const workerCrashDriver = `const module = await import("file://" + Bun.argv[2] + "?crash=" + Date.now())
const repo = Bun.argv[3]
const hooks = await module.ReconcOpenCodePlugin({ directory: repo, worktree: repo, client: {} })
await hooks["chat.message"]({ sessionID: "ses_crash", messageID: "msg-1" }, { parts: [{ type: "text", text: "go" }] })
let blocked = false
try {
  await hooks["tool.execute.before"](
    { sessionID: "ses_crash", tool: "write", callID: "call-1", args: { path: "blocked" } },
    {},
  )
} catch (error) {
  blocked = String(error?.message || error).includes("one-shot fallback denied the tool")
}
if (!blocked) throw new Error("worker crash did not preserve fail-closed one-shot fallback")
await hooks["tool.execute.after"](
  { sessionID: "ses_crash", tool: "read", callID: "call-2", args: { path: "README.md" } },
  { title: "read", output: "ok", metadata: {} },
)
await hooks.event({ event: { type: "session.deleted", properties: { sessionID: "ses_crash" } } })
`
