package hooks

import (
	"encoding/json"
	"fmt"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
)

func generateDevinCLI() (*Artifact, error) {
	timeouts, err := requiredTimeouts(KindDevinCLI,
		EventSessionStart, EventUserPromptSubmit, EventPreToolUse,
		EventPermissionRequest, EventPostToolUse, EventStop,
		EventSessionEnd, EventPostCompaction,
	)
	if err != nil {
		return nil, err
	}
	command := func(event string, lifecycle Event) map[string]interface{} {
		return map[string]interface{}{
			"type":    "command",
			"command": fmt.Sprintf(`"$DEVIN_PROJECT_DIR/tools/reconc/bin/hook" %s "$DEVIN_PROJECT_DIR"`, event),
			"timeout": timeouts[lifecycle],
		}
	}
	entry := func(event string, lifecycle Event, matcher string) map[string]interface{} {
		out := map[string]interface{}{"hooks": []interface{}{command(event, lifecycle)}}
		if matcher != "" {
			out["matcher"] = matcher
		}
		return out
	}
	template := map[string]interface{}{
		"SessionStart":      []interface{}{entry("devin-session-start", EventSessionStart, "")},
		"UserPromptSubmit":  []interface{}{entry("devin-user-prompt-submit", EventUserPromptSubmit, "")},
		"PreToolUse":        []interface{}{entry("devin-pre-tool-use", EventPreToolUse, "^(exec|edit)$")},
		"PermissionRequest": []interface{}{entry("devin-permission-request", EventPermissionRequest, "^(exec|edit)$")},
		"PostToolUse":       []interface{}{entry("devin-post-tool-use", EventPostToolUse, "^(read|edit|grep|glob|exec|mcp__.*)$")},
		"Stop":              []interface{}{entry("devin-stop", EventStop, "")},
		"SessionEnd":        []interface{}{entry("devin-session-end", EventSessionEnd, "")},
		"PostCompaction":    []interface{}{entry("devin-post-compaction", EventPostCompaction, "")},
	}
	data, _ := json.MarshalIndent(template, "", "  ")
	return &Artifact{Kind: KindDevinCLI, TargetPath: DevinHooksPath, Content: string(data) + "\n"}, nil
}

func generateOpenCodeThin() (*Artifact, error) {
	return generateBunAgentPlugin(KindOpenCode, OpenCodePluginPath, "opencode", false)
}

func generateGitHubCopilot() (*Artifact, error) {
	timeouts, err := requiredTimeouts(KindGitHubCopilot,
		EventSessionStart, EventUserPromptSubmit, EventPreToolUse,
		EventPermissionRequest, EventPostToolUse, EventPostToolUseFailure,
		EventStop, EventSessionEnd, EventNotification, EventSubagentStart,
		EventSubagentStop, EventPreCompaction,
	)
	if err != nil {
		return nil, err
	}
	command := func(event string, lifecycle Event, matcher string) map[string]interface{} {
		bashCommand := fmt.Sprintf("tools/reconc/bin/hook %s .", event)
		powershellCommand := fmt.Sprintf(`& sh "tools/reconc/bin/hook" "%s" "."; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }`, event)
		if lifecycle == EventStop || lifecycle == EventSubagentStop {
			reason := "Reconc could not evaluate this GitHub Copilot stop. Reinstall the GitHub Copilot hook and tools/reconc/bin/hook."
			fallback := fmt.Sprintf(`{"decision":"block","reason":%q}`, reason)
			bashCommand += fmt.Sprintf(` || printf '%%s\n' '%s'`, fallback)
			powershellCommand = fmt.Sprintf(`if (Get-Command sh -ErrorAction SilentlyContinue) { & sh "tools/reconc/bin/hook" "%s" "."; if ($LASTEXITCODE -ne 0) { Write-Output '%s'; exit 0 } } else { Write-Output '%s'; exit 0 }`, event, fallback, fallback)
		}
		entry := map[string]interface{}{
			"type":       "command",
			"bash":       bashCommand,
			"powershell": powershellCommand,
			"cwd":        ".",
			"timeoutSec": timeouts[lifecycle],
		}
		if matcher != "" {
			entry["matcher"] = matcher
		}
		return entry
	}
	const evidenceTools = "Read|Bash|Edit|Write"
	const guardedTools = "Bash|Edit|Write"
	template := map[string]interface{}{
		"version": 1,
		"hooks": map[string]interface{}{
			"SessionStart":       []interface{}{command("copilot-session-start", EventSessionStart, "")},
			"UserPromptSubmit":   []interface{}{command("copilot-user-prompt-submit", EventUserPromptSubmit, "")},
			"PreToolUse":         []interface{}{command("copilot-pre-tool-use", EventPreToolUse, guardedTools)},
			"PermissionRequest":  []interface{}{command("copilot-permission-request", EventPermissionRequest, guardedTools)},
			"PostToolUse":        []interface{}{command("copilot-post-tool-use", EventPostToolUse, evidenceTools)},
			"PostToolUseFailure": []interface{}{command("copilot-post-tool-use-failure", EventPostToolUseFailure, evidenceTools)},
			"Stop":               []interface{}{command("copilot-stop", EventStop, "")},
			"SessionEnd":         []interface{}{command("copilot-session-end", EventSessionEnd, "")},
			"Notification":       []interface{}{command("copilot-notification", EventNotification, "")},
			"subagentStart":      []interface{}{command("copilot-subagent-start", EventSubagentStart, "")},
			"SubagentStop":       []interface{}{command("copilot-subagent-stop", EventSubagentStop, "")},
			"PreCompact":         []interface{}{command("copilot-pre-compaction", EventPreCompaction, "")},
		},
	}
	data, _ := json.MarshalIndent(template, "", "  ")
	return &Artifact{Kind: KindGitHubCopilot, TargetPath: GitHubCopilotHooksPath, Content: string(data) + "\n"}, nil
}

