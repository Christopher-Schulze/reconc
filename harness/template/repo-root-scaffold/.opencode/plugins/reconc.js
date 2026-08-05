// Managed by reconc. Project-local opencode policy adapter.
// Policy, session state, and continuation decisions stay in the Go runtime.

const fallbackSessionID = globalThis.crypto?.randomUUID?.() ?? "opencode-" + Date.now()
const startedSessions = new Set()
const terminalToolFailures = new Set()
const maxRememberedToolFailures = 1024
const routeBudgets = {"opencode-continuation-accepted":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow","maxContinuations":10},"opencode-continuation-failed":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow","maxContinuations":10},"opencode-continuation-suppressed":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow","maxContinuations":10},"opencode-continuation-unavailable":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow","maxContinuations":10},"opencode-permission-request":{"timeoutMilliseconds":10000,"maxOutputBytes":8192,"errorPolicy":"block","timeoutPolicy":"block"},"opencode-post-compaction":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"opencode-post-tool-use":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"opencode-post-tool-use-failure":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"opencode-pre-compaction":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"opencode-pre-tool-use":{"timeoutMilliseconds":10000,"maxOutputBytes":8192,"errorPolicy":"block","timeoutPolicy":"block"},"opencode-session-end":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"opencode-session-start":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"opencode-stop":{"timeoutMilliseconds":30000,"maxOutputBytes":8192,"errorPolicy":"block","timeoutPolicy":"allow","maxContinuations":10},"opencode-user-prompt-submit":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"}}
const continuationStates = new Map()
const maxContinuationSessions = 1024
let continuationTouch = 0
let capacityDiagnosticActive = false

const sessionIDFrom = (value, depth = 0) => {
  if (!value || depth > 6) return ""
  if (typeof value === "string") return value.startsWith("ses_") ? value : ""
  if (Array.isArray(value)) {
    for (const item of value) {
      const found = sessionIDFrom(item, depth + 1)
      if (found) return found
    }
    return ""
  }
  if (typeof value !== "object") return ""
  for (const key of ["sessionID", "sessionId", "session_id", "session"]) {
    const candidate = value[key]
    if (typeof candidate === "string" && candidate.trim() !== "") return candidate.trim()
    const found = sessionIDFrom(value[key], depth + 1)
    if (found) return found
  }
  for (const item of Object.values(value)) {
    const found = sessionIDFrom(item, depth + 1)
    if (found) return found
  }
  return ""
}

const normalizeTool = (tool) => {
  switch (String(tool || "").toLowerCase()) {
    case "read": return "Read"
    case "write": return "Write"
    case "edit": return "Edit"
    case "multiedit": return "MultiEdit"
    case "bash":
    case "shell": return "Bash"
    default: return tool || ""
  }
}

