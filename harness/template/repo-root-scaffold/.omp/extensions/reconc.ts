// Managed by reconc. Project-local Oh My Pi policy extension.
// Policy, evidence, and continuation decisions stay in Reconc's Go runtime.

import type {
  ExtensionAPI,
  ExtensionContext,
  SessionStopEventResult,
  ToolCallEvent,
  ToolCallEventResult,
  ToolResultEvent,
  UserBashEvent,
  UserBashEventResult,
  UserPythonEvent,
} from "@oh-my-pi/pi-coding-agent"

type JsonObject = Record<string, unknown>

interface RouteBudget {
  timeoutMilliseconds: number
  maxOutputBytes: number
  errorPolicy: "allow" | "block" | "host"
  timeoutPolicy: "allow" | "block" | "host"
  maxContinuations?: number
}

interface RunResult {
  code: number
  stdout: string
  stderr: string
  timedOut: boolean
  aborted: boolean
  truncated: boolean
  invalidUTF8: boolean
}

interface CombinedOutput {
  stdout: string
  stderr: string
  truncated: boolean
  invalidUTF8: boolean
}

const routeBudgets: Record<string, RouteBudget> = {"omp-permission-request":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-permission-result":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-post-compaction":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-post-tool-use":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-post-tool-use-failure":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-pre-compaction":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-pre-tool-use":{"timeoutMilliseconds":10000,"maxOutputBytes":8192,"errorPolicy":"block","timeoutPolicy":"block"},"omp-session-end":{"timeoutMilliseconds":1000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-session-start":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-stop":{"timeoutMilliseconds":29000,"maxOutputBytes":8192,"errorPolicy":"block","timeoutPolicy":"block","maxContinuations":8},"omp-user-bash":{"timeoutMilliseconds":10000,"maxOutputBytes":8192,"errorPolicy":"block","timeoutPolicy":"block"},"omp-user-prompt-submit":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-user-python":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"}}
const defaultBudget: RouteBudget = {
  timeoutMilliseconds: 5000,
  maxOutputBytes: 8192,
  errorPolicy: "block",
  timeoutPolicy: "block",
}

const isRecord = (value: unknown): value is JsonObject =>
  !!value && typeof value === "object" && !Array.isArray(value)

const boundedText = (value: unknown, limit: number): string => {
  const bytes = new TextEncoder().encode(String(value))
  let end = Math.min(bytes.length, Math.max(0, limit))
  while (end > 0) {
    try {
      return new TextDecoder("utf-8", { fatal: true }).decode(bytes.slice(0, end))
    } catch {
      end -= 1
    }
  }
  return ""
}

const readCombined = async (
  stdout: ReadableStream<Uint8Array>,
  stderr: ReadableStream<Uint8Array>,
  limit: number,
  signal: AbortSignal,
): Promise<CombinedOutput> => {
  let remaining = Math.max(0, limit)
  let truncated = false
  const drain = async (stream: ReadableStream<Uint8Array>): Promise<Uint8Array> => {
    const chunks: Uint8Array[] = []
    const reader = stream.getReader()
    while (true) {
      const result = await new Promise<ReadableStreamReadResult<Uint8Array>>((resolve, reject) => {
        const stop = (): void => {
          void reader.cancel()
          resolve({ done: true, value: undefined })
        }
        signal.addEventListener("abort", stop, { once: true })
        reader.read().then(
          (value) => {
            signal.removeEventListener("abort", stop)
            resolve(value)
          },
          (error: unknown) => {
            signal.removeEventListener("abort", stop)
            reject(error)
          },
        )
      })
      if (result.done) break
      const bytes = result.value
      const keep = Math.min(remaining, bytes.length)
      if (keep > 0) {
        chunks.push(bytes.slice(0, keep))
        remaining -= keep
      }
      if (keep < bytes.length) truncated = true
    }
    const size = chunks.reduce((total, chunk) => total + chunk.length, 0)
    const joined = new Uint8Array(size)
    let offset = 0
    for (const chunk of chunks) {
      joined.set(chunk, offset)
      offset += chunk.length
    }
    return joined
  }

  const [stdoutBytes, stderrBytes] = await Promise.all([drain(stdout), drain(stderr)])
  try {
    return {
      stdout: new TextDecoder("utf-8", { fatal: true }).decode(stdoutBytes).trim(),
      stderr: new TextDecoder("utf-8", { fatal: true }).decode(stderrBytes).trim(),
      truncated,
      invalidUTF8: false,
    }
  } catch {
    return {
      stdout: "",
      stderr: boundedText("Reconc emitted invalid UTF-8", limit),
      truncated,
      invalidUTF8: true,
    }
  }
}

