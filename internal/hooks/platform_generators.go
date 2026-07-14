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

func generateCopilot() *Artifact {
	entry := func(event string, lifecycle Event, matcher string) map[string]interface{} {
		out := map[string]interface{}{
			"type":       "command",
			"bash":       shellRuntimeCommand(".", event),
			"powershell": fmt.Sprintf("reconc hook runtime %s .", event),
			"command":    fmt.Sprintf("reconc hook runtime %s .", event),
			"cwd":        ".",
			"timeoutSec": mustTimeoutSeconds(KindCopilot, lifecycle),
		}
		if matcher != "" {
			out["matcher"] = matcher
		}
		return out
	}
	template := map[string]interface{}{
		"version": 1,
		"hooks": map[string]interface{}{
			"SessionStart":       []interface{}{entry("copilot-session-start", EventSessionStart, "")},
			"UserPromptSubmit":   []interface{}{entry("copilot-user-prompt-submit", EventUserPromptSubmit, "")},
			"PreToolUse":         []interface{}{entry("copilot-pre-tool-use", EventPreToolUse, "Bash|Edit|Write")},
			"PermissionRequest":  []interface{}{entry("copilot-permission-request", EventPermissionRequest, "Bash|Edit|Write")},
			"PostToolUse":        []interface{}{entry("copilot-post-tool-use", EventPostToolUse, "Read|Bash|Edit|Write|Grep|Glob")},
			"PostToolUseFailure": []interface{}{entry("copilot-post-tool-use-failure", EventPostToolUseFailure, "Bash")},
			"Stop":               []interface{}{entry("copilot-stop", EventStop, "")},
			"SessionEnd":         []interface{}{entry("copilot-session-end", EventSessionEnd, "")},
		},
	}
	data, _ := json.MarshalIndent(template, "", "  ")
	return &Artifact{Kind: KindCopilot, TargetPath: CopilotHooksPath, Content: string(data) + "\n"}
}

func generateOpenCodeThin() *Artifact {
	return generateBunAgentPlugin(KindOpenCode, OpenCodePluginPath, "opencode", false)
}

func generateKilo() *Artifact {
	return generateBunAgentPlugin(KindKilo, KiloPluginPath, "kilo", true)
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

const textFromParts = (parts) => (Array.isArray(parts) ? parts : [])
  .map((part) => typeof part === "string" ? part : part?.text || part?.content || "")
  .filter(Boolean)
  .join("\n")

__EXPORT_HEAD__ async ({ directory, worktree, client }) => {
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
    const stopEvent = "__PREFIX__-stop"
    const result = await run(stopEvent, { session_id: sessionID, reconc_runtime: "__PREFIX__" })
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
    "chat.message": async (input, output) => {
      const sessionID = await ensureSession(sessionIDFrom(input))
      await run("__PREFIX__-user-prompt-submit", { session_id: sessionID, reconc_runtime: "__PREFIX__", prompt: textFromParts(output?.parts) })
    },
    "tool.execute.before": async (input, output) => {
      await ensureSession(sessionIDFrom(input))
      const event = "__PREFIX__-pre-tool-use"
      const result = await run(event, toolPayload(input, output))
      if (shouldBlockFailure(event, result)) throw new Error(result.stderr || result.stdout || "reconc blocked tool execution")
    },
    "tool.execute.after": async (input, output) => {
      await ensureSession(sessionIDFrom(input))
      const payload = toolPayload(input, output)
      const event = payload.error ? "__PREFIX__-post-tool-use-failure" : "__PREFIX__-post-tool-use"
      await run(event, payload)
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
      const result = await run("__PREFIX__-post-compaction", { session_id: sessionID, reconc_runtime: "__PREFIX__", summary: input?.message?.summary || "" })
      const context = contextFrom(result)
      if (context && Array.isArray(output?.context)) output.context.push(context)
    },
    event: async ({ event }) => {
      const sessionID = sessionIDFrom(event) || fallbackSessionID
      if (event?.type === "session.created") {
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
