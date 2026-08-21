package hooks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestWorkerResponseBufferContract(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("Bun is required to verify the worker response buffer: %v", err)
	}
	repo := t.TempDir()
	driverPath := filepath.Join(repo, "worker-buffer-contract.js")
	driver := "const routeBudgets = new Proxy({}, { get: () => ({ timeoutMilliseconds: 2000, maxOutputBytes: 8192 }) })\n" +
		hookWorkerClientSource + workerBufferContractDriver
	if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	workerPath := filepath.Join(repo, "worker-buffer-fake.js")
	if err := os.WriteFile(workerPath, []byte(workerBufferFakeWorker), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(bun, driverPath, workerPath, filepath.Join(repo, "worker-buffer.jsonl"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("worker response buffer contract: %v\n%s", err, output)
	}
}

func BenchmarkWorkerResponseBufferGeometricGrowth(b *testing.B) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		b.Skipf("Bun is required to benchmark the worker response buffer: %v", err)
	}
	repo := b.TempDir()
	driverPath := filepath.Join(repo, "worker-buffer-benchmark.js")
	if err := os.WriteFile(driverPath, []byte(hookWorkerClientSource+workerBufferBenchmarkDriver), 0o644); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	output, err := exec.Command(bun, driverPath, strconv.Itoa(b.N)).Output()
	b.StopTimer()
	if err != nil {
		b.Fatal(err)
	}
	var metrics struct {
		JavaScriptNanosecondsPerOperation float64 `json:"javascript_nanoseconds_per_operation"`
		CopiedBytesPerOperation           float64 `json:"copied_bytes_per_operation"`
	}
	if err := json.Unmarshal(output, &metrics); err != nil {
		b.Fatalf("decode JavaScript benchmark metrics: %v: %q", err, output)
	}
	if metrics.CopiedBytesPerOperation > 128*1024 {
		b.Fatalf("geometric buffer copied %.0f bytes per full frame", metrics.CopiedBytesPerOperation)
	}
	b.ReportMetric(metrics.JavaScriptNanosecondsPerOperation, "js-ns/op")
	b.ReportMetric(metrics.CopiedBytesPerOperation, "copy-B/op")
}

const workerBufferContractDriver = `
const workerProgram = Bun.argv[2]
const logPath = Bun.argv[3]
const maxBytes = 128 * 1024
const assert = (condition, message) => { if (!condition) throw new Error(message) }
const newBuffer = () => ({ buffer: new Uint8Array(), length: 0, capacity: 0 })

const oneByte = newBuffer()
let copiedBytes = 0
for (let index = 0; index < maxBytes; index++) {
  const beforeLength = oneByte.length
  const beforeCapacity = oneByte.capacity
  assert(appendReconcWorkerBytes(oneByte, Uint8Array.of(index % 251), maxBytes), "one-byte append failed")
  if (oneByte.capacity !== beforeCapacity) copiedBytes += beforeLength
}
assert(oneByte.length === maxBytes && oneByte.capacity === maxBytes, "exact-limit buffer was not accepted")
assert(copiedBytes <= maxBytes, "geometric growth copied an unbounded prefix")
const exactBuffer = oneByte.buffer
assert(!appendReconcWorkerBytes(oneByte, Uint8Array.of(1), maxBytes), "overflow append was accepted")
assert(oneByte.length === maxBytes && oneByte.buffer === exactBuffer, "overflow append mutated the buffer")

const irregular = newBuffer()
const expected = new Uint8Array(16391)
for (let index = 0; index < expected.length; index++) expected[index] = index % 239
const sizes = [7, 1025, 3, 4093, 1, 257]
let offset = 0
let sizeIndex = 0
while (offset < expected.length) {
  const end = Math.min(expected.length, offset + sizes[sizeIndex++ % sizes.length])
  assert(appendReconcWorkerBytes(irregular, expected.subarray(offset, end), maxBytes), "irregular append failed")
  offset = end
}
assert(irregular.length === expected.length, "irregular buffer length drifted")
assert(irregular.buffer.subarray(0, irregular.length).every((value, index) => value === expected[index]), "irregular bytes drifted")

const fallbackEvents = []
const commandFor = async () => [process.execPath, workerProgram, logPath]
const runOneShot = async (event) => {
  fallbackEvents.push(event)
  return { code: 0, stdout: "fallback:" + event, stderr: "", timedOut: false, aborted: false, truncated: false, invalidUTF8: false }
}
const transport = createReconcWorkerTransport(process.cwd(), commandFor, runOneShot, "canceled")
assert((await transport.run("one-byte", {}, undefined)).stdout === "one-byte", "one-byte response drifted")
assert((await transport.run("irregular", {}, undefined)).stdout === "irregular", "irregular response drifted")
const exactLimit = await transport.run("exact-limit", {}, undefined)
assert(exactLimit.stdout.length > 130000 && exactLimit.stdout.split("").every((value) => value === "x"), "exact-limit frame drifted")
assert((await transport.run("remainder-first", {}, undefined)).stdout === "remainder-first", "first remainder frame drifted")
assert((await transport.run("remainder-second", {}, undefined)).stdout === "remainder-second", "buffered remainder was not reused")
const controller = new AbortController()
const canceled = transport.run("cancel", {}, controller.signal)
setTimeout(() => controller.abort(), 20)
assert((await canceled).aborted, "cancellation did not abort the request")
assert((await transport.run("restart", {}, undefined)).stdout === "restart", "worker did not restart after cancellation")
await transport.close()

const overflowTransport = createReconcWorkerTransport(process.cwd(), commandFor, runOneShot, "canceled")
assert((await overflowTransport.run("overflow", {}, undefined)).stdout === "fallback:overflow", "overflow did not use one-shot fallback")
await overflowTransport.close()
assert(fallbackEvents.length === 1 && fallbackEvents[0] === "overflow", "unexpected fallback events: " + fallbackEvents)

const records = (await Bun.file(logPath).text()).trim().split("\n").map((line) => JSON.parse(line))
assert(records.filter((record) => record.record === "start").length === 3, "worker start/restart count drifted")
assert(records.filter((record) => record.record === "shutdown").length === 1, "clean shutdown count drifted")
`