const commandFor = async (repo: string, event: string): Promise<string[]> => {
  const wrapper = repo + "/tools/reconc/bin/hook"
  if (await Bun.file(wrapper).exists()) {
    if (process.platform === "win32") return ["sh", wrapper, event, repo]
    return [wrapper, event, repo]
  }
  for (const binary of [repo + "/.build/bin/reconc", repo + "/reconc"]) {
    if (await Bun.file(binary).exists()) {
      if (event === "__worker_v1__") return [binary, "hook", "worker"]
      return [binary, "hook", "runtime", event, repo]
    }
  }
  if (event === "__worker_v1__") return ["reconc", "hook", "worker"]
  return ["reconc", "hook", "runtime", event, repo]
}

const runOneShot = async (
  event: string,
  payload: JsonObject,
  repo: string,
  signal?: AbortSignal,
  timeoutOverride?: number,
): Promise<RunResult> => {
  const budget = routeBudgets[event] ?? defaultBudget
  const outputAbort = new AbortController()
  let timedOut = false
  let aborted = signal?.aborted === true
  let timeoutID: ReturnType<typeof setTimeout> | undefined
  let kill: (() => void) | undefined
  const abort = (): void => {
    aborted = true
    outputAbort.abort()
    kill?.()
  }
  if (aborted) {
    return { code: 1, stdout: "", stderr: "OMP canceled the Reconc hook", timedOut, aborted, truncated: false, invalidUTF8: false }
  }
  signal?.addEventListener("abort", abort, { once: true })
  try {
    const body = JSON.stringify(payload)
    const command = await commandFor(repo, event)
    if (aborted) {
      return { code: 1, stdout: "", stderr: "OMP canceled the Reconc hook", timedOut, aborted, truncated: false, invalidUTF8: false }
    }
    const proc = Bun.spawn(command, {
      cwd: repo,
      stdin: "pipe",
      stdout: "pipe",
      stderr: "pipe",
      killSignal: "SIGKILL",
    })
    kill = (): void => {
      killReconcProcessTree(proc)
    }
    if (aborted) {
      outputAbort.abort()
      kill()
      const code = await proc.exited
      return { code, stdout: "", stderr: "OMP canceled the Reconc hook", timedOut, aborted, truncated: false, invalidUTF8: false }
    }
    proc.stdin.write(body)
    proc.stdin.end()
    timeoutID = setTimeout(() => {
      timedOut = true
      outputAbort.abort()
      kill?.()
    }, timeoutOverride ?? budget.timeoutMilliseconds)
    const [code, output] = await Promise.all([
      proc.exited,
      readCombined(proc.stdout, proc.stderr, budget.maxOutputBytes, outputAbort.signal),
    ])
    return { code, stdout: output.stdout, stderr: output.stderr, timedOut, aborted, truncated: output.truncated, invalidUTF8: output.invalidUTF8 }
  } catch (error: unknown) {
    outputAbort.abort()
    kill?.()
    return {
      code: 1,
      stdout: "",
      stderr: boundedText(error, budget.maxOutputBytes),
      timedOut,
      aborted,
      truncated: false,
      invalidUTF8: false,
    }
  } finally {
    if (timeoutID !== undefined) clearTimeout(timeoutID)
    signal?.removeEventListener("abort", abort)
  }
}

