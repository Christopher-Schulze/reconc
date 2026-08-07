package hooks

import (
	"encoding/json"
	"fmt"
	"strings"
)

func generateGitPreCommit() *Artifact {
	content := `#!/bin/sh
# Managed by ` + "`" + `reconc hook install git-pre-commit` + "`" + `.
#
# Runs ` + "`" + `reconc ci --staged` + "`" + ` before every commit so that staged write paths
# are evaluated against the compiled policy lockfile. Exits non-zero on
# blocking violations, which aborts the commit.
#
# To bypass this hook for an individual commit, run ` + "`" + `git commit --no-verify` + "`" + `.
# To remove it, delete this file or run ` + "`" + `reconc hook install git-pre-commit
# --force` + "`" + ` to regenerate it.

set -eu
export RECONC_AUDIT=1

repo_root=$(git rev-parse --show-toplevel)

for dev_reconc in "$repo_root/tools/reconc/.build/bin/reconc" "$repo_root/.build/bin/reconc" "$repo_root/reconc"; do
    if [ -x "$dev_reconc" ]; then
        exec "$dev_reconc" ci "$repo_root" --staged
    fi
done

` + shellBinaryResolver() + `
for reconc_dir in "$repo_root/tools/reconc/dist" "$repo_root/dist"; do
    resolve_status=0
    resolve_reconc_dir "$reconc_dir" || resolve_status=$?
    if [ "$resolve_status" -eq 0 ]; then
        exec "$resolved_reconc" ci "$repo_root" --staged
    fi
    if [ "$resolve_status" -eq 2 ]; then
        exit 2
    fi
done

if command -v reconc >/dev/null 2>&1; then
    exec reconc ci "$repo_root" --staged
fi

echo "reconc pre-commit hook: no executable Reconc binary found" >&2
echo "expected one stable or unambiguous versioned repo-local binary, a dev binary, or reconc on PATH" >&2
exit 2
`
	return &Artifact{
		Kind:       KindGitPreCommit,
		TargetPath: GitPreCommitPath,
		Executable: true,
		Content:    content,
	}
}

