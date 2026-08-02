// Managed by reconc. Project-local Oh My Pi policy extension.
// Policy, evidence, and continuation decisions stay in Reconc's Go runtime.

import type {
  ExtensionAPI,
  ExtensionContext,
  SessionStopEventResult,
  ToolCallEvent,
  ToolCallEventResult,
  ToolResultEvent,
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

const routeBudgets: Record<string, RouteBudget> = {"omp-permission-request":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-permission-result":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-post-compaction":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-post-tool-use":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-post-tool-use-failure":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-pre-compaction":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-pre-tool-use":{"timeoutMilliseconds":10000,"maxOutputBytes":8192,"errorPolicy":"block","timeoutPolicy":"block"},"omp-session-end":{"timeoutMilliseconds":1000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-session-start":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"omp-stop":{"timeoutMilliseconds":29000,"maxOutputBytes":8192,"errorPolicy":"block","timeoutPolicy":"block","maxContinuations":8},"omp-user-prompt-submit":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"}}
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
      try { proc.kill("SIGKILL") } catch {}
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
  })
}