const killReconcProcessTree = (proc) => {
  if (process.platform === "win32" && Number.isSafeInteger(proc?.pid) && proc.pid > 0) {
    try {
      Bun.spawnSync(["taskkill", "/PID", String(proc.pid), "/T", "/F"], {
        stdout: "ignore",
        stderr: "ignore",
      })
    } catch {}
  }
  try { proc.kill("SIGKILL") } catch {}
}

const appendReconcWorkerBytes = (current, right, maxBytes) => {
  const required = current.length + right.length
  if (required > maxBytes) return false
  if (required > current.capacity) {
    const capacity = Math.min(
      maxBytes,
      Math.max(required, Math.max(1024, current.capacity * 2)),
    )
    const grown = new Uint8Array(capacity)
    grown.set(current.buffer.subarray(0, current.length))
    current.buffer = grown
    current.capacity = capacity
  }
  current.buffer.set(right, current.length)
  current.length = required
  return true
}

const createReconcWorkerTransport = (repo, commandFor, runOneShot, canceledMessage) => {
  const workerProtocolVersion = 1
  const maxWorkerResponseBytes = 128 * 1024
  let worker = undefined
  let workerUnsupported = false
  let nextRequestID = 0
  let serial = Promise.resolve()

  const workerError = (kind, message) => Object.assign(new Error(message), { reconcWorkerKind: kind })

  const abortWorker = (current) => {
    if (!current) return
    try { current.process.stdin.end() } catch {}
    killReconcProcessTree(current.process)
    try { current.reader.cancel() } catch {}
  }

  const killWorker = () => {
    const current = worker
    worker = undefined
    abortWorker(current)
  }

  const drainWorkerStderr = async (stream) => {
    try {
      const reader = stream.getReader()
      const chunks = []
      let remaining = 8192
      while (true) {
        const result = await reader.read()
        if (result.done) break
        const bytes = result.value instanceof Uint8Array ? result.value : new Uint8Array(result.value)
        const keep = Math.min(remaining, bytes.length)
        if (keep > 0) chunks.push(bytes.slice(0, keep))
        remaining -= keep
      }
      if (chunks.length > 0) {
        const size = chunks.reduce((total, chunk) => total + chunk.length, 0)
        const joined = new Uint8Array(size)
        let offset = 0
        for (const chunk of chunks) {
          joined.set(chunk, offset)
          offset += chunk.length
        }
        let diagnostic = "Reconc worker emitted invalid UTF-8 diagnostics"
        try { diagnostic = new TextDecoder("utf-8", { fatal: true }).decode(joined).trim() } catch {}
        if (diagnostic) console.error("reconc hook worker: " + diagnostic)
      }
    } catch {}
  }

  const readWorkerLine = async (current) => {
    while (true) {
      const newline = current.buffer.subarray(0, current.length).indexOf(10)
      if (newline >= 0) {
        let line = current.buffer.slice(0, newline)
        const remainder = current.buffer.slice(newline + 1, current.length)
        current.buffer = remainder
        current.length = remainder.length
        current.capacity = remainder.length
        if (line.length > 0 && line[line.length - 1] === 13) line = line.slice(0, -1)
        if (line.length === 0) throw workerError("protocol", "Reconc worker returned an empty frame")
        try {
          return new TextDecoder("utf-8", { fatal: true }).decode(line)
        } catch {
          throw workerError("protocol", "Reconc worker returned invalid UTF-8")
        }
      }
      const next = await current.reader.read()
      if (next.done) throw workerError("crash", "Reconc worker closed stdout")
      const bytes = next.value instanceof Uint8Array ? next.value : new Uint8Array(next.value)
      if (current.length + bytes.length > maxWorkerResponseBytes) {
        throw workerError("protocol", "Reconc worker response exceeded its frame limit")
      }
      if (!appendReconcWorkerBytes(current, bytes, maxWorkerResponseBytes)) {
        throw workerError("protocol", "Reconc worker response exceeded its frame limit")
      }
    }
  }

  const waitForWorkerLine = async (current, timeoutMilliseconds, signal, timeoutKind) => {
    if (signal?.aborted) throw workerError("aborted", canceledMessage)
    let timeoutID
    let abort
    const stopped = new Promise((_, reject) => {
      timeoutID = setTimeout(() => reject(workerError(timeoutKind, "Reconc worker timed out")), Math.max(1, timeoutMilliseconds))
      abort = () => reject(workerError("aborted", canceledMessage))
      signal?.addEventListener("abort", abort, { once: true })
    })
    try {
      return await Promise.race([readWorkerLine(current), stopped])
    } finally {
      clearTimeout(timeoutID)
      signal?.removeEventListener("abort", abort)
    }
  }

  const writeWorkerFrame = (current, frame) => {
    current.process.stdin.write(JSON.stringify(frame) + "\n")
  }

  const parseWorkerResponse = (text, id, expectedType) => {
    let response
    try { response = JSON.parse(text) } catch { throw workerError("protocol", "Reconc worker returned invalid JSON") }
    if (!response || typeof response !== "object" || Array.isArray(response)) {
      throw workerError("protocol", "Reconc worker response is not an object")
    }
    const allowed = new Set(["format_version", "type", "id", "code", "stdout", "stderr", "error"])
    if (Object.keys(response).some((key) => !allowed.has(key)) ||
        response.format_version !== workerProtocolVersion || response.type !== expectedType || response.id !== id ||
        !Number.isSafeInteger(response.code) || response.code < 0 || response.code > 255 ||
        (response.stdout !== undefined && typeof response.stdout !== "string") ||
        (response.stderr !== undefined && typeof response.stderr !== "string") ||
        (response.error !== undefined && typeof response.error !== "string")) {
      throw workerError("protocol", "Reconc worker response contract drifted")
    }
    return response
  }

  const startWorker = async (signal, budgetMilliseconds) => {
    if (worker) return worker
    if (workerUnsupported) throw workerError("unsupported", "Reconc worker protocol is unavailable")
    let process
    let current
    try {
      const command = await commandFor("__worker_v1__")
      process = Bun.spawn(command, {
        cwd: repo,
        stdin: "pipe",
        stdout: "pipe",
        stderr: "pipe",
        killSignal: "SIGKILL",
      })
      current = { process, reader: process.stdout.getReader(), buffer: new Uint8Array(), length: 0, capacity: 0 }
      void drainWorkerStderr(process.stderr)
      const id = "ping-" + (++nextRequestID)
      writeWorkerFrame(current, { format_version: workerProtocolVersion, type: "ping", id })
      const startupBudget = Math.min(2500, Math.max(500, Math.floor(budgetMilliseconds / 2)))
      const response = parseWorkerResponse(
        await waitForWorkerLine(current, startupBudget, signal, "startup"),
        id,
        "response",
      )
      if (response.code !== 0 || response.stdout || response.stderr || response.error) {
        throw workerError("protocol", "Reconc worker handshake was not clean")
      }
      worker = current
      return current
    } catch (error) {
      if (current) abortWorker(current)
      else if (process) killReconcProcessTree(process)
      if (error?.reconcWorkerKind !== "aborted") workerUnsupported = true
      throw error
    }
  }

  const execute = async (event, payload, signal) => {
    const budget = routeBudgets[event] || { timeoutMilliseconds: 5000, maxOutputBytes: 8192 }
    const startedAt = Date.now()
    if (signal?.aborted) {
      return { code: 1, stdout: "", stderr: canceledMessage, timedOut: false, aborted: true, truncated: false, invalidUTF8: false }
    }
    if (workerUnsupported) return runOneShot(event, payload, signal, budget.timeoutMilliseconds)
    let current
    try {
      current = await startWorker(signal, budget.timeoutMilliseconds)
    } catch (error) {
      if (error?.reconcWorkerKind === "aborted") {
        return { code: 1, stdout: "", stderr: canceledMessage, timedOut: false, aborted: true, truncated: false, invalidUTF8: false }
      }
      const remaining = budget.timeoutMilliseconds - (Date.now() - startedAt)
      if (remaining <= 0) {
        return { code: 1, stdout: "", stderr: "Reconc worker startup timed out", timedOut: true, aborted: false, truncated: false, invalidUTF8: false }
      }
      return runOneShot(event, payload, signal, remaining)
    }
    const id = "request-" + (++nextRequestID)
    try {
      writeWorkerFrame(current, {
        format_version: workerProtocolVersion,
        type: "request",
        id,
        event,
        repo,
        payload,
      })
      const remaining = budget.timeoutMilliseconds - (Date.now() - startedAt)
      if (remaining <= 0) throw workerError("timeout", "Reconc worker request timed out")
      const response = parseWorkerResponse(
        await waitForWorkerLine(current, remaining, signal, "timeout"),
        id,
        "response",
      )
      if (response.error) throw workerError("protocol", response.error)
      return {
        code: response.code,
        stdout: response.stdout || "",
        stderr: response.stderr || "",
        timedOut: false,
        aborted: false,
        truncated: false,
        invalidUTF8: false,
      }
    } catch (error) {
      killWorker()
      if (error?.reconcWorkerKind === "aborted") {
        return { code: 1, stdout: "", stderr: canceledMessage, timedOut: false, aborted: true, truncated: false, invalidUTF8: false }
      }
      if (error?.reconcWorkerKind === "timeout") {
        return { code: 1, stdout: "", stderr: "Reconc worker request timed out", timedOut: true, aborted: false, truncated: false, invalidUTF8: false }
      }
      if (error?.reconcWorkerKind === "protocol") workerUnsupported = true
      const remaining = budget.timeoutMilliseconds - (Date.now() - startedAt)
      if (remaining <= 0) {
        return { code: 1, stdout: "", stderr: "Reconc worker failed before a response", timedOut: true, aborted: false, truncated: false, invalidUTF8: false }
      }
      return runOneShot(event, payload, signal, remaining)
    }
  }

  const run = (event, payload, signal) => {
    const pending = serial.then(() => execute(event, payload, signal), () => execute(event, payload, signal))
    serial = pending.then(() => undefined, () => undefined)
    return pending
  }

  const close = async () => {
    await serial
    const current = worker
    if (!current) return
    const id = "shutdown-" + (++nextRequestID)
    try {
      writeWorkerFrame(current, { format_version: workerProtocolVersion, type: "shutdown", id })
      parseWorkerResponse(await waitForWorkerLine(current, 200, undefined, "shutdown"), id, "shutdown")
    } catch {}
    killWorker()
  }

  return { run, close }
}

