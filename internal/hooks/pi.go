package hooks

import "strings"

func generatePi() (*Artifact, error) {
	routeBudgets, err := bunRouteBudgets(KindPi)
	if err != nil {
		return nil, err
	}
	content := strings.Replace(piExtensionTemplate, "__ROUTE_BUDGETS__", routeBudgets, 1)
	return &Artifact{Kind: KindPi, TargetPath: PiExtensionPath, Content: content}, nil
}

const piExtensionTemplate = `// Managed by reconc. Project-local Pi policy extension.
// Policy, evidence, and continuation decisions stay in Reconc's Go runtime.

import type {
  ExtensionAPI,
  ExtensionContext,
  ToolCallEvent,
  ToolCallEventResult,
  ToolResultEvent,
  UserBashEventResult,
} from "@earendil-works/pi-coding-agent"

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

interface ContinuationState {
  inFlight: boolean
  activityGeneration: number
  handledGeneration: number
  continuationCount: number
  pendingInjectedInputs: number
  lastDelivery: "none" | "requested" | "failed" | "suppressed"
  lastTouched: number
}

const routeBudgets: Record<string, RouteBudget> = __ROUTE_BUDGETS__
const defaultBudget: RouteBudget = {
  timeoutMilliseconds: 5000,
  maxOutputBytes: 8192,
  errorPolicy: "block",
  timeoutPolicy: "block",
}
const continuationStates = new Map<string, ContinuationState>()
const maxContinuationSessions = 1024
let continuationTouch = 0
let capacityDiagnosticActive = false

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
    if (await Bun.file(binary).exists()) return [binary, "hook", "runtime", event, repo]
  }
  return ["reconc", "hook", "runtime", event, repo]
}

const run = async (
  event: string,
  payload: JsonObject,
  repo: string,
  signal?: AbortSignal,
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
    return { code: 1, stdout: "", stderr: "Pi canceled the Reconc hook", timedOut, aborted, truncated: false, invalidUTF8: false }
  }
  signal?.addEventListener("abort", abort, { once: true })
  try {
    const command = await commandFor(repo, event)
    if (aborted) {
      return { code: 1, stdout: "", stderr: "Pi canceled the Reconc hook", timedOut, aborted, truncated: false, invalidUTF8: false }
    }
    const proc = Bun.spawn(command, {
      cwd: repo,
      stdin: "pipe",
      stdout: "pipe",
      stderr: "pipe",
      killSignal: "SIGKILL",
    })
    kill = (): void => {
      try { proc.kill("SIGKILL") } catch {}
    }
    if (aborted) {
      outputAbort.abort()
      kill()
      const code = await proc.exited
      return { code, stdout: "", stderr: "Pi canceled the Reconc hook", timedOut, aborted, truncated: false, invalidUTF8: false }
    }
    proc.stdin.write(JSON.stringify(payload))
    proc.stdin.end()
    timeoutID = setTimeout(() => {
      timedOut = true
      outputAbort.abort()
      kill?.()
    }, budget.timeoutMilliseconds)
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

const failureReason = (event: string, result: RunResult): string => {
  const budget = routeBudgets[event] ?? defaultBudget
  if (result.aborted) return ""
  if (result.timedOut && budget.timeoutPolicy === "block") return "Reconc timed out while evaluating this Pi event"
  if ((result.invalidUTF8 || result.truncated) && budget.errorPolicy === "block") return "Reconc returned an invalid or oversized Pi response"
  if (result.code !== 0 && budget.errorPolicy === "block") {
    return result.stderr || result.stdout || "Reconc could not evaluate this Pi event"
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
  if (result.aborted) return undefined
  const failure = failureReason(event, result)
  if (failure) return { block: true, reason: failure }
  if (!result.stdout) return undefined
  const body = parseObject(result.stdout)
  if (body?.decision === "block" && typeof body.reason === "string" && body.reason.trim() !== "") {
    return { block: true, reason: body.reason.trim() }
  }
  return { block: true, reason: "Reconc returned an invalid Pi pre-tool response" }
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

const continuationState = (sessionID: string): ContinuationState | undefined => {
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
        console.error("reconc Pi continuation state capacity unavailable")
      }
      return undefined
    }
    continuationStates.delete(evictID)
  }
  capacityDiagnosticActive = false
  const state: ContinuationState = {
    inFlight: false,
    activityGeneration: 0,
    handledGeneration: -1,
    continuationCount: 0,
    pendingInjectedInputs: 0,
    lastDelivery: "none",
    lastTouched: ++continuationTouch,
  }
  continuationStates.set(sessionID, state)
  return state
}

const markActivity = (sessionID: string): void => {
  const state = continuationState(sessionID)
  if (state) state.activityGeneration += 1
}

const continuationFrom = (result: RunResult, limit: number): { valid: boolean; reason: string } => {
  if (result.code !== 0 || result.timedOut || result.aborted || result.truncated || result.invalidUTF8) {
    return { valid: false, reason: "" }
  }
  if (!result.stdout) return { valid: true, reason: "" }
  const body = parseObject(result.stdout)
  const candidate = body?.additionalContext ??
    (isRecord(body?.hookSpecificOutput) ? body.hookSpecificOutput.additionalContext : undefined) ??
    body?.reason
  if (typeof candidate !== "string") return { valid: false, reason: "" }
  return { valid: true, reason: boundedText(candidate.trim(), limit) }
}

export default function ReconcPiExtension(pi: ExtensionAPI): void {
  const observe = async (event: string, payload: JsonObject, ctx: ExtensionContext, signal?: AbortSignal): Promise<void> => {
    const result = await run(event, payload, ctx.cwd, signal)
    if (!result.aborted && (result.code !== 0 || result.timedOut || result.truncated || result.invalidUTF8)) {
      console.error("reconc Pi observation failed open: " + event)
    }
  }

  const reportContinuation = async (
    ctx: ExtensionContext,
    state: ContinuationState,
    delivery: "requested" | "failed" | "suppressed",
  ): Promise<void> => {
    state.lastDelivery = delivery
    const result = await run("pi-continuation-" + delivery, sessionPayload(ctx, "agent_settled", {
      continuation_delivery: delivery,
    }), ctx.cwd)
    if (result.code !== 0 || result.timedOut || result.truncated || result.invalidUTF8) {
      console.error("reconc Pi continuation observation failed: " + delivery)
    }
  }

  const handleStop = async (ctx: ExtensionContext): Promise<void> => {
    const state = continuationState(ctx.sessionManager.getSessionId())
    if (!state || state.inFlight) return
    if (state.handledGeneration === state.activityGeneration) {
      if (state.lastDelivery !== "suppressed") await reportContinuation(ctx, state, "suppressed")
      return
    }
    state.handledGeneration = state.activityGeneration
    state.inFlight = true
    state.lastTouched = ++continuationTouch
    try {
      const route = "pi-stop"
      const result = await run(route, sessionPayload(ctx, "agent_settled", {
        stop_hook_active: false,
      }), ctx.cwd)
      const budget = routeBudgets[route] ?? defaultBudget
      const continuation = continuationFrom(result, budget.maxOutputBytes)
      if (!continuation.valid) {
        await reportContinuation(ctx, state, "failed")
        return
      }
      if (!continuation.reason) {
        state.lastDelivery = "none"
        return
      }
      if (!Number.isSafeInteger(budget.maxContinuations) || (budget.maxContinuations ?? 0) <= 0 ||
          state.continuationCount >= (budget.maxContinuations ?? 0)) {
        await reportContinuation(ctx, state, "suppressed")
        return
      }
      state.pendingInjectedInputs += 1
      try {
        pi.sendUserMessage(continuation.reason)
      } catch {
        state.pendingInjectedInputs -= 1
        await reportContinuation(ctx, state, "failed")
        return
      }
      state.continuationCount += 1
      await reportContinuation(ctx, state, "requested")
    } finally {
      state.inFlight = false
      state.lastTouched = ++continuationTouch
    }
  }

  pi.on("session_start", async (event, ctx) => {
    continuationState(ctx.sessionManager.getSessionId())
    await observe("pi-session-start", sessionPayload(ctx, "session_start", {
      reason: event.reason,
      previous_session_file: event.previousSessionFile,
    }), ctx)
  })

  pi.on("input", async (event, ctx) => {
    const state = continuationState(ctx.sessionManager.getSessionId())
    if (event.source === "extension" && state && state.pendingInjectedInputs > 0) {
      state.pendingInjectedInputs -= 1
    } else {
      markActivity(ctx.sessionManager.getSessionId())
    }
    await observe("pi-user-prompt-submit", sessionPayload(ctx, "input", {
      prompt: event.text,
      input_source: event.source,
      streaming_behavior: event.streamingBehavior,
      image_count: event.images?.length ?? 0,
    }), ctx, ctx.signal)
  })

  pi.on("tool_call", async (event, ctx) => {
    markActivity(ctx.sessionManager.getSessionId())
    const route = "pi-pre-tool-use"
    const result = await run(route, toolPayload(ctx, event, "tool_call"), ctx.cwd, ctx.signal)
    return toolDecision(route, result)
  })

  pi.on("tool_result", async (event, ctx) => {
    const route = event.isError ? "pi-post-tool-use-failure" : "pi-post-tool-use"
    const errorText = event.isError
      ? event.content.filter((item) => item.type === "text").map((item) => item.text).join("\n") || "Pi tool execution failed"
      : ""
    const exitCode = event.toolName === "bash" && !event.isError ? 0 : undefined
    await observe(route, toolPayload(ctx, event, "tool_result", {
      is_error: event.isError,
      error: errorText,
      tool_response: {
        content: event.content,
        details: event.details,
        usage: event.usage,
        success: !event.isError,
        ...(exitCode === undefined ? {} : { exit_code: exitCode }),
        ...(errorText === "" ? {} : { error: errorText }),
      },
    }), ctx, ctx.signal)
  })

  pi.on("user_bash", async (event, ctx): Promise<UserBashEventResult | undefined> => {
    markActivity(ctx.sessionManager.getSessionId())
    const route = "pi-user-bash"
    const result = await run(route, sessionPayload(ctx, "user_bash", {
      tool_name: "bash",
      tool_input: { command: event.command },
      user_bash_cwd: event.cwd,
      exclude_from_context: event.excludeFromContext,
    }), ctx.cwd, ctx.signal)
    const decision = toolDecision(route, result)
    if (!decision?.block) return undefined
    return {
      result: {
        output: decision.reason || "Reconc blocked this Pi shell command",
        exitCode: 2,
        cancelled: false,
        truncated: false,
      },
    }
  })

  pi.on("session_before_compact", async (event, ctx) => {
    await observe("pi-pre-compaction", sessionPayload(ctx, "session_before_compact", {
      reason: event.reason,
      will_retry: event.willRetry,
    }), ctx, event.signal)
  })

  pi.on("session_compact", async (event, ctx) => {
    await observe("pi-post-compaction", sessionPayload(ctx, "session_compact", {
      reason: event.reason,
      will_retry: event.willRetry,
      from_extension: event.fromExtension,
    }), ctx)
  })

  pi.on("agent_settled", async (_event, ctx) => {
    await handleStop(ctx)
  })

  pi.on("session_shutdown", async (event, ctx) => {
    await observe("pi-session-end", sessionPayload(ctx, "session_shutdown", {
      reason: event.reason,
      target_session_file: event.targetSessionFile,
    }), ctx)
    continuationStates.delete(ctx.sessionManager.getSessionId())
  })
}
`