func generateClaudeCode() (*Artifact, error) {
	timeouts, err := requiredTimeouts(KindClaudeCode,
		EventSessionStart, EventPostCompaction, EventUserPromptSubmit,
		EventPreToolUse, EventPermissionRequest, EventPermissionDenied,
		EventPostToolUse, EventPostToolUseFailure, EventSubagentStart,
		EventSubagentStop, EventPreCompaction, EventStop, EventStopFailure,
		EventSessionEnd, EventNotification, EventMCPBefore, EventMCPAfter,
	)
	if err != nil {
		return nil, err
	}
	// Route Claude Code events to reconc-specific runtime sub-actions.
	command := func(event string, lifecycle Event) map[string]interface{} {
		return map[string]interface{}{
			"type":    "command",
			"command": "${CLAUDE_PROJECT_DIR}/tools/reconc/bin/hook",
			"args": []interface{}{
				event,
				"${CLAUDE_PROJECT_DIR}",
			},
			"timeout": timeouts[lifecycle],
		}
	}
	const guardedTools = "Edit|Write|MultiEdit|NotebookEdit|TabWrite|StrReplace|Delete|Bash"
	const evidenceTools = "Read|Edit|Write|MultiEdit|NotebookEdit|TabWrite|StrReplace|Delete|Bash"
	// Claude names every MCP call `mcp__<server>__<tool>`. The namespace needs
	// its own matcher group: a matcher built only from exact-match characters is
	// compared literally, so the regular expression cannot join the exact tool
	// alternations above without changing how they match.
	const mcpTools = "mcp__.*"
	template := map[string]interface{}{
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				map[string]interface{}{
					"matcher": "startup|resume|clear",
					"hooks": []interface{}{
						command("claude-session-start", EventSessionStart),
					},
				},
				map[string]interface{}{
					"matcher": "compact",
					"hooks": []interface{}{
						command("claude-compaction-recovery", EventPostCompaction),
					},
				},
			},
			"UserPromptSubmit": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{command("claude-user-prompt-submit", EventUserPromptSubmit)},
				},
			},
			"PreToolUse": []interface{}{
				map[string]interface{}{
					"matcher": guardedTools,
					"hooks": []interface{}{
						command("claude-pre-tool-use", EventPreToolUse),
					},
				},
				map[string]interface{}{
					"matcher": mcpTools,
					"hooks": []interface{}{
						command("claude-mcp-before", EventMCPBefore),
					},
				},
			},
			"PermissionRequest": []interface{}{
				map[string]interface{}{
					"matcher": guardedTools,
					"hooks": []interface{}{
						command("claude-permission-request", EventPermissionRequest),
					},
				},
			},
			"PermissionDenied": []interface{}{
				map[string]interface{}{
					"matcher": guardedTools,
					"hooks":   []interface{}{command("claude-permission-denied", EventPermissionDenied)},
				},
			},
			"PostToolUse": []interface{}{
				map[string]interface{}{
					"matcher": evidenceTools,
					"hooks": []interface{}{
						command("claude-post-tool-use", EventPostToolUse),
					},
				},
				map[string]interface{}{
					"matcher": mcpTools,
					"hooks": []interface{}{
						command("claude-mcp-after", EventMCPAfter),
					},
				},
			},
			"PostToolUseFailure": []interface{}{
				map[string]interface{}{
					"matcher": evidenceTools,
					"hooks": []interface{}{
						command("claude-post-tool-use-failure", EventPostToolUseFailure),
					},
				},
				map[string]interface{}{
					"matcher": mcpTools,
					"hooks": []interface{}{
						command("claude-mcp-after", EventMCPAfter),
					},
				},
			},
			"Notification":  []interface{}{map[string]interface{}{"hooks": []interface{}{command("claude-notification", EventNotification)}}},
			"SubagentStart": []interface{}{map[string]interface{}{"hooks": []interface{}{command("claude-subagent-start", EventSubagentStart)}}},
			"SubagentStop":  []interface{}{map[string]interface{}{"hooks": []interface{}{command("claude-subagent-stop", EventSubagentStop)}}},
			"PreCompact":    []interface{}{map[string]interface{}{"hooks": []interface{}{command("claude-pre-compaction", EventPreCompaction)}}},
			"PostCompact":   []interface{}{map[string]interface{}{"hooks": []interface{}{command("claude-post-compaction", EventPostCompaction)}}},
			"Stop": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						command("claude-stop", EventStop),
					},
				},
			},
			"StopFailure": []interface{}{map[string]interface{}{"hooks": []interface{}{command("claude-stop-failure", EventStopFailure)}}},
			"SessionEnd": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						command("claude-session-end", EventSessionEnd),
					},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(template, "", "  ")
	return &Artifact{
		Kind:       KindClaudeCode,
		TargetPath: ClaudeCodeSettingsPath,
		Executable: false,
		Content:    string(data) + "\n",
	}, nil
}