const workerTransports = new Map<string, ReturnType<typeof createReconcWorkerTransport>>()
const workerTransport = (repo: string): ReturnType<typeof createReconcWorkerTransport> => {
  const existing = workerTransports.get(repo)
  if (existing) return existing
  const created = createReconcWorkerTransport(
    repo,
    (event: string) => commandFor(repo, event),
    (event: string, payload: JsonObject, signal?: AbortSignal, timeout?: number) => runOneShot(event, payload, repo, signal, timeout),
    "OMP canceled the Reconc hook",
  )
  workerTransports.set(repo, created)
  return created
}
const run = (event: string, payload: JsonObject, repo: string, signal?: AbortSignal): Promise<RunResult> =>
  workerTransport(repo).run(event, payload, signal)

const failureReason = (event: string, result: RunResult): string => {
  const budget = routeBudgets[event] ?? defaultBudget
  if (result.aborted) return ""
  if (result.timedOut && budget.timeoutPolicy === "block") return "Reconc timed out while evaluating this OMP event"
  if ((result.invalidUTF8 || result.truncated) && budget.errorPolicy === "block") return "Reconc returned an invalid or oversized OMP response"
  if (result.code !== 0 && budget.errorPolicy === "block") {
    return result.stderr || result.stdout || "Reconc could not evaluate this OMP event"
  }
  return ""
}