const workerBufferFakeWorker = `
import { appendFileSync } from "node:fs"
const logPath = Bun.argv[2]
const record = (value) => appendFileSync(logPath, JSON.stringify(value) + "\n")
const encoder = new TextEncoder()
const decoder = new TextDecoder("utf-8", { fatal: true })
const writeBytes = async (bytes, sizes, pause) => {
  let offset = 0
  let index = 0
  while (offset < bytes.length) {
    const end = Math.min(bytes.length, offset + sizes[index++ % sizes.length])
    process.stdout.write(bytes.subarray(offset, end))
    offset = end
    if (pause) await Bun.sleep(1)
  }
}
const response = (frame, stdout = "") => ({ format_version: 1, type: "response", id: frame.id, code: 0, stdout })
const writeResponse = async (frame, stdout, sizes = [4096], pause = false) => {
  await writeBytes(encoder.encode(JSON.stringify(response(frame, stdout)) + "\n"), sizes, pause)
}

record({ record: "start" })
let buffered = ""
for await (const chunk of Bun.stdin.stream()) {
  buffered += decoder.decode(chunk, { stream: true })
  while (true) {
    const newline = buffered.indexOf("\n")
    if (newline < 0) break
    const frame = JSON.parse(buffered.slice(0, newline))
    buffered = buffered.slice(newline + 1)
    if (frame.type === "shutdown") {
      record({ record: "shutdown" })
      process.stdout.write(JSON.stringify({ format_version: 1, type: "shutdown", id: frame.id, code: 0 }) + "\n")
      process.exit(0)
    }
    if (frame.type === "ping") {
      await writeResponse(frame, "")
      continue
    }
    if (frame.event === "one-byte") {
      await writeResponse(frame, "one-byte", [1], true)
      continue
    }
    if (frame.event === "irregular") {
      await writeResponse(frame, "irregular", [1, 7, 2, 31, 3, 5])
      continue
    }
    if (frame.event === "exact-limit") {
      const base = response(frame, "")
      const empty = JSON.stringify(base)
      const stdout = "x".repeat(128 * 1024 - 1 - encoder.encode(empty).length)
      const line = encoder.encode(JSON.stringify(response(frame, stdout)) + "\n")
      if (line.length !== 128 * 1024) throw new Error("exact-limit fixture length=" + line.length)
      await writeBytes(line, [8191, 17, 4096], false)
      continue
    }
    if (frame.event === "remainder-first") {
      const nextID = "request-" + (Number(frame.id.slice("request-".length)) + 1)
      const first = JSON.stringify(response(frame, "remainder-first")) + "\n"
      const second = JSON.stringify(response({ id: nextID }, "remainder-second")) + "\n"
      process.stdout.write(first + second)
      continue
    }
    if (frame.event === "remainder-second") continue
    if (frame.event === "cancel") {
      await Bun.sleep(10000)
      continue
    }
    if (frame.event === "overflow") {
      const bytes = new Uint8Array(128 * 1024 + 1)
      bytes.fill(120)
      bytes[bytes.length - 1] = 10
      process.stdout.write(bytes)
      continue
    }
    await writeResponse(frame, frame.event)
  }
}
`

const workerBufferBenchmarkDriver = `
const iterations = Math.max(1, Number(Bun.argv[2]))
const maxBytes = 128 * 1024
let copiedBytes = 0
const startedAt = Bun.nanoseconds()
for (let iteration = 0; iteration < iterations; iteration++) {
  const current = { buffer: new Uint8Array(), length: 0, capacity: 0 }
  let chunkIndex = 0
  const sizes = [257, 4093, 17, 1021]
  while (current.length < maxBytes) {
    const size = Math.min(maxBytes - current.length, sizes[chunkIndex++ % sizes.length])
    const beforeLength = current.length
    const beforeCapacity = current.capacity
    if (!appendReconcWorkerBytes(current, new Uint8Array(size), maxBytes)) throw new Error("append failed")
    if (current.capacity !== beforeCapacity) copiedBytes += beforeLength
  }
}
const elapsed = Bun.nanoseconds() - startedAt
console.log(JSON.stringify({
  javascript_nanoseconds_per_operation: elapsed / iterations,
  copied_bytes_per_operation: copiedBytes / iterations,
}))
`
