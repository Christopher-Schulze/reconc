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
	const devinToolMatcher = "^(exec|read|write|edit|apply_patch|notebook_edit|grep|glob|get_output|write_to_process|kill_shell|mcp_call_tool|mcp__.*)$"
	template := map[string]interface{}{
		"SessionStart":      []interface{}{entry("devin-session-start", EventSessionStart, "")},
		"UserPromptSubmit":  []interface{}{entry("devin-user-prompt-submit", EventUserPromptSubmit, "")},
		"PreToolUse":        []interface{}{entry("devin-pre-tool-use", EventPreToolUse, devinToolMatcher)},
		"PermissionRequest": []interface{}{entry("devin-permission-request", EventPermissionRequest, devinToolMatcher)},
		"PostToolUse":       []interface{}{entry("devin-post-tool-use", EventPostToolUse, devinToolMatcher)},
		"Stop":              []interface{}{entry("devin-stop", EventStop, "")},
		"SessionEnd":        []interface{}{entry("devin-session-end", EventSessionEnd, "")},
		"PostCompaction":    []interface{}{entry("devin-post-compaction", EventPostCompaction, "")},
	}
	return jsonHookArtifact(&Artifact{Kind: KindDevinCLI, TargetPath: DevinHooksPath}, template)
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
		// Copilot logs most hook failures and continues: a non-zero exit other
		// than 2 is fail-open for PermissionRequest, Stop, and SubagentStop.
		// PreToolUse denies on crashes and non-zero exits, and every event is
		// fail-open on timeout. Each decision route therefore turns a failure
		// into its exact deny or block envelope with exit 0.
		switch lifecycle {
		case EventStop, EventSubagentStop:
			reason := "Reconc could not evaluate this GitHub Copilot stop. Reinstall the GitHub Copilot hook and tools/reconc/bin/hook."
			fallback := fmt.Sprintf(`{"decision":"block","reason":%q}`, reason)
			bashCommand += fmt.Sprintf(` || printf '%%s\n' '%s'`, fallback)
			powershellCommand = fmt.Sprintf(`if (Get-Command sh -ErrorAction SilentlyContinue) { & sh "tools/reconc/bin/hook" "%s" "."; if ($LASTEXITCODE -ne 0) { Write-Output '%s'; exit 0 } } else { Write-Output '%s'; exit 0 }`, event, fallback, fallback)
		case EventPreToolUse:
			reason := "Reconc could not evaluate this GitHub Copilot tool call. Reinstall the GitHub Copilot hook and tools/reconc/bin/hook."
			fallback := fmt.Sprintf(`{"permissionDecision":"deny","permissionDecisionReason":%q}`, reason)
			bashCommand += fmt.Sprintf(` || printf '%%s\n' '%s'`, fallback)
			powershellCommand = fmt.Sprintf(`if (Get-Command sh -ErrorAction SilentlyContinue) { & sh "tools/reconc/bin/hook" "%s" "."; if ($LASTEXITCODE -ne 0) { Write-Output '%s'; exit 0 } } else { Write-Output '%s'; exit 0 }`, event, fallback, fallback)
		case EventPermissionRequest:
			reason := "Reconc could not evaluate this GitHub Copilot permission request. Reinstall the GitHub Copilot hook and tools/reconc/bin/hook."
			fallback := fmt.Sprintf(`{"behavior":"deny","message":%q}`, reason)
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
	// PascalCase PreToolUse and PermissionRequest match Claude tool names, but
	// PostToolUse anchors the regex against runtime tool names, so the evidence
	// matcher names both to fire under either semantics.
	const evidenceTools = "view|bash|powershell|create|edit|str_replace_editor|apply_patch|Read|Bash|Edit|Write"
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
	return jsonHookArtifact(&Artifact{Kind: KindGitHubCopilot, TargetPath: GitHubCopilotHooksPath}, template)
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
	return jsonHookArtifact(&Artifact{Kind: KindGrok, TargetPath: GrokHooksPath}, template)
}

