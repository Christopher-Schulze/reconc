package hooks

const hookWorkerClientSource = `const killReconcProcessTree = (proc) => {
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
`
