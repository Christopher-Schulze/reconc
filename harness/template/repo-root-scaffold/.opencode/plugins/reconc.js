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
      if (await Bun.file(binary).exists()) return [binary, "hook", "runtime", event, repo]
    }
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

  const run = async (event, payload) => {
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
      }, budget.timeoutMilliseconds)
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
      } else if (event?.type === "session.idle") {
        await handleStop(event)
      }
    },
  }
}