func generateKilo() (*Artifact, error) {
	return generateBunAgentPlugin(KindKilo, KiloPluginPath, "kilo", true)
}

func generateGrok() (*Artifact, error) {
	timeouts, err := requiredTimeouts(KindGrok,
		EventSessionStart, EventUserPromptSubmit, EventPreToolUse,
		EventPostToolUse, EventPostToolUseFailure, EventPermissionDenied,
		EventStop, EventStopFailure, EventNotification, EventSubagentStart,
		EventSubagentStop, EventPreCompaction, EventPostCompaction, EventSessionEnd,
	)
	if err != nil {
		return nil, err
	}
	command := func(event string, lifecycle Event) map[string]interface{} {
		commandText := fmt.Sprintf("tools/reconc/bin/hook %s .", event)
		if lifecycle == EventPreToolUse {
			commandText += ` || printf '%s\n' '{"decision":"deny","reason":"Reconc could not evaluate this Grok tool call. Reinstall the Grok hook and tools/reconc/bin/hook."}'`
		} else if lifecycle == EventStop {
			commandText += ` || printf '%s\n' '{"decision":"block","reason":"Reconc could not evaluate this Grok Stop. Reinstall the Grok hook and tools/reconc/bin/hook."}'`
		}
		return map[string]interface{}{
			"type":    "command",
			"command": commandText,
			"timeout": timeouts[lifecycle],
		}
	}
	entry := func(event string, lifecycle Event, matcher string) map[string]interface{} {
		group := map[string]interface{}{
			"hooks": []interface{}{command(event, lifecycle)},
		}
		if matcher != "" {
			group["matcher"] = matcher
		}
		return group
	}
	const evidenceTools = "^(read_file|hashline_read|grep|hashline_grep|list_dir|write|search_replace|hashline_edit|run_terminal_command|run_terminal_cmd)$"
	const guardedTools = "^(write|search_replace|hashline_edit|run_terminal_command|run_terminal_cmd)$"
	template := map[string]interface{}{
		"reconcManaged": true,
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				entry("grok-session-start", EventSessionStart, ""),
			},
			"UserPromptSubmit": []interface{}{
				entry("grok-user-prompt-submit", EventUserPromptSubmit, ""),
			},
			"PreToolUse": []interface{}{
				entry("grok-pre-tool-use", EventPreToolUse, guardedTools),
			},
			"PostToolUse": []interface{}{
				entry("grok-post-tool-use", EventPostToolUse, evidenceTools),
			},
			"PostToolUseFailure": []interface{}{
				entry("grok-post-tool-use-failure", EventPostToolUseFailure, evidenceTools),
			},
			"PermissionDenied": []interface{}{
				entry("grok-permission-denied", EventPermissionDenied, guardedTools),
			},
			"Stop": []interface{}{
				entry("grok-stop", EventStop, ""),
			},
			"StopFailure": []interface{}{
				entry("grok-stop-failure", EventStopFailure, ""),
			},
			"Notification": []interface{}{
				entry("grok-notification", EventNotification, ""),
			},
			"SubagentStart": []interface{}{
				entry("grok-subagent-start", EventSubagentStart, ""),
			},
			"SubagentStop": []interface{}{
				entry("grok-subagent-stop", EventSubagentStop, ""),
			},
			"PreCompact": []interface{}{
				entry("grok-pre-compaction", EventPreCompaction, ""),
			},
			"PostCompact": []interface{}{
				entry("grok-post-compaction", EventPostCompaction, ""),
			},
			"SessionEnd": []interface{}{
				entry("grok-session-end", EventSessionEnd, ""),
			},
		},
	}
	data, _ := json.MarshalIndent(template, "", "  ")
	return &Artifact{Kind: KindGrok, TargetPath: GrokHooksPath, Content: string(data) + "\n"}, nil
}

