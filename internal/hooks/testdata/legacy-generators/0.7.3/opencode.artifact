// Managed by reconc. Project-local opencode policy adapter.
// Policy, session state, and continuation decisions stay in the Go runtime.

const fallbackSessionID = globalThis.crypto?.randomUUID?.() ?? "opencode-" + Date.now()
const startedSessions = new Set()
const routeBudgets = {"opencode-permission-request":{"timeoutMilliseconds":10000,"maxOutputBytes":8192,"errorPolicy":"block","timeoutPolicy":"block"},"opencode-post-compaction":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"opencode-post-tool-use":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"opencode-post-tool-use-failure":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"opencode-pre-tool-use":{"timeoutMilliseconds":10000,"maxOutputBytes":8192,"errorPolicy":"block","timeoutPolicy":"block"},"opencode-session-end":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"opencode-session-start":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"opencode-stop":{"timeoutMilliseconds":30000,"maxOutputBytes":8192,"errorPolicy":"block","timeoutPolicy":"allow"}}

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
    case "bash": return "Bash"
    default: return tool || ""
  }
}

export const ReconcOpenCodePlugin = async ({ directory, worktree, client }) => {
  const repo = worktree || directory || process.cwd()
  const wrapper = repo + "/tools/reconc/bin/hook"
  const binaries = [repo + "/.build/bin/reconc", repo + "/reconc"]

  const commandFor = async (event) => {
    if (await Bun.file(wrapper).exists()) return [wrapper, event, repo]
    for (const binary of binaries) {
      if (await Bun.file(binary).exists()) return [binary, "hook", "runtime", event, repo]
    }
    return ["reconc", "hook", "runtime", event, repo]
  }

  const run = async (event, payload) => {
    const budget = routeBudgets[event] || { timeoutMilliseconds: 5000, maxOutputBytes: 8192, errorPolicy: "block", timeoutPolicy: "block" }
    let proc
    let timeoutID
    let timedOut = false
    try {
      proc = Bun.spawn(await commandFor(event), {
        stdin: "pipe",
        stdout: "pipe",
        stderr: "pipe",
        maxBuffer: budget.maxOutputBytes,
        killSignal: "SIGKILL",
      })
      proc.stdin.write(JSON.stringify(payload))
      proc.stdin.end()
      timeoutID = setTimeout(() => {
        timedOut = true
        proc.kill("SIGKILL")
      }, budget.timeoutMilliseconds)
      const [code, stdout, stderr] = await Promise.all([proc.exited, new Response(proc.stdout).text(), new Response(proc.stderr).text()])
      return { code, stdout: stdout.trim(), stderr: stderr.trim(), timedOut }
    } catch (error) {
      if (proc) {
        try { proc.kill("SIGKILL") } catch {}
      }
      return { code: 1, stdout: "", stderr: String(error), timedOut }
    } finally {
      if (timeoutID) clearTimeout(timeoutID)
    }
  }

  const shouldBlockFailure = (event, result) => {
    const budget = routeBudgets[event] || { errorPolicy: "block", timeoutPolicy: "block" }
    if (result.timedOut) return budget.timeoutPolicy === "block"
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

  const toolPayload = (input, output) => {
    const payload = {
      session_id: sessionIDFrom(input) || fallbackSessionID,
      reconc_runtime: "opencode",
      tool_name: normalizeTool(input?.tool || output?.tool),
      tool_input: output?.args || input?.args || {},
      tool_response: output?.result || output?.response || {},
    }
    const error = output?.error || output?.metadata?.error
    if (error) payload.error = String(error)
    return payload
  }

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

  const handleStop = async (event) => {
    const sessionID = await ensureSession(sessionIDFrom(event))
    const stopEvent = "opencode-stop"
    const result = await run(stopEvent, { session_id: sessionID, reconc_runtime: "opencode" })
    if (result.code !== 0 && !shouldBlockFailure(stopEvent, result)) return
    const reason = contextFrom(result)
    if (result.code === 0 && !reason) return
    if (reason && client?.session?.prompt) {
      await client.session.prompt({ path: { id: sessionID }, body: { parts: [{ type: "text", text: reason }] } })
      return
    }
    throw new Error(result.stderr || reason || "reconc blocked session stop")
  }

  return {
    "tool.execute.before": async (input, output) => {
      await ensureSession(sessionIDFrom(input))
      const event = "opencode-pre-tool-use"
      const result = await run(event, toolPayload(input, output))
      if (shouldBlockFailure(event, result)) throw new Error(result.stderr || result.stdout || "reconc blocked tool execution")
    },
    "tool.execute.after": async (input, output) => {
      await ensureSession(sessionIDFrom(input))
      const payload = toolPayload(input, output)
      const event = payload.error ? "opencode-post-tool-use-failure" : "opencode-post-tool-use"
      await run(event, payload)
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
      const result = await run("opencode-post-compaction", { session_id: sessionID, reconc_runtime: "opencode", summary: input?.message?.summary || "" })
      const context = contextFrom(result)
      if (context && Array.isArray(output?.context)) output.context.push(context)
    },
    event: async ({ event }) => {
      const sessionID = sessionIDFrom(event) || fallbackSessionID
      if (event?.type === "session.created") {
        await ensureSession(sessionID)
      } else if (event?.type === "session.compacted") {
        await run("opencode-post-compaction", { session_id: sessionID, reconc_runtime: "opencode" })
      } else if (event?.type === "session.deleted") {
        await run("opencode-session-end", { session_id: sessionID, reconc_runtime: "opencode" })
        startedSessions.delete(sessionID)
      } else if (event?.type === "session.idle") {
        await handleStop(event)
      }
    },
  }
}