func generateCodex() (*Artifact, error) {
	timeouts, err := requiredTimeouts(KindCodex,
		EventSessionStart, EventUserPromptSubmit, EventPreToolUse,
		EventPermissionRequest, EventPostToolUse, EventPreCompaction,
		EventPostCompaction, EventSubagentStart, EventSubagentStop, EventStop,
		EventSessionEnd, EventMCPBefore, EventMCPAfter,
	)
	if err != nil {
		return nil, err
	}
	command := func(event string, lifecycle Event, statusMessage string) map[string]interface{} {
		entry := map[string]interface{}{
			"type":    "command",
			"command": shellRuntimeCommand(".", event),
			"timeout": timeouts[lifecycle],
		}
		if statusMessage != "" {
			entry["statusMessage"] = statusMessage
		}
		return entry
	}
	template := map[string]interface{}{
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				map[string]interface{}{
					"matcher": "startup|resume|clear|compact",
					"hooks": []interface{}{
						command("codex-session-start", EventSessionStart, "reconc: initializing policy session"),
					},
				},
			},
			"UserPromptSubmit": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{command("codex-user-prompt-submit", EventUserPromptSubmit, "")},
				},
			},
			"PreToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "Write|Edit|MultiEdit|Bash|apply_patch",
					"hooks": []interface{}{
						command("codex-pre-tool-use", EventPreToolUse, ""),
					},
				},
				// Codex compares a matcher without regular-expression characters
				// literally, so the MCP namespace needs its own group.
				map[string]interface{}{
					"matcher": "mcp__.*",
					"hooks": []interface{}{
						command("codex-mcp-before", EventMCPBefore, ""),
					},
				},
			},
			"PermissionRequest": []interface{}{
				map[string]interface{}{
					"matcher": "Write|Edit|MultiEdit|Bash|apply_patch",
					"hooks": []interface{}{
						command("codex-permission-request", EventPermissionRequest, ""),
					},
				},
			},
			"PostToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "Read|Edit|Write|MultiEdit|Bash|apply_patch",
					"hooks": []interface{}{
						command("codex-post-tool-use", EventPostToolUse, ""),
					},
				},
				map[string]interface{}{
					"matcher": "mcp__.*",
					"hooks": []interface{}{
						command("codex-mcp-after", EventMCPAfter, ""),
					},
				},
			},
			"PreCompact":    []interface{}{map[string]interface{}{"hooks": []interface{}{command("codex-pre-compaction", EventPreCompaction, "")}}},
			"PostCompact":   []interface{}{map[string]interface{}{"hooks": []interface{}{command("codex-post-compaction", EventPostCompaction, "")}}},
			"SubagentStart": []interface{}{map[string]interface{}{"hooks": []interface{}{command("codex-subagent-start", EventSubagentStart, "")}}},
			"SubagentStop":  []interface{}{map[string]interface{}{"hooks": []interface{}{command("codex-subagent-stop", EventSubagentStop, "")}}},
			"Stop": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						command("codex-stop", EventStop, ""),
					},
				},
			},
			"SessionEnd": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						command("codex-session-end", EventSessionEnd, ""),
					},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(template, "", "  ")
	return &Artifact{
		Kind:       KindCodex,
		TargetPath: CodexHooksPath,
		Executable: false,
		Content:    string(data) + "\n",
	}, nil
}

func generateCursor() (*Artifact, error) {
	platform, ok := PlatformForKind(KindCursor)
	if !ok {
		return nil, hookGeneratorError("missing hook platform: %s", KindCursor)
	}
	if err := validatePlatform(platform); err != nil {
		return nil, err
	}
	hookEntries := map[string]interface{}{}
	for _, capability := range platform.Capabilities {
		if capability.Support == SupportUnsupported {
			continue
		}
		for _, binding := range capability.Bindings {
			if binding.Compatibility || binding.RuntimeEvent == "" {
				continue
			}
			entry := map[string]interface{}{
				"command": runtimeCommand(".", binding.RuntimeEvent),
				"timeout": capability.TimeoutSeconds,
			}
			if binding.ResponseMode == CursorResponseDecision || binding.ResponseMode == CursorResponseStopFollowup {
				entry["failClosed"] = capability.ErrorPolicy == FailureBlock && capability.TimeoutPolicy == FailureBlock
			} else {
				entry["failClosed"] = false
			}
			if binding.Matcher != "" {
				entry["matcher"] = binding.Matcher
			}
			if binding.LoopLimit > 0 {
				entry["loop_limit"] = binding.LoopLimit
			}
			entries, _ := hookEntries[binding.NativeEvent].([]interface{})
			hookEntries[binding.NativeEvent] = append(entries, entry)
		}
	}
	template := map[string]interface{}{
		"version": 1,
		"hooks":   hookEntries,
	}
	data, _ := json.MarshalIndent(template, "", "  ")
	return &Artifact{
		Kind:       KindCursor,
		TargetPath: CursorHooksPath,
		Executable: false,
		Content:    string(data) + "\n",
	}, nil
}