func generateBunAgentPlugin(kind, targetPath, prefix string, descriptorExport bool) (*Artifact, error) {
	routeBudgets, err := bunRouteBudgets(kind)
	if err != nil {
		return nil, err
	}
	exportHead := "const ReconcOpenCodeServer ="
	exportTail := openCodeV2Entry
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

// failClosedEvents reports the platform events whose declared error policy
// blocks the action when the hook cannot produce a verdict.
func failClosedEvents(kind string) (map[Event]bool, error) {
	platform, ok := PlatformForKind(kind)
	if !ok {
		return nil, hookGeneratorError("missing hook platform: %s", kind)
	}
	blocking := make(map[Event]bool, len(platform.Capabilities))
	for _, capability := range platform.Capabilities {
		if capability.ErrorPolicy == FailureBlock {
			blocking[capability.Event] = true
		}
	}
	return blocking, nil
}

func hookGeneratorError(format string, args ...interface{}) error {
	return &rerrors.PolicySourceError{Message: fmt.Sprintf(format, args...)}
}

const bunAgentPluginTemplate = `// Managed by reconc. Project-local __PREFIX__ policy adapter.
// Policy, session state, and continuation decisions stay in the Go runtime.

const fallbackSessionID = globalThis.crypto?.randomUUID?.() ?? "__PREFIX__-" + Date.now()
const startedSessions = new Set()
const startingSessions = new Map()
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
    case "patch":
    case "apply_patch": return "apply_patch"
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
    if (result.invalidUTF8 || result.truncated) return budget.errorPolicy === "block"
    return result.code !== 0 && budget.errorPolicy === "block"
  }

  // Concurrent hooks and lifecycle events for one session share a single
  // in-flight start instead of each starting the session.
  const ensureSession = async (sessionID) => {
    const id = sessionID || fallbackSessionID
    if (startedSessions.has(id)) return id
    let start = startingSessions.get(id)
    if (!start) {
      start = (async () => {
        const event = "__PREFIX__-session-start"
        const result = await run(event, { session_id: id, reconc_runtime: "__PREFIX__" })
        if (shouldBlockFailure(event, result)) throw new Error(result.stderr || result.stdout || "reconc session start failed")
        if (result.code === 0) startedSessions.add(id)
      })()
      startingSessions.set(id, start)
      start.then(() => startingSessions.delete(id), () => startingSessions.delete(id))
    }
    await start
    return id
  }

  const toolPayload = (input, output, phase) => {
    const toolName = normalizeTool(input?.tool || output?.tool)
    const hostTool = typeof input?.tool === "string" ? input.tool : typeof output?.tool === "string" ? output.tool : ""
    let toolInput = output?.args || input?.args || {}
    // The patch tool carries every add/update/delete path inside patchText;
    // the runtime reads apply_patch paths from the command field.
    if (toolName === "apply_patch" && typeof toolInput?.patchText === "string" && toolInput.command === undefined) {
      toolInput = { ...toolInput, command: toolInput.patchText }
    }
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
    dispose: async () => {
      await workerTransport.close()
    },
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

// openCodeSessionTracker remembers the most recently active sessions; a
// session's own hooks refresh it, so eviction drops the least recently active.
const openCodeSessionTracker = `const trackedSessions = (limit) => {
  const sessions = new Set()
  return {
    track(sessionID) {
      if (typeof sessionID !== "string" || !sessionID) return
      sessions.delete(sessionID)
      if (sessions.size >= limit) sessions.delete(sessions.values().next().value)
      sessions.add(sessionID)
    },
    has: (sessionID) => sessions.has(sessionID),
    forget: (sessionID) => sessions.delete(sessionID),
  }
}
`

// openCodeV2Entry adapts OpenCode 2.x domain hooks to the 1.x hook object, so
// both versions share one policy adapter and one worker. OpenCode 2.x loads
// setup() from the default export and ignores server(); 1.x does the reverse.
const openCodeV2Entry = `

const textOf = (content) => {
  if (typeof content === "string") return content
  if (!Array.isArray(content)) return ""
  return content.map((part) => (typeof part?.text === "string" ? part.text : "")).filter(Boolean).join("\n")
}

// 2.x ends an agent run with a durable execution event; a run that ended
// because the user interrupted it never reaches the stop gate, so it cannot
// be continued against the user's choice.
const openCodeV1EventType = (event) => {
  switch (event?.type) {
    case "session.created":
    case "session.deleted":
      return event.type
    case "session.compaction.ended":
      return "session.compacted"
    case "session.execution.succeeded":
    case "session.execution.failed":
      return "session.idle"
    default:
      return ""
  }
}

// The 2.x event stream carries every session the server runs, including
// other projects'; lifecycle events reach the adapter only for sessions this
// plugin instance has seen through its own location-bound hooks.
const maxTrackedSessions = 4096

` + openCodeSessionTracker + `
const ReconcOpenCodeSetup = async (ctx) => {
  const location = ctx?.location || {}
  const sessions = trackedSessions(maxTrackedSessions)
  const track = sessions.track
  const ownsEvent = (event) => {
    if (!sessions.has(event?.data?.sessionID)) return false
    const directory = event?.location?.directory
    return !directory || !location.directory || directory === location.directory
  }
  const hooks = await ReconcOpenCodeServer({
    directory: location.directory,
    worktree: location.worktree,
    client: {
      session: {
        // 2.x admits a queued prompt durably and returns its record; the
        // shared adapter expects 1.x promptAsync acceptance.
        promptAsync: async ({ sessionID, messageID, parts }) => {
          await ctx.session.prompt({
            sessionID,
            text: textOf(parts),
            delivery: "queue",
            metadata: { reconc_message_id: messageID },
          })
          return { response: { ok: true, status: 204 } }
        },
      },
    },
  })
  await ctx.session.hook("prompt", async (event) => {
    track(event?.sessionID)
    const injected = event?.metadata?.reconc_message_id
    await hooks["chat.message"](
      { sessionID: event?.sessionID, messageID: typeof injected === "string" ? injected : event?.messageID },
      { parts: [{ type: "text", text: event?.prompt?.text || "" }] },
    )
  })
  await ctx.tool.hook("execute.before", async (event) => {
    track(event?.sessionID)
    await hooks["tool.execute.before"](
      { tool: event?.tool, sessionID: event?.sessionID, callID: event?.id },
      { args: event?.input },
    )
  })
  await ctx.tool.hook("execute.after", async (event) => {
    track(event?.sessionID)
    if (event?.status === "error") {
      await hooks.event({ event: { type: "message.part.updated", properties: { part: {
        type: "tool", tool: event.tool, sessionID: event.sessionID, messageID: event.messageID, callID: event.id,
        state: { status: "error", input: event.input, error: String(event.error?.message || event.error || "tool execution failed"), metadata: {} },
      } } } })
      return
    }
    const result = event?.result || {}
    await hooks["tool.execute.after"](
      { tool: event?.tool, sessionID: event?.sessionID, callID: event?.id },
      {
        args: event?.input,
        output: textOf(result.content),
        metadata: result.metadata && typeof result.metadata === "object" && !Array.isArray(result.metadata) ? result.metadata : {},
      },
    )
  })
  await ctx.permission.hook("evaluate", async (event) => {
    track(event?.sessionID)
    const resources = Array.isArray(event?.resources) ? event.resources : []
    const output = { status: "ask" }
    await hooks["permission.ask"](
      { sessionID: event?.sessionID, type: event?.action, pattern: resources, title: resources[0] || "", metadata: event?.metadata || {} },
      output,
    )
    if (output.status === "deny") {
      event.effect = "deny"
      event.message = "Reconc denied this permission request."
    }
  })
  await ctx.session.hook("compaction", async (event) => {
    track(event?.sessionID)
    const output = { context: [] }
    await hooks["experimental.session.compacting"]({ sessionID: event?.sessionID }, output)
    if (!Array.isArray(event?.system)) return
    for (const text of output.context) {
      if (typeof text === "string" && text) event.system.push({ type: "text", text })
    }
  })
  const controller = new AbortController()
  const events = (async () => {
    try {
      for await (const event of ctx.event.subscribe({ signal: controller.signal })) {
        // One failing event must not end lifecycle and continuation handling
        // for every later event.
        try {
          const type = openCodeV1EventType(event)
          if (!type || !ownsEvent(event)) continue
          await hooks.event({ event: { type, properties: event?.data || {} } })
          if (type === "session.deleted") sessions.forget(event.data.sessionID)
        } catch (error) {
          console.error("reconc OpenCode event handling failed: " + String(error))
        }
      }
    } catch (error) {
      if (!controller.signal.aborted) console.error("reconc OpenCode event stream ended: " + String(error))
    }
  })()
  return async () => {
    controller.abort()
    await events
    await hooks.dispose()
  }
}

export default { id: "reconc", server: ReconcOpenCodeServer, setup: ReconcOpenCodeSetup }`