export const ReconcOpenCodePlugin = async ({ directory, worktree, client }) => {
  const repo = worktree || directory || process.cwd()
  const wrapper = repo + "/tools/reconc/bin/hook"
  const binaries = [repo + "/.build/bin/reconc", repo + "/reconc"]

  const commandFor = async (event) => {
    if (await Bun.file(wrapper).exists()) {
      if (process.platform === "win32") return ["sh", wrapper, event, repo]
      return [wrapper, event, repo]
    }
    for (const binary of binaries) {
      if (await Bun.file(binary).exists()) {
        if (event === "__worker_v1__") return [binary, "hook", "worker"]
        return [binary, "hook", "runtime", event, repo]
      }
    }
    if (event === "__worker_v1__") return ["reconc", "hook", "worker"]
    return ["reconc", "hook", "runtime", event, repo]
  }

  const boundedText = (value, limit) => {
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

  const readCombined = async (stdout, stderr, limit, signal) => {
    const marker = new TextEncoder().encode("\n[reconc output truncated]")
    let remaining = Math.max(0, limit)
    let truncated = false
    const drain = async (stream) => {
      const chunks = []
      const reader = stream.getReader()
      while (true) {
        const { done, value } = await new Promise((resolve, reject) => {
          const stop = () => {
            reader.cancel().catch(() => {})
            resolve({ done: true })
          }
          signal.addEventListener("abort", stop, { once: true })
          reader.read().then(
            (result) => {
              signal.removeEventListener("abort", stop)
              resolve(result)
            },
            (error) => {
              signal.removeEventListener("abort", stop)
              reject(error)
            },
          )
        })
        if (done) break
        const bytes = value instanceof Uint8Array ? value : new Uint8Array(value)
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
    let stdoutKeep = stdoutBytes.length
    let stderrKeep = stderrBytes.length
    if (truncated && limit >= marker.length) {
      const removeFromStderr = Math.min(stderrKeep, marker.length)
      stderrKeep -= removeFromStderr
      stdoutKeep -= Math.min(stdoutKeep, marker.length - removeFromStderr)
    }
    let stdoutText
    let stderrText
    try {
      stdoutText = new TextDecoder("utf-8", { fatal: true }).decode(stdoutBytes.slice(0, stdoutKeep))
      stderrText = new TextDecoder("utf-8", { fatal: true }).decode(stderrBytes.slice(0, stderrKeep))
    } catch {
      return {
        stdout: "",
        stderr: boundedText("[reconc invalid UTF-8 output]", limit).trim(),
        truncated,
        invalidUTF8: true,
      }
    }
    const suffix = truncated && limit >= marker.length ? new TextDecoder().decode(marker) : ""
    return { stdout: stdoutText.trim(), stderr: (stderrText + suffix).trim(), truncated, invalidUTF8: false }
  }

  const runOneShot = async (event, payload, _signal, timeoutOverride) => {
    const budget = routeBudgets[event] || { timeoutMilliseconds: 5000, maxOutputBytes: 8192, errorPolicy: "block", timeoutPolicy: "block" }
    let proc
    let timeoutID
    let timedOut = false
    const outputAbort = new AbortController()
    try {
      proc = Bun.spawn(await commandFor(event), {
        stdin: "pipe",
        stdout: "pipe",
        stderr: "pipe",
        killSignal: "SIGKILL",
      })
      proc.stdin.write(JSON.stringify(payload))
      proc.stdin.end()
      timeoutID = setTimeout(() => {
        timedOut = true
        outputAbort.abort()
        proc.kill("SIGKILL")
      }, timeoutOverride || budget.timeoutMilliseconds)
      const [code, output] = await Promise.all([proc.exited, readCombined(proc.stdout, proc.stderr, budget.maxOutputBytes, outputAbort.signal)])
      return { code, stdout: output.stdout, stderr: output.stderr, timedOut, truncated: output.truncated, invalidUTF8: output.invalidUTF8 }
    } catch (error) {
      if (proc) {
        outputAbort.abort()
        try { proc.kill("SIGKILL") } catch {}
      }
      return { code: 1, stdout: "", stderr: boundedText(error, budget.maxOutputBytes), timedOut, truncated: false, invalidUTF8: false }
    } finally {
      if (timeoutID) clearTimeout(timeoutID)
    }
  }

  const createReconcWorkerTransport = (repo, commandFor, runOneShot, canceledMessage) => {
  const workerProtocolVersion = 1
  const maxWorkerResponseBytes = 128 * 1024
  let worker = undefined
  let workerUnsupported = false
  let nextRequestID = 0
  let serial = Promise.resolve()

  const workerError = (kind, message) => Object.assign(new Error(message), { reconcWorkerKind: kind })

  const killWorker = () => {
    const current = worker
    worker = undefined
    if (!current) return
    try { current.process.stdin.end() } catch {}
    try { current.process.kill("SIGKILL") } catch {}
    try { current.reader.cancel() } catch {}
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

  const appendBytes = (left, right) => {
    const joined = new Uint8Array(left.length + right.length)
    joined.set(left)
    joined.set(right, left.length)
    return joined
  }

  const readWorkerLine = async (current) => {
    while (true) {
      const newline = current.buffer.indexOf(10)
      if (newline >= 0) {
        let line = current.buffer.slice(0, newline)
        current.buffer = current.buffer.slice(newline + 1)
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
      if (current.buffer.length + bytes.length > maxWorkerResponseBytes) {
        throw workerError("protocol", "Reconc worker response exceeded its frame limit")
      }
      current.buffer = appendBytes(current.buffer, bytes)
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
    const command = await commandFor("__worker_v1__")
    const process = Bun.spawn(command, {
      cwd: repo,
      stdin: "pipe",
      stdout: "pipe",
      stderr: "pipe",
      killSignal: "SIGKILL",
    })
    const current = { process, reader: process.stdout.getReader(), buffer: new Uint8Array() }
    worker = current
    void drainWorkerStderr(process.stderr)
    const id = "ping-" + (++nextRequestID)
    writeWorkerFrame(current, { format_version: workerProtocolVersion, type: "ping", id })
    try {
      const startupBudget = Math.min(2500, Math.max(500, Math.floor(budgetMilliseconds / 2)))
      const response = parseWorkerResponse(
        await waitForWorkerLine(current, startupBudget, signal, "startup"),
        id,
        "response",
      )
      if (response.code !== 0 || response.stdout || response.stderr || response.error) {
        throw workerError("protocol", "Reconc worker handshake was not clean")
      }
      return current
    } catch (error) {
      killWorker()
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

  const workerTransport = createReconcWorkerTransport(repo, commandFor, runOneShot, "Host canceled the Reconc hook")
  const run = (event, payload) => workerTransport.run(event, payload)

  const shouldBlockFailure = (event, result) => {
    const budget = routeBudgets[event] || { errorPolicy: "block", timeoutPolicy: "block" }
    if (result.timedOut) return budget.timeoutPolicy === "block"
    if (result.invalidUTF8) return budget.errorPolicy === "block"
    return result.code !== 0 && budget.errorPolicy === "block"
  }

  const ensureSession = async (sessionID) => {
    const id = sessionID || fallbackSessionID
    if (startedSessions.has(id)) return id
    const event = "opencode-session-start"
    const result = await run(event, { session_id: id, reconc_runtime: "opencode" })
    if (shouldBlockFailure(event, result)) throw new Error(result.stderr || result.stdout || "reconc session start failed")
    if (result.code === 0) startedSessions.add(id)
    return id
  }

  const toolPayload = (input, output, phase) => {
    const toolName = normalizeTool(input?.tool || output?.tool)
    const hostTool = typeof input?.tool === "string" ? input.tool : typeof output?.tool === "string" ? output.tool : ""
    const toolInput = output?.args || input?.args || {}
    const metadata = output?.metadata && typeof output.metadata === "object" && !Array.isArray(output.metadata) ? output.metadata : {}
    const response = {
      title: output?.title || "",
      output: output?.output ?? output?.result ?? output?.response ?? "",
      metadata,
    }
    if (toolName === "Bash") {
      const hasExit = Object.hasOwn(metadata, "exit")
      const exit = metadata.exit
      const directExit = output?.exit_code ?? output?.exitCode
      if (hasExit && Number.isSafeInteger(exit) && (directExit === undefined || directExit === exit)) {
        response.exit_code = exit
        response.success = exit === 0
      } else {
        response.success = false
        response.error = hasExit && exit !== null ? "invalid authoritative shell exit status" : "missing authoritative shell exit status"
      }
      if (output?.error) {
        response.success = false
        response.error = String(output.error)
      }
    }
    const completedOutcome = output?.isError === true || output?.error || response.success === false
      ? "failure"
      : "success"
    const payload = {
      session_id: sessionIDFrom(input) || fallbackSessionID,
      reconc_runtime: "opencode",
      tool_name: toolName,
      tool_input: toolInput,
      tool_response: response,
      reconc_mcp: {
        platform: "opencode",
        tool: hostTool,
        observed: false,
        blocking_pre_hook: true,
        input_valid: !!toolInput && typeof toolInput === "object" && !Array.isArray(toolInput),
        ...(phase === "after" ? { outcome: completedOutcome } : {}),
      },
    }
    return payload
  }

  const failurePayload = (part) => ({
    session_id: part?.sessionID || fallbackSessionID,
    reconc_runtime: "opencode",
    tool_name: normalizeTool(part?.tool),
    tool_input: part?.state?.input || {},
    tool_response: { error: part?.state?.error || "tool execution failed", metadata: part?.state?.metadata || {} },
    error: String(part?.state?.error || "tool execution failed"),
    reconc_mcp: {
      platform: "opencode",
      tool: typeof part?.tool === "string" ? part.tool : "",
      observed: false,
      blocking_pre_hook: true,
      input_valid: !!part?.state?.input && typeof part.state.input === "object" && !Array.isArray(part.state.input),
      outcome: "failure",
    },
  })

  const denied = (event, result) => {
    if (shouldBlockFailure(event, result)) return true
    if (result.code !== 0) return false
    if (!result.stdout) return false
    try {
      const body = JSON.parse(result.stdout)
      return body?.behavior === "deny" || body?.permissionDecision === "deny" || body?.hookSpecificOutput?.decision?.behavior === "deny"
    } catch {
      return false
    }
  }

  const contextFrom = (result) => {
    if (!result?.stdout) return ""
    try {
      const body = JSON.parse(result.stdout)
      return body?.additionalContext || body?.hookSpecificOutput?.additionalContext || body?.reason || ""
    } catch {
      return ""
    }
  }

  const continuationFrom = (result, limit) => {
    if (!result || result.code !== 0 || result.timedOut || result.truncated || result.invalidUTF8) {
      return { valid: false, reason: "" }
    }
    if (!result.stdout) return { valid: true, reason: "" }
    try {
      const body = JSON.parse(result.stdout)
      const candidate = body?.additionalContext || body?.hookSpecificOutput?.additionalContext || body?.reason || ""
      if (typeof candidate !== "string") return { valid: false, reason: "" }
      return { valid: true, reason: boundedText(candidate.trim(), limit) }
    } catch {
      return { valid: false, reason: "" }
    }
  }

  const collectText = (value, output = []) => {
    if (!value) return output
    if (typeof value === "string") {
      output.push(value)
      return output
    }
    if (Array.isArray(value)) {
      for (const item of value) collectText(item, output)
      return output
    }
    if (typeof value !== "object") return output
    for (const key of ["text", "content", "message"]) {
      if (typeof value[key] === "string") output.push(value[key])
    }
    for (const key of ["parts", "children"]) collectText(value[key], output)
    return output
  }

  const rememberTerminalFailure = (part) => {
    const key = [part?.sessionID, part?.messageID, part?.callID || part?.id].filter(Boolean).join(":")
    if (!key || terminalToolFailures.has(key)) return false
    if (terminalToolFailures.size >= maxRememberedToolFailures) terminalToolFailures.delete(terminalToolFailures.values().next().value)
    terminalToolFailures.add(key)
    return true
  }

  const continuationState = (sessionID) => {
    const current = continuationStates.get(sessionID)
    if (current) {
      current.lastTouched = ++continuationTouch
      return current
    }
    if (continuationStates.size >= maxContinuationSessions) {
      let evictID = ""
      let evictTouch = Number.POSITIVE_INFINITY
      for (const [candidateID, candidate] of continuationStates) {
        if (!candidate.inFlight && candidate.lastTouched < evictTouch) {
          evictID = candidateID
          evictTouch = candidate.lastTouched
        }
      }
      if (!evictID) {
        if (!capacityDiagnosticActive) {
          capacityDiagnosticActive = true
          console.error("reconc continuation: state capacity unavailable")
        }
        return undefined
      }
      continuationStates.delete(evictID)
    }
    capacityDiagnosticActive = false
    const state = {
      inFlight: false,
      activityGeneration: 0,
      handledGeneration: -1,
      continuationCount: 0,
      lastDelivery: "none",
      lastTouched: ++continuationTouch,
      injectedMessageIDs: new Set(),
    }
    continuationStates.set(sessionID, state)
    return state
  }

  const sessionHash = (sessionID) => {
    let hash = 2166136261
    for (const byte of new TextEncoder().encode(sessionID)) {
      hash ^= byte
      hash = Math.imul(hash, 16777619)
    }
    return (hash >>> 0).toString(16).padStart(8, "0")
  }

  const reportContinuation = async (sessionID, state, delivery) => {
    state.lastDelivery = delivery
    const event = "opencode-continuation-" + delivery
    await run(event, {
      session_id: sessionID,
      reconc_runtime: "opencode",
      continuation_delivery: delivery,
    })
    if (delivery !== "accepted") {
      console.error("reconc continuation: host=opencode session=" + sessionHash(sessionID) + " route=opencode-stop delivery=" + delivery)
    }
  }

  const markActivity = (sessionID) => {
    const state = continuationState(sessionID)
    if (!state) return
    state.activityGeneration += 1
  }

  const handleStop = async (event) => {
    const sessionID = await ensureSession(sessionIDFrom(event))
    const state = continuationState(sessionID)
    if (!state) return
    if (state.inFlight) return
    if (state.handledGeneration === state.activityGeneration) {
      if (state.lastDelivery !== "suppressed") await reportContinuation(sessionID, state, "suppressed")
      return
    }
    state.handledGeneration = state.activityGeneration
    state.inFlight = true
    state.lastTouched = ++continuationTouch
    const stopEvent = "opencode-stop"
    try {
      const result = await run(stopEvent, { session_id: sessionID, reconc_runtime: "opencode" })
      const budget = routeBudgets[stopEvent] || { maxOutputBytes: 8192, maxContinuations: 0 }
      if (result.code !== 0 || result.timedOut) {
        await reportContinuation(sessionID, state, "failed")
        return
      }
      const continuation = continuationFrom(result, budget.maxOutputBytes)
      if (!continuation.valid) {
        await reportContinuation(sessionID, state, "failed")
        return
      }
      if (!continuation.reason) {
        state.lastDelivery = "none"
        return
      }
      if (!Number.isSafeInteger(budget.maxContinuations) || budget.maxContinuations <= 0 ||
          state.continuationCount >= budget.maxContinuations) {
        await reportContinuation(sessionID, state, "suppressed")
        return
      }
      if (typeof client?.session?.promptAsync !== "function") {
        await reportContinuation(sessionID, state, "unavailable")
        return
      }
      const messageID = "msg_reconc_" + crypto.randomUUID().replaceAll("-", "")
      state.injectedMessageIDs.add(messageID)
      let acceptance
      try {
        acceptance = await client.session.promptAsync({
          sessionID,
          messageID,
          parts: [{ type: "text", text: continuation.reason }],
        })
      } catch {
        state.injectedMessageIDs.delete(messageID)
        await reportContinuation(sessionID, state, "failed")
        return
      }
      if (!acceptance || acceptance.error || acceptance.response?.ok !== true || acceptance.response?.status !== 204) {
        state.injectedMessageIDs.delete(messageID)
        await reportContinuation(sessionID, state, "failed")
        return
      }
      state.continuationCount += 1
      await reportContinuation(sessionID, state, "accepted")
    } finally {
      state.inFlight = false
      state.lastTouched = ++continuationTouch
    }
  }

  return {
    "chat.message": async (input, output) => {
      const sessionID = await ensureSession(input?.sessionID)
      const state = continuationState(sessionID)
      const injected = state?.injectedMessageIDs?.delete(input?.messageID) === true
      if (!injected) markActivity(sessionID)
      await run("opencode-user-prompt-submit", {
        session_id: sessionID,
        reconc_runtime: "opencode",
        prompt: collectText(output?.parts || []).join("\n"),
      })
    },
    "tool.execute.before": async (input, output) => {
      const sessionID = await ensureSession(sessionIDFrom(input))
      markActivity(sessionID)
      const event = "opencode-pre-tool-use"
      const result = await run(event, toolPayload(input, output, "before"))
      if (shouldBlockFailure(event, result)) throw new Error(result.stderr || result.stdout || "reconc blocked tool execution")
    },
    "tool.execute.after": async (input, output) => {
      await ensureSession(sessionIDFrom(input))
      await run("opencode-post-tool-use", toolPayload(input, output, "after"))
    },
    "permission.ask": async (input, output) => {
      const sessionID = await ensureSession(sessionIDFrom(input))
      const patterns = Array.isArray(input?.pattern) ? input.pattern : [input?.pattern].filter(Boolean)
      const event = "opencode-permission-request"
      const result = await run(event, {
        session_id: sessionID,
        reconc_runtime: "opencode",
        tool_name: normalizeTool(input?.type || input?.metadata?.tool),
        tool_input: { file_path: patterns[0] || "", command: input?.title || patterns[0] || "", pattern: patterns },
      })
      if (denied(event, result)) output.status = "deny"
    },
    "experimental.session.compacting": async (input, output) => {
      const sessionID = await ensureSession(sessionIDFrom(input))
      const result = await run("opencode-pre-compaction", { session_id: sessionID, reconc_runtime: "opencode" })
      const context = contextFrom(result)
      if (context && Array.isArray(output?.context)) output.context.push(context)
    },
    event: async ({ event }) => {
      const sessionID = sessionIDFrom(event) || fallbackSessionID
      if (event?.type === "message.part.updated") {
        const part = event?.properties?.part
        if (part?.type === "tool" && part?.state?.status === "error" && rememberTerminalFailure(part)) {
          await ensureSession(part?.sessionID || sessionID)
          await run("opencode-post-tool-use-failure", failurePayload(part))
        }
      } else if (event?.type === "session.created") {
        await ensureSession(sessionID)
      } else if (event?.type === "session.compacted") {
        await run("opencode-post-compaction", { session_id: sessionID, reconc_runtime: "opencode" })
      } else if (event?.type === "session.deleted") {
        await run("opencode-session-end", { session_id: sessionID, reconc_runtime: "opencode" })
        startedSessions.delete(sessionID)
        continuationStates.delete(sessionID)
        if (startedSessions.size === 0) await workerTransport.close()
      } else if (event?.type === "session.idle") {
        await handleStop(event)
      }
    },
  }
}