func generateBunAgentPlugin(kind, targetPath, prefix string, descriptorExport bool) (*Artifact, error) {
	routeBudgets, err := bunRouteBudgets(kind)
	if err != nil {
		return nil, err
	}
	exportHead := "export const ReconcOpenCodePlugin ="
	exportTail := ""
	if descriptorExport {
		exportHead = "const ReconcKiloServer ="
		exportTail = `

export default { id: "reconc", server: ReconcKiloServer }`
	}
	content := strings.NewReplacer(
		"__EXPORT_HEAD__", exportHead,
		"__EXPORT_TAIL__", exportTail,
		"__PREFIX__", prefix,
		"__ROUTE_BUDGETS__", routeBudgets,
		"__WORKER_CLIENT__", hookWorkerClientSource,
	).Replace(bunAgentPluginTemplate)
	return &Artifact{Kind: kind, TargetPath: targetPath, Content: content}, nil
}

type bunRouteBudget struct {
	TimeoutMilliseconds int           `json:"timeoutMilliseconds"`
	MaxOutputBytes      int           `json:"maxOutputBytes"`
	ErrorPolicy         FailurePolicy `json:"errorPolicy"`
	TimeoutPolicy       FailurePolicy `json:"timeoutPolicy"`
	MaxContinuations    int           `json:"maxContinuations,omitempty"`
}

func bunRouteBudgets(kind string) (string, error) {
	platform, ok := PlatformForKind(kind)
	if !ok {
		return "", hookGeneratorError("missing hook platform: %s", kind)
	}
	budgets := map[string]bunRouteBudget{}
	for _, capability := range platform.Capabilities {
		for _, binding := range capability.Bindings {
			if binding.RuntimeEvent == "" {
				continue
			}
			budgets[binding.RuntimeEvent] = bunRouteBudget{
				TimeoutMilliseconds: capability.TimeoutSeconds * 1000,
				MaxOutputBytes:      capability.MaxOutputBytes,
				ErrorPolicy:         capability.ErrorPolicy,
				TimeoutPolicy:       capability.TimeoutPolicy,
				MaxContinuations:    capability.MaxContinuations,
			}
		}
	}
	data, err := json.Marshal(budgets)
	if err != nil {
		return "", hookGeneratorError("marshal hook route budgets: %v", err)
	}
	return string(data), nil
}

func requiredTimeouts(kind string, events ...Event) (map[Event]int, error) {
	platform, ok := PlatformForKind(kind)
	if !ok {
		return nil, hookGeneratorError("missing hook platform: %s", kind)
	}
	timeouts := make(map[Event]int, len(events))
	for _, event := range events {
		for _, capability := range platform.Capabilities {
			if capability.Event == event && capability.TimeoutSeconds > 0 {
				timeouts[event] = capability.TimeoutSeconds
				break
			}
		}
		if timeouts[event] == 0 {
			return nil, hookGeneratorError("missing timeout for %s %s", kind, event)
		}
	}
	return timeouts, nil
}

func hookGeneratorError(format string, args ...interface{}) error {
	return &rerrors.PolicySourceError{Message: fmt.Sprintf(format, args...)}
}