const parseObject = (text: string): JsonObject | undefined => {
  try {
    const value: unknown = JSON.parse(text)
    return isRecord(value) ? value : undefined
  } catch {
    return undefined
  }
}

const toolDecision = (event: string, result: RunResult): ToolCallEventResult | undefined => {
  const failure = failureReason(event, result)
  if (failure) return { block: true, reason: failure }
  if (!result.stdout) return undefined
  const body = parseObject(result.stdout)
  if (body?.decision === "block" && typeof body.reason === "string" && body.reason.trim() !== "") {
    return { block: true, reason: body.reason.trim() }
  }
  return { block: true, reason: "Reconc returned an invalid OMP pre-tool response" }
}

const stopDecision = (event: string, result: RunResult): SessionStopEventResult | undefined => {
  if (result.aborted) return undefined
  const failure = failureReason(event, result)
  if (failure) return { decision: "block", reason: failure }
  if (!result.stdout) return undefined
  const body = parseObject(result.stdout)
  if (body?.decision === "block" && typeof body.reason === "string" && body.reason.trim() !== "") {
    return { decision: "block", reason: body.reason.trim() }
  }
  if (body?.continue === true && typeof body.additionalContext === "string" && body.additionalContext.trim() !== "") {
    return { continue: true, additionalContext: body.additionalContext.trim() }
  }
  return { decision: "block", reason: "Reconc returned an invalid OMP Stop response" }
}

