package hooks

import (
	"encoding/json"
	"fmt"
	"strings"
)

func generateDevinCLI() *Artifact {
	command := func(event string, lifecycle Event) map[string]interface{} {
		return map[string]interface{}{
			"type":    "command",
			"command": fmt.Sprintf(`"$DEVIN_PROJECT_DIR/tools/reconc/bin/hook" %s "$DEVIN_PROJECT_DIR"`, event),
			"timeout": mustTimeoutSeconds(KindDevinCLI, lifecycle),
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
	return &Artifact{Kind: KindDevinCLI, TargetPath: DevinHooksPath, Content: string(data) + "\n"}
}

func generateOpenCodeThin() *Artifact {
	return generateBunAgentPlugin(KindOpenCode, OpenCodePluginPath, "opencode", false)
}

func generateGitHubCopilot() *Artifact {
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
			"timeoutSec": mustTimeoutSeconds(KindGitHubCopilot, lifecycle),
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
	return &Artifact{Kind: KindGitHubCopilot, TargetPath: GitHubCopilotHooksPath, Content: string(data) + "\n"}
}

func generateKilo() *Artifact {
	return generateBunAgentPlugin(KindKilo, KiloPluginPath, "kilo", true)
}

func generateGrok() *Artifact {
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
			"timeout": mustTimeoutSeconds(KindGrok, lifecycle),
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
	return &Artifact{Kind: KindGrok, TargetPath: GrokHooksPath, Content: string(data) + "\n"}
}

func generateBunAgentPlugin(kind, targetPath, prefix string, descriptorExport bool) *Artifact {
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
		"__ROUTE_BUDGETS__", bunRouteBudgets(kind),
	).Replace(bunAgentPluginTemplate)
	return &Artifact{Kind: kind, TargetPath: targetPath, Content: content}
}

type bunRouteBudget struct {
	TimeoutMilliseconds int           `json:"timeoutMilliseconds"`
	MaxOutputBytes      int           `json:"maxOutputBytes"`
	ErrorPolicy         FailurePolicy `json:"errorPolicy"`
	TimeoutPolicy       FailurePolicy `json:"timeoutPolicy"`
}

func bunRouteBudgets(kind string) string {
	platform, ok := PlatformForKind(kind)
	if !ok {
		panic("missing hook platform: " + kind)
	}
	budgets := map[string]bunRouteBudget{}
	for _, capability := range platform.Capabilities {
		for _, event := range capability.RuntimeEvents {
			budgets[event] = bunRouteBudget{
				TimeoutMilliseconds: capability.TimeoutSeconds * 1000,
				MaxOutputBytes:      capability.MaxOutputBytes,
				ErrorPolicy:         capability.ErrorPolicy,
				TimeoutPolicy:       capability.TimeoutPolicy,
			}
		}
	}
	data, err := json.Marshal(budgets)
	if err != nil {
		panic("marshal hook route budgets: " + err.Error())
	}
	return string(data)
}

func mustTimeoutSeconds(kind string, event Event) int {
	platform, ok := PlatformForKind(kind)
	if !ok {
		panic("missing hook platform: " + kind)
	}
	for _, capability := range platform.Capabilities {
		if capability.Event == event && capability.TimeoutSeconds > 0 {
			return capability.TimeoutSeconds
		}
	}
	panic(fmt.Sprintf("missing timeout for %s %s", kind, event))
}

const bunAgentPluginTemplate = `// Managed by reconc. Project-local __PREFIX__ policy adapter.
// Policy, session state, and continuation decisions stay in the Go runtime.

const fallbackSessionID = globalThis.crypto?.randomUUID?.() ?? "__PREFIX__-" + Date.now()
const startedSessions = new Set()
const terminalToolFailures = new Set()
const maxRememberedToolFailures = 1024
const routeBudgets = __ROUTE_BUDGETS__

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
      if (await Bun.file(binary).exists()) return [binary, "hook", "runtime", event, repo]
    }
    return ["reconc", "hook", "runtime", event, repo]
  }

  const run = async (event, payload) => {
    const budget = routeBudgets[event] || { timeoutMilliseconds: 5000, maxOutputBytes: 8192, errorPolicy: "block", timeoutPolicy: "block" }
    // Bun.spawn has no output-size option; cap after reading. The Go
    // runtime already bounds its own hook output per route.
    const cap = (text) => budget.maxOutputBytes > 0 && text.length > budget.maxOutputBytes ? text.slice(0, budget.maxOutputBytes) : text
    let proc
    let timeoutID
    let timedOut = false
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
        proc.kill("SIGKILL")
      }, budget.timeoutMilliseconds)
      const [code, stdout, stderr] = await Promise.all([proc.exited, new Response(proc.stdout).text(), new Response(proc.stderr).text()])
      return { code, stdout: cap(stdout.trim()), stderr: cap(stderr.trim()), timedOut }
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
    const event = "__PREFIX__-session-start"
    const result = await run(event, { session_id: id, reconc_runtime: "__PREFIX__" })
    if (shouldBlockFailure(event, result)) throw new Error(result.stderr || result.stdout || "reconc session start failed")
    if (result.code === 0) startedSessions.add(id)
    return id
  }

  const toolPayload = (input, output) => {
    const payload = {
      session_id: sessionIDFrom(input) || fallbackSessionID,
      reconc_runtime: "__PREFIX__",
      tool_name: normalizeTool(input?.tool || output?.tool),
      tool_input: output?.args || input?.args || {},
      tool_response: {
        title: output?.title || "",
        output: output?.output ?? output?.result ?? output?.response ?? "",
        metadata: output?.metadata || {},
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

  const handleStop = async (event) => {
    const sessionID = await ensureSession(sessionIDFrom(event))
    const stopEvent = "__PREFIX__-stop"
    const result = await run(stopEvent, { session_id: sessionID, reconc_runtime: "__PREFIX__" })
    if (result.code !== 0) {
      if (shouldBlockFailure(stopEvent, result)) throw new Error(result.stderr || result.stdout || "reconc blocked session stop")
      return
    }
    const reason = contextFrom(result)
    if (!reason) return
    if (client?.session?.prompt) {
      await client.session.prompt({ path: { id: sessionID }, body: { parts: [{ type: "text", text: reason }] } })
      return
    }
    // No prompt API on this host: session.idle is a best-effort
    // continuation surface, so log the nudge instead of throwing.
    console.error("reconc continuation (host has no session.prompt API): " + reason)
  }

  return {
    "chat.message": async (input, output) => {
      const sessionID = await ensureSession(input?.sessionID)
      await run("__PREFIX__-user-prompt-submit", {
        session_id: sessionID,
        reconc_runtime: "__PREFIX__",
        prompt: collectText(output?.parts || []).join("\n"),
      })
    },
    "tool.execute.before": async (input, output) => {
      await ensureSession(sessionIDFrom(input))
      const event = "__PREFIX__-pre-tool-use"
      const result = await run(event, toolPayload(input, output))
      if (shouldBlockFailure(event, result)) throw new Error(result.stderr || result.stdout || "reconc blocked tool execution")
    },
    "tool.execute.after": async (input, output) => {
      await ensureSession(sessionIDFrom(input))
      await run("__PREFIX__-post-tool-use", toolPayload(input, output))
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
      } else if (event?.type === "session.idle") {
        await handleStop(event)
      }
    },
  }
}__EXPORT_TAIL__
`