func generateAntigravity() (*Artifact, error) {
	timeouts, err := requiredTimeouts(KindAntigravity,
		EventSessionStart, EventPreToolUse, EventPostToolUse, EventSessionEnd, EventStop,
	)
	if err != nil {
		return nil, err
	}
	repoExpr := "."
	command := func(event string, lifecycle Event) map[string]interface{} {
		return map[string]interface{}{
			"type":    "command",
			"command": runtimeCommand(repoExpr, event),
			"timeout": timeouts[lifecycle],
		}
	}
	preToolMatcher := strings.Join([]string{
		"write_to_file",
		"replace_file_content",
		"multi_replace_file_content",
		"run_command",
	}, "|")
	postToolMatcher := strings.Join([]string{
		"view_file",
		"write_to_file",
		"replace_file_content",
		"multi_replace_file_content",
		"list_dir",
		"find_by_name",
		"grep_search",
		"run_command",
	}, "|")
	toolEntry := func(event string, lifecycle Event, matcher string) map[string]interface{} {
		return map[string]interface{}{
			"matcher": matcher,
			"hooks": []interface{}{
				command(event, lifecycle),
			},
		}
	}
	template := map[string]interface{}{
		"reconc": map[string]interface{}{
			"PreInvocation": []interface{}{
				command("antigravity-pre-invocation", EventSessionStart),
			},
			"PreToolUse": []interface{}{
				toolEntry("antigravity-pre-tool-use", EventPreToolUse, preToolMatcher),
			},
			"PostToolUse": []interface{}{
				toolEntry("antigravity-post-tool-use", EventPostToolUse, postToolMatcher),
			},
			"PostInvocation": []interface{}{
				command("antigravity-post-invocation", EventSessionEnd),
			},
			"Stop": []interface{}{
				command("antigravity-stop", EventStop),
			},
		},
	}
	data, _ := json.MarshalIndent(template, "", "  ")
	return &Artifact{
		Kind:       KindAntigravity,
		TargetPath: AntigravityHooksPath,
		Executable: false,
		Content:    string(data) + "\n",
	}, nil
}

func generateKimiCode() (*Artifact, error) {
	platform, ok := PlatformForKind(KindKimiCode)
	if !ok {
		return nil, hookGeneratorError("missing hook platform: %s", KindKimiCode)
	}
	if err := validatePlatform(platform); err != nil {
		return nil, err
	}
	var body strings.Builder
	// The leading newline belongs to the managed block. Appending and later
	// removing the block therefore preserves the user's original EOF newline
	// byte-for-byte, whether it was present or absent.
	body.WriteByte('\n')
	body.WriteString(KimiCodeManagedBlockStart)
	body.WriteByte('\n')
	for _, capability := range platform.Capabilities {
		if capability.Support == SupportUnsupported {
			continue
		}
		for _, binding := range capability.Bindings {
			if binding.Compatibility || binding.RuntimeEvent == "" {
				continue
			}
			body.WriteString("[[hooks]]\n")
			fmt.Fprintf(&body, "event = %q\n", binding.NativeEvent)
			fmt.Fprintf(&body, "command = %q\n", "reconc hook kimi-runtime "+binding.RuntimeEvent)
			fmt.Fprintf(&body, "timeout = %d\n\n", capability.TimeoutSeconds)
		}
	}
	body.WriteString(KimiCodeManagedBlockEnd)
	body.WriteByte('\n')
	return &Artifact{
		Kind:       KindKimiCode,
		TargetPath: KimiCodeConfigDisplayPath,
		Content:    body.String(),
	}, nil
}

func runtimeCommand(repoExpr, event string) string {
	return fmt.Sprintf(`sh -lc '%s'`, shellRuntimeCommand(repoExpr, event))
}

func shellRuntimeCommand(repoExpr, event string) string {
	return fmt.Sprintf(`repo="%s"; hook="$repo/tools/reconc/bin/hook"; if [ -x "$hook" ]; then exec "$hook" %s "$repo"; fi; repo="$(git -C "$repo" rev-parse --show-toplevel 2>/dev/null || printf "%%s" "$repo")"; RECONC_HOOK_REPO_RESOLVED=1 exec "$repo/tools/reconc/bin/hook" %s "$repo"`, repoExpr, event, event)
}