const sessionPayload = (ctx: ExtensionContext, hookEventName: string, extra: JsonObject = {}): JsonObject => ({
  hook_event_name: hookEventName,
  session_id: ctx.sessionManager.getSessionId(),
  session_file: ctx.sessionManager.getSessionFile(),
  cwd: ctx.cwd,
  ...extra,
})

const toolPayload = (
  ctx: ExtensionContext,
  event: ToolCallEvent | ToolResultEvent,
  hookEventName: string,
  extra: JsonObject = {},
): JsonObject => sessionPayload(ctx, hookEventName, {
  tool_name: event.toolName,
  tool_input: event.input,
  tool_call_id: event.toolCallId,
  ...extra,
})

export default function ReconcOMPExtension(pi: ExtensionAPI): void {
  const observe = async (event: string, payload: JsonObject, ctx: ExtensionContext): Promise<void> => {
    const result = await run(event, payload, ctx.cwd)
    if (result.code !== 0 || result.timedOut || result.truncated || result.invalidUTF8) {
      pi.logger.warn("Reconc OMP observation failed open", {
        event,
        code: result.code,
        timedOut: result.timedOut,
        truncated: result.truncated,
        error: result.stderr,
      })
    }
  }

  pi.on("session_start", async (_event, ctx) => {
    await observe("omp-session-start", sessionPayload(ctx, "session_start"), ctx)
  })

  pi.on("input", async (event, ctx) => {
    await observe("omp-user-prompt-submit", sessionPayload(ctx, "input", {
      prompt: event.text,
      input_source: event.source,
    }), ctx)
  })

  pi.on("tool_call", async (event, ctx) => {
    const route = "omp-pre-tool-use"
    const result = await run(route, toolPayload(ctx, event, "tool_call"), ctx.cwd)
    return toolDecision(route, result)
  })

  pi.on("user_bash", async (event: UserBashEvent, ctx: ExtensionContext): Promise<UserBashEventResult | undefined> => {
    const route = "omp-user-bash"
    const result = await run(route, sessionPayload(ctx, "user_bash", {
      tool_name: "bash",
      tool_input: { command: event.command },
      user_bash_cwd: event.cwd,
      exclude_from_context: event.excludeFromContext,
    }), ctx.cwd)
    const decision = toolDecision(route, result)
    if (!decision?.block) return undefined
    return {
      result: {
        output: decision.reason || "Reconc blocked this OMP shell command",
        exitCode: 2,
        cancelled: false,
        truncated: false,
      },
    }
  })

  // Observation only: Reconc cannot decide Python source, but an execution
  // surface that stays invisible would make the user_bash gate look wider than
  // it is.
  pi.on("user_python", async (event: UserPythonEvent, ctx: ExtensionContext) => {
    await observe("omp-user-python", sessionPayload(ctx, "user_python", {
      user_python_cwd: event.cwd,
      exclude_from_context: event.excludeFromContext,
      code_bytes: new TextEncoder().encode(event.code).length,
    }), ctx)
  })

  pi.on("tool_approval_requested", async (event, ctx) => {
    await observe("omp-permission-request", sessionPayload(ctx, "tool_approval_requested", {
      session_id: event.sessionId,
      tool_name: event.toolName,
      tool_call_id: event.toolCallId,
      reason: event.reason,
      approval_mode: event.approvalMode,
    }), ctx)
  })

  pi.on("tool_approval_resolved", async (event, ctx) => {
    await observe("omp-permission-result", sessionPayload(ctx, "tool_approval_resolved", {
      session_id: event.sessionId,
      tool_name: event.toolName,
      tool_call_id: event.toolCallId,
      approved: event.approved,
      reason: event.reason,
    }), ctx)
  })

  pi.on("tool_result", async (event, ctx) => {
    const route = event.isError ? "omp-post-tool-use-failure" : "omp-post-tool-use"
    const details = isRecord(event.details) ? event.details : {}
    const exitCode = event.toolName === "bash" && typeof details.exitCode === "number"
      ? details.exitCode
      : event.toolName === "bash" && !event.isError ? 0 : undefined
    const errorText = event.isError
      ? event.content.filter((item) => item.type === "text").map((item) => item.text).join("\n") || "OMP tool execution failed"
      : ""
    await observe(route, toolPayload(ctx, event, "tool_result", {
      is_error: event.isError,
      error: errorText,
      tool_response: {
        content: event.content,
        details: event.details,
        success: !event.isError,
        ...(exitCode === undefined ? {} : { exit_code: exitCode }),
        ...(errorText === "" ? {} : { error: errorText }),
      },
    }), ctx)
  })

  pi.on("session_stop", async (event, ctx) => {
    const route = "omp-stop"
    const result = await run(route, sessionPayload(ctx, "session_stop", {
      session_id: event.session_id,
      session_file: event.session_file,
      turn_id: event.turn_id,
      stop_hook_active: event.stop_hook_active,
    }), ctx.cwd, event.signal)
    return stopDecision(route, result)
  })

  pi.on("auto_compaction_start", async (event, ctx) => {
    await observe("omp-pre-compaction", sessionPayload(ctx, "auto_compaction_start", {
      reason: event.reason,
      action: event.action,
    }), ctx)
  })

  pi.on("auto_compaction_end", async (event, ctx) => {
    await observe("omp-post-compaction", sessionPayload(ctx, "auto_compaction_end", {
      action: event.action,
      aborted: event.aborted,
      will_retry: event.willRetry,
      error: event.errorMessage,
      skipped: event.skipped,
    }), ctx)
  })

  pi.on("session_shutdown", async (_event, ctx) => {
    await observe("omp-session-end", sessionPayload(ctx, "session_shutdown"), ctx)
    await workerTransports.get(ctx.cwd)?.close()
    workerTransports.delete(ctx.cwd)
  })
}