const bunAgentPluginTemplate = `// Managed by reconc. Project-local __PREFIX__ policy adapter.
// Policy, session state, and continuation decisions stay in the Go runtime.

const fallbackSessionID = globalThis.crypto?.randomUUID?.() ?? "__PREFIX__-" + Date.now()
const startedSessions = new Set()
const terminalToolFailures = new Set()
const maxRememberedToolFailures = 1024
const routeBudgets = __ROUTE_BUDGETS__
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

__EXPORT_HEAD__ async ({ directory, worktree, client }) => {
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
        killReconcProcessTree(proc)
      }, timeoutOverride || budget.timeoutMilliseconds)
      const [code, output] = await Promise.all([proc.exited, readCombined(proc.stdout, proc.stderr, budget.maxOutputBytes, outputAbort.signal)])
      return { code, stdout: output.stdout, stderr: output.stderr, timedOut, truncated: output.truncated, invalidUTF8: output.invalidUTF8 }
    } catch (error) {
      if (proc) {
        outputAbort.abort()
        killReconcProcessTree(proc)
      }
      return { code: 1, stdout: "", stderr: boundedText(error, budget.maxOutputBytes), timedOut, truncated: false, invalidUTF8: false }
    } finally {
      if (timeoutID) clearTimeout(timeoutID)
    }
  }

  __WORKER_CLIENT__
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
    const event = "__PREFIX__-session-start"
    const result = await run(event, { session_id: id, reconc_runtime: "__PREFIX__" })
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
      reconc_runtime: "__PREFIX__",
      tool_name: toolName,
      tool_input: toolInput,
      tool_response: response,
      reconc_mcp: {
        platform: "__PREFIX__",
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
    reconc_runtime: "__PREFIX__",
    tool_name: normalizeTool(part?.tool),
    tool_input: part?.state?.input || {},
    tool_response: { error: part?.state?.error || "tool execution failed", metadata: part?.state?.metadata || {} },
    error: String(part?.state?.error || "tool execution failed"),
    reconc_mcp: {
      platform: "__PREFIX__",
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
    const event = "__PREFIX__-continuation-" + delivery
    await run(event, {
      session_id: sessionID,
      reconc_runtime: "__PREFIX__",
      continuation_delivery: delivery,
    })
    if (delivery !== "accepted") {
      console.error("reconc continuation: host=__PREFIX__ session=" + sessionHash(sessionID) + " route=__PREFIX__-stop delivery=" + delivery)
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
    const stopEvent = "__PREFIX__-stop"
    try {
      const result = await run(stopEvent, { session_id: sessionID, reconc_runtime: "__PREFIX__" })
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
      await run("__PREFIX__-user-prompt-submit", {
        session_id: sessionID,
        reconc_runtime: "__PREFIX__",
        prompt: collectText(output?.parts || []).join("\n"),
      })
    },
    "tool.execute.before": async (input, output) => {
      const sessionID = await ensureSession(sessionIDFrom(input))
      markActivity(sessionID)
      const event = "__PREFIX__-pre-tool-use"
      const result = await run(event, toolPayload(input, output, "before"))
      if (shouldBlockFailure(event, result)) throw new Error(result.stderr || result.stdout || "reconc blocked tool execution")
    },
    "tool.execute.after": async (input, output) => {
      await ensureSession(sessionIDFrom(input))
      await run("__PREFIX__-post-tool-use", toolPayload(input, output, "after"))
    },
    "permission.ask": async (input, output) => {
      const sessionID = await ensureSession(sessionIDFrom(input))
      const patterns = Array.isArray(input?.pattern) ? input.pattern : [input?.pattern].filter(Boolean)
      const event = "__PREFIX__-permission-request"
      const result = await run(event, {
        session_id: sessionID,
        reconc_runtime: "__PREFIX__",
        tool_name: normalizeTool(input?.type || input?.metadata?.tool),
        tool_input: { file_path: patterns[0] || "", command: input?.title || patterns[0] || "", pattern: patterns },
      })
      if (denied(event, result)) output.status = "deny"
    },
    "experimental.session.compacting": async (input, output) => {
      const sessionID = await ensureSession(sessionIDFrom(input))
      const result = await run("__PREFIX__-pre-compaction", { session_id: sessionID, reconc_runtime: "__PREFIX__" })
      const context = contextFrom(result)
      if (context && Array.isArray(output?.context)) output.context.push(context)
    },
    event: async ({ event }) => {
      const sessionID = sessionIDFrom(event) || fallbackSessionID
      if (event?.type === "message.part.updated") {
        const part = event?.properties?.part
        if (part?.type === "tool" && part?.state?.status === "error" && rememberTerminalFailure(part)) {
          await ensureSession(part?.sessionID || sessionID)
          await run("__PREFIX__-post-tool-use-failure", failurePayload(part))
        }
      } else if (event?.type === "session.created") {
        await ensureSession(sessionID)
      } else if (event?.type === "session.compacted") {
        await run("__PREFIX__-post-compaction", { session_id: sessionID, reconc_runtime: "__PREFIX__" })
      } else if (event?.type === "session.deleted") {
        await run("__PREFIX__-session-end", { session_id: sessionID, reconc_runtime: "__PREFIX__" })
        startedSessions.delete(sessionID)
        continuationStates.delete(sessionID)
        if (startedSessions.size === 0) await workerTransport.close()
      } else if (event?.type === "session.idle") {
        await handleStop(event)
      }
    },
  }
}__EXPORT_TAIL__
`
