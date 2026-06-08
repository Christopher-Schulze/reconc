// Managed by reconc. Project-local OpenCode plugin.
// Uses the repo-local Reconc binary when present; PATH is only a fallback.

const sessionID = globalThis.crypto?.randomUUID?.() ?? "opencode-" + Date.now()
const maxNoProgressNudges = 8

const resolveRepoRoot = async (candidate) => {
  const proc = Bun.spawn(["git", "-C", candidate, "rev-parse", "--show-toplevel"], {
    stdout: "pipe",
    stderr: "pipe",
  })
  const [code, stdout] = await Promise.all([
    proc.exited,
    new Response(proc.stdout).text(),
  ])
  if (code === 0) {
    const root = stdout.trim()
    if (root !== "") return root
  }
  return candidate
}

export const ReconcOpenCodePlugin = async ({ directory, worktree, client }) => {
  const repo = await resolveRepoRoot(worktree || directory || process.cwd())
  const reconcPlatform =
    process.platform === "darwin" ? "darwin" :
    process.platform === "linux" ? "linux" :
    process.platform === "win32" ? "windows" : ""
  const reconcArch =
    process.arch === "arm64" ? "arm64" :
    process.arch === "x64" ? "amd64" : ""
  const reconcReleaseName =
    reconcPlatform !== "" && reconcArch !== ""
      ? "reconc-0.5.0-" + reconcPlatform + "-" + reconcArch + (reconcPlatform === "windows" ? ".exe" : "")
      : ""
  const reconcBinaryCandidates = reconcReleaseName === "" ? [] : [
    repo + "/tools/reconc/dist/" + reconcReleaseName,
    repo + "/dist/" + reconcReleaseName,
  ]
  const reconcArgs = (event) => ["hook", "runtime", event, repo]
  const stateDir = repo + "/.reconc/runloop"
  const stateFile = stateDir + "/state.json"
  const stopFile = stateDir + "/stop"
  let sessionStarted = false
  let nudgeInFlight = false

  const run = async (event, payload) => {
    let bin = "reconc"
    for (const candidate of reconcBinaryCandidates) {
      if (await Bun.file(candidate).exists()) {
        bin = candidate
        break
      }
    }
    const proc = Bun.spawn([bin, ...reconcArgs(event)], {
      stdin: "pipe",
      stdout: "pipe",
      stderr: "pipe",
    })
    proc.stdin.write(JSON.stringify(payload))
    proc.stdin.end()
    const [code, stdout, stderr] = await Promise.all([
      proc.exited,
      new Response(proc.stdout).text(),
      new Response(proc.stderr).text(),
    ])
    return { code, stdout, stderr }
  }

  const runCommand = async (args) => {
    const proc = Bun.spawn(args, {
      cwd: repo,
      stdout: "pipe",
      stderr: "pipe",
    })
    const [code, stdout, stderr] = await Promise.all([
      proc.exited,
      new Response(proc.stdout).text(),
      new Response(proc.stderr).text(),
    ])
    return { code, stdout: stdout.trim(), stderr: stderr.trim() }
  }

  const ensureStateDir = async () => {
    await runCommand(["mkdir", "-p", stateDir])
  }

  const readStopMarker = async () => {
    if (!(await Bun.file(stopFile).exists())) return { exists: false, legacy: false }
    const text = (await Bun.file(stopFile).text()).trim()
    if (text === "") return { exists: true, legacy: true }
    try {
      const marker = JSON.parse(text)
      return {
        exists: true,
        legacy: false,
        session_id: typeof marker.session_id === "string" ? marker.session_id.trim() : "",
        active_run_id: typeof marker.active_run_id === "string" ? marker.active_run_id.trim() : "",
      }
    } catch {
      return { exists: true, legacy: true }
    }
  }

  const stopMarkerMatchesState = (marker, state) => {
    if (!marker?.exists) return false
    if (marker.legacy) return true
    if (marker.active_run_id && state.active_run_id) return marker.active_run_id === state.active_run_id
    if (marker.session_id) return marker.session_id === state.session_id || marker.session_id === state.active_run_id
    return false
  }

  const sameActiveRun = (state, openCodeSessionID) => {
    const session = String(openCodeSessionID || "").trim()
    if (!session) return false
    if (state.active_run_id) return state.active_run_id === session
    return state.session_id === session
  }

  const readState = async () => {
    if (!(await Bun.file(stateFile).exists())) {
      return {
        enabled: false,
        session_id: "",
        active_run_id: "",
        no_progress_nudges: 0,
        disabled_reason: "",
        stop_anchor_message_id: "",
        awaiting_continuation: false,
      }
    }
    try {
      const state = JSON.parse(await Bun.file(stateFile).text())
      if (typeof state.enabled !== "boolean") state.enabled = false
      if (typeof state.no_progress_nudges !== "number") state.no_progress_nudges = 0
      if (typeof state.session_id !== "string") state.session_id = ""
      if (typeof state.active_run_id !== "string") state.active_run_id = state.session_id || ""
      if (typeof state.disabled_reason !== "string") state.disabled_reason = ""
      if (typeof state.stop_anchor_message_id !== "string") state.stop_anchor_message_id = ""
      if (typeof state.awaiting_continuation !== "boolean") state.awaiting_continuation = false
      const stopMarker = await readStopMarker()
      const stopApplies = stopMarkerMatchesState(stopMarker, state)

      if ((state.disabled_reason || stopApplies) && state.enabled) {
        state.enabled = false
        state.no_progress_nudges = 0
        state.active_run_id = ""
        state.awaiting_continuation = false
        if (stopApplies) state.disabled_reason = "stop_file"
      }

      if (!state.enabled) {
        state.no_progress_nudges = 0
        state.awaiting_continuation = false
      }

      return state
    } catch {
      return {
        enabled: false,
        session_id: "",
        active_run_id: "",
        no_progress_nudges: 0,
        disabled_reason: "invalid_state_json",
        stop_anchor_message_id: "",
        awaiting_continuation: false,
      }
    }
  }

  const writeState = async (state) => {
    await ensureStateDir()
    await Bun.write(stateFile, JSON.stringify({
      ...state,
      updated_at: new Date().toISOString(),
    }, null, 2) + "\n")
  }

  const disableRunloop = async (state, reason, context = {}) => {
    await writeState({
      ...state,
      ...context,
      enabled: false,
      active_run_id: "",
      no_progress_nudges: 0,
      disabled_reason: reason,
      awaiting_continuation: false,
    })
  }

  const enableRunloop = async (state, openCodeSessionID) => {
    const session = openCodeSessionID || state.session_id || state.active_run_id || ""
    await writeState({
      ...state,
      enabled: true,
      session_id: session,
      active_run_id: session,
      no_progress_nudges: 0,
      stop_anchor_message_id: "",
      disabled_reason: "",
      awaiting_continuation: false,
      last_prompt_at: new Date().toISOString(),
    })
  }

  const clearStopFile = async () => {
    if (!(await Bun.file(stopFile).exists())) return
    await runCommand(["rm", "-f", stopFile])
  }

  const setStopFile = async (sessionID, activeRunID, reason) => {
    await runCommand(["mkdir", "-p", stateDir])
    await Bun.write(stopFile, JSON.stringify({
      session_id: sessionID || "",
      active_run_id: activeRunID || sessionID || "",
      reason: reason || "",
    }) + "\n")
  }

  const messageIdentity = (value) => {
    const candidates = [
      value?.id,
      value?.message?.id,
      value?.info?.id,
      value?.message_id,
      value?.messageId,
      value?.uuid,
      value?.message?.uuid,
      value?.meta?.id,
    ]
    for (const candidate of candidates) {
      if (typeof candidate === "string" && candidate.trim() !== "") return candidate.trim()
    }
    if (typeof value?.created_at === "string" && value.created_at.trim() !== "") return "created:" + value.created_at.trim()
    if (typeof value?.createdAt === "string" && value.createdAt.trim() !== "") return "created:" + value.createdAt.trim()
    if (typeof value?.timestamp === "string" && value.timestamp.trim() !== "") return "created:" + value.timestamp.trim()
    return ""
  }

  const markUserInterrupt = async (event) => {
    if (!isUserInterruptEvent(event)) return false
    const state = await readState()
    const openCodeSessionID = findSessionID(event)
    if (state.enabled && !sameActiveRun(state, openCodeSessionID)) return false
    const latestMessage = await latestUserMessage(openCodeSessionID)
    await disableRunloop({
      ...state,
      session_id: openCodeSessionID || state.session_id || "",
      stop_anchor_message_id: latestMessage?.signature || "",
    }, "user_interrupt")
    await setStopFile(openCodeSessionID || state.session_id || "", state.active_run_id || openCodeSessionID || "", "user_interrupt")
    return true
  }

  const isUserInterruptEvent = (event) => {
    const type = event?.type
    if (type === "session.interrupted_by_user") return true
    if (type === "tui.command.execute" && event?.properties?.command === "session.interrupt") return true
    if (type === "session.error" && event?.properties?.error?.name === "MessageAbortedError") return true
    return false
  }

  const isSessionErrorEvent = (event) => event?.type === "session.error" && !isUserInterruptEvent(event)

  const ensureSession = async () => {
    if (sessionStarted) return
    const result = await run("opencode-session-start", { session_id: sessionID })
    if (result.code !== 0) throw new Error(result.stderr || result.stdout || "reconc OpenCode session-start failed")
    sessionStarted = true
  }

  const normalizeTool = (tool) => {
    switch (tool) {
      case "read": return "Read"
      case "write": return "Write"
      case "edit": return "Edit"
      case "multiedit": return "MultiEdit"
      case "bash": return "Bash"
      default: return tool || ""
    }
  }

  const payload = (input, output) => ({
    session_id: sessionID,
    tool_name: normalizeTool(input?.tool || output?.tool),
    tool_input: output?.args || input?.args || {},
    tool_response: output?.result || output?.response || {},
  })

  const permissionPayload = (input) => {
    const patterns = Array.isArray(input?.pattern) ? input.pattern : [input?.pattern].filter(Boolean)
    const firstPattern = patterns.find((item) => typeof item === "string" && item.trim() !== "") || ""
    const metadata = input?.metadata || {}
    return {
      session_id: input?.sessionID || sessionID,
      tool_name: normalizeTool(input?.type || metadata.tool || metadata.tool_name || metadata.toolName),
      tool_input: {
        file_path: firstPattern,
        command: input?.title || firstPattern,
        pattern: patterns,
        metadata,
      },
    }
  }

  const findSessionID = (value, depth = 0) => {
    if (!value || depth > 6) return ""
    if (typeof value === "string") return value.startsWith("ses_") ? value : ""
    if (Array.isArray(value)) {
      for (const item of value) {
        const found = findSessionID(item, depth + 1)
        if (found) return found
      }
      return ""
    }
    if (typeof value !== "object") return ""
    for (const key of ["sessionID", "sessionId", "session_id", "session"]) {
      const found = findSessionID(value[key], depth + 1)
      if (found) return found
    }
    for (const item of Object.values(value)) {
      const found = findSessionID(item, depth + 1)
      if (found) return found
    }
    return ""
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
    for (const key of ["parts", "children"]) {
      collectText(value[key], output)
    }
    return output
  }

  const isSyntheticMessage = (message) => {
    const candidates = [
      message?.synthetic,
      message?.is_synthetic,
      message?.isSynthetic,
      message?.metadata?.synthetic,
      message?.metadata?.compaction_continue,
      message?.metadata?.compactionContinue,
      message?.metadata?.source,
      message?.info?.synthetic,
      message?.info?.is_synthetic,
      message?.info?.isSynthetic,
      message?.info?.metadata?.synthetic,
      message?.info?.metadata?.compaction_continue,
      message?.info?.metadata?.compactionContinue,
      message?.info?.metadata?.source,
      message?.message?.synthetic,
      message?.message?.is_synthetic,
      message?.message?.isSynthetic,
      message?.message?.metadata?.synthetic,
      message?.message?.metadata?.compaction_continue,
      message?.message?.metadata?.compactionContinue,
      message?.message?.metadata?.source,
    ]

    return candidates.some((value) => {
      if (value === true || value === 1) return true
      if (typeof value === "string") {
        if (value.toLowerCase() === "compaction") return true
      }
      return false
    })
  }

  const messagesList = (response) => {
    const data = response?.data ?? response
    if (Array.isArray(data)) return data
    if (Array.isArray(data?.data)) return data.data
    if (Array.isArray(data?.messages)) return data.messages
    return []
  }

  const isUserRole = (role) => {
    if (typeof role !== "string") return false
    const normalized = role.toLowerCase().trim()
    return normalized === "user" || normalized === "human"
  }

  const latestUserMessage = async (openCodeSessionID) => {
    if (!client?.session?.messages || !openCodeSessionID) return null
    const response = await client.session.messages({ path: { id: openCodeSessionID } })
    const messages = messagesList(response)
    for (let index = messages.length - 1; index >= 0; index--) {
      const message = messages[index]
      const role = message?.info?.role || message?.role || message?.message?.role || ""
      if (isSyntheticMessage(message)) continue
      const text = collectText(message?.parts || message).join("\n")
      if (!text) continue
      if (isUserRole(role) || (!role && hasExplicitRunText(text))) {
        return { text, signature: messageIdentity(message) || text }
      }
    }
    return null
  }

  const partsText = (parts) => collectText(parts).join("\n")

  const handleUserPromptText = async (openCodeSessionID, text, messageID) => {
    await ensureSession()
    let state = await readState()
    const targetSessionID = openCodeSessionID || state.session_id || state.active_run_id || sessionID
    if (hasExplicitRunText(text)) {
      await clearStopFile()
      await enableRunloop({ ...state, stop_anchor_message_id: "" }, targetSessionID)
      return
    }
    if (state.enabled && !sameActiveRun(state, targetSessionID)) return
    await disableRunloop({
      ...state,
      session_id: targetSessionID,
      stop_anchor_message_id: messageID || "",
    }, "user_prompt")
    await setStopFile(targetSessionID, state.active_run_id || targetSessionID, "user_prompt")
  }

  const currentTask = async () => {
    const path = repo + "/docs/tasks.md"
    if (!(await Bun.file(path).exists())) return ""
    const content = await Bun.file(path).text()
    const match = content.match(/^Current:\s+(TASK-[0-9]{4}-[A-Za-z0-9-]+)\s+->\s+(tasks\/TASK-[0-9]{4}-[A-Za-z0-9-]+\.md)$/m)
    return match?.[1] || ""
  }

  const currentHead = async () => {
    const result = await runCommand(["git", "rev-parse", "HEAD"])
    return result.code === 0 ? result.stdout : ""
  }

  const currentDirtyState = async () => {
    const result = await runCommand(["git", "status", "--porcelain=v1"])
    return result.code === 0 ? result.stdout : "unknown"
  }

const legacyRunLoopPrompt = (task, head) => "runloop autocontinue. Continue the repository task lifecycle without asking for routine permission. No ceremony, no confirmation questions - just work.\n\n" +
    "Active: TASK = " + (task || "read docs/tasks.md Current") + ". Read the live Current: pointer in docs/tasks.md yourself.\n\n" +
    "Quality gate (mandatory before any Done):\n" +
    "- Brutal efficient, performance- and efficiency-maximized, secure (deny-by-default, fail-closed), maintainable.\n" +
    "- NO gaps, nothing forgotten: implement every spec atom or own it via a concrete follow-up TASK. Never declare NO_SPEC_SURFACE without grepping docs/spec.md first.\n" +
    "- Read and adapt the Research Refs (a floor, not inspiration) before coding.\n" +
    "- Max out each feature's intended effect - innovative, not the smallest runnable approximation.\n" +
    "- Integrate into existing project subsystems; never build a parallel/duplicate system (grep for the existing mechanism first).\n" +
    "- Same-TASK substantive tests, then a real Final Reality Check + Contradiction Check with concrete file:line evidence. Verify goal by goal, atomically - no sampling.\n" +
    "- Exactly one commit per TASK including git rm of the archived task path; never bundle TASKs, never stack uncommitted work.\n" +
    "- After every completed TASK, run the per-TASK Reality-Check loop in docs/task-loop-workflow.md before continuing: fresh-eyes, strict, paranoid, forensically deep, LINE BY LINE - zero guessing, nothing from memory, no sampling or spot-checks; verify every goal and every changed line hard and explicitly. Check for gaps; is this REALLY EXACTLY what we wanted or something else; does it honestly meet our quality standards (Hard Quality Mandate)? If there is any potential work, ALWAYS do it, then re-run the loop - repeat until the honest hard Reality-Check finds nothing left to do. Only then continue.\n\n" +
    "After a completed TASK promote/resume the next executable TASK and continue immediately.\n" +
    "Stop only for: user stop, destructive/high-risk choice, missing credentials/access, unresolved test/build failure after root-cause attempts, Reconc/spec/policy conflict needing user direction, repeated no-progress, or the zero-finding Terminal Gate in workflow-complete-loop.md.\n" +
    "Never auto-push. Never touch _drop/, research/, or README.md unless explicitly instructed."

const runLoopPrompt = (_task, _head) => "🔥 STFU & LET ME COOK! 🔥"

  const hasExplicitRunText = (text) => String(text || "").split(/\s+/).some((field) => {
    const token = field.replace(/^[\s.,;:!?()[\]{}<>]+|[\s.,;:!?()[\]{}<>]+$/g, "")
    return token === "/runloop"
  })

  const maybeAutocontinue = async (event, stopResult) => {
    if (stopResult?.stdout && stopResult.stdout.includes('\"decision\":\"block\"')) return
    if (nudgeInFlight) return

    const openCodeSessionID = findSessionID(event)
    const state = await readState()

    const stopMarker = await readStopMarker()
    if (stopMarkerMatchesState(stopMarker, state)) {
      await disableRunloop(state, "stop_file")
      return
    }

    if (!state.enabled) return

    const targetSessionID = openCodeSessionID || state.active_run_id || state.session_id
    if (!targetSessionID) return

    if (!client?.session?.prompt) return

    const task = await currentTask()
    const head = await currentHead()
    const dirtyState = await currentDirtyState()
    if (!task || !head) {
      return
    }

    if (state.active_run_id && state.active_run_id !== targetSessionID && state.session_id !== targetSessionID) return

    const progress = task + "\n" + dirtyState
    const noProgress = state.last_head === head && state.last_current === progress
    const noProgressNudges = noProgress ? (state.no_progress_nudges || 0) + 1 : 0
    if (noProgressNudges >= maxNoProgressNudges) {
      await disableRunloop({ ...state, session_id: targetSessionID, last_head: head, last_current: progress, no_progress_nudges: noProgressNudges }, "no_progress_guard")
      return
    }

    nudgeInFlight = true
    await writeState({
      ...state,
      enabled: true,
      session_id: targetSessionID,
      active_run_id: targetSessionID,
      last_head: head,
      last_current: progress,
      no_progress_nudges: noProgressNudges,
      awaiting_continuation: true,
      last_prompt_at: new Date().toISOString(),
    })
    try {
      await client.session.prompt({
        path: { id: targetSessionID },
        body: {
          parts: [{ type: "text", text: runLoopPrompt(task, head) }],
        },
      })
    } finally {
      nudgeInFlight = false
    }
  }

  return {
    "chat.message": async (input, output) => {
      const text = partsText(output?.parts || [])
      await handleUserPromptText(input?.sessionID || sessionID, text, input?.messageID || output?.message?.id || "")
    },
    "tool.execute.before": async (input, output) => {
      await ensureSession()
      const result = await run("opencode-pre-tool-use", payload(input, output))
      if (result.code !== 0) throw new Error(result.stderr || result.stdout || "reconc blocked OpenCode tool execution")
    },
    "tool.execute.after": async (input, output) => {
      await ensureSession()
      await run("opencode-post-tool-use", payload(input, output))
    },
    "permission.ask": async (input, output) => {
      await ensureSession()
      const result = await run("opencode-pre-tool-use", permissionPayload(input))
      if (result.code !== 0) output.status = "deny"
    },
    event: async ({ event }) => {
      if (await markUserInterrupt(event)) return
      if (isSessionErrorEvent(event)) {
        const state = await readState()
        const eventSessionID = findSessionID(event) || state.session_id || ""
        if (!state.enabled || sameActiveRun(state, eventSessionID)) {
          await disableRunloop({ ...state, session_id: eventSessionID }, "session_error")
          await setStopFile(eventSessionID, state.active_run_id || eventSessionID, "session_error")
        }
        return
      }
      if (event?.type !== "session.idle") return
      await ensureSession()
      const result = await run("opencode-stop", { session_id: sessionID, opencode_continuation_driver: true })
      if (result.code !== 0) return
      if (result.stdout && result.stdout.includes('\"decision\":\"block\"')) {
        throw new Error(result.stdout)
      }
      await maybeAutocontinue(event, result)
    },
  }
}
