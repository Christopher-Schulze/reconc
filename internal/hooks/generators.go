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

for dev_reconc in "$repo_root/.build/bin/reconc" "$repo_root/reconc"; do
    if [ -x "$dev_reconc" ]; then
        exec "$dev_reconc" ci "$repo_root" --staged
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

func generateClaudeCode() *Artifact {
	// Route Claude Code events to reconc-specific runtime sub-actions.
	command := func(event string, lifecycle Event) map[string]interface{} {
		return map[string]interface{}{
			"type":    "command",
			"command": "${CLAUDE_PROJECT_DIR}/tools/reconc/bin/hook",
			"args": []interface{}{
				event,
				"${CLAUDE_PROJECT_DIR}",
			},
			"timeout": mustTimeoutSeconds(KindClaudeCode, lifecycle),
		}
	}
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
						command("claude-post-compaction", EventPostCompaction),
					},
				},
			},
			"PreToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "Edit|Write|MultiEdit|Bash",
					"hooks": []interface{}{
						command("claude-pre-tool-use", EventPreToolUse),
					},
				},
			},
			"PermissionRequest": []interface{}{
				map[string]interface{}{
					"matcher": "Edit|Write|MultiEdit|Bash",
					"hooks": []interface{}{
						command("claude-permission-request", EventPermissionRequest),
					},
				},
			},
			"UserPromptSubmit": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						command("claude-user-prompt-submit", EventUserPromptSubmit),
					},
				},
			},
			"PostToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "Read|Edit|Write|MultiEdit|Bash",
					"hooks": []interface{}{
						command("claude-post-tool-use", EventPostToolUse),
					},
				},
			},
			"PostToolUseFailure": []interface{}{
				map[string]interface{}{
					"matcher": "Bash",
					"hooks": []interface{}{
						command("claude-post-tool-use-failure", EventPostToolUseFailure),
					},
				},
			},
			"Stop": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						command("claude-stop", EventStop),
					},
				},
			},
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
	}
}

func generateCodex() *Artifact {
	template := map[string]interface{}{
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				map[string]interface{}{
					"matcher": "startup|resume|clear",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":          "command",
							"command":       shellRuntimeCommand(".", "codex-session-start"),
							"statusMessage": "reconc: initializing policy session",
						},
					},
				},
			},
			"PreToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "Write|Edit|MultiEdit|Bash|apply_patch",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": shellRuntimeCommand(".", "codex-pre-tool-use"),
						},
					},
				},
			},
			"PermissionRequest": []interface{}{
				map[string]interface{}{
					"matcher": "Write|Edit|MultiEdit|Bash|apply_patch",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": shellRuntimeCommand(".", "codex-permission-request"),
						},
					},
				},
			},
			"UserPromptSubmit": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": shellRuntimeCommand(".", "codex-user-prompt-submit"),
						},
					},
				},
			},
			"PostToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "Read|Edit|Write|MultiEdit|Bash|apply_patch",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": shellRuntimeCommand(".", "codex-post-tool-use"),
						},
					},
				},
			},
			"PostToolUseFailure": []interface{}{
				map[string]interface{}{
					"matcher": "Bash",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": shellRuntimeCommand(".", "codex-post-tool-use-failure"),
						},
					},
				},
			},
			"Stop": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": shellRuntimeCommand(".", "codex-stop"),
						},
					},
				},
			},
			"SessionEnd": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": shellRuntimeCommand(".", "codex-session-end"),
						},
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
	}
}

func generateCursor() *Artifact {
	repoExpr := "."
	entry := func(event string, failClosed bool) map[string]interface{} {
		return map[string]interface{}{
			"command":    runtimeCommand(repoExpr, event),
			"failClosed": failClosed,
		}
	}
	postToolEntry := entry("cursor-post-tool-use", true)
	postToolEntry["matcher"] = "Read|Write|Edit|MultiEdit|StrReplace|Delete|FileEdit|TabRead|TabWrite"
	preToolEntry := entry("cursor-pre-tool-use", true)
	preToolEntry["matcher"] = "Write|Edit|MultiEdit|StrReplace|Delete|FileEdit|TabWrite"
	stopEntry := entry("cursor-stop", true)
	stopEntry["loop_limit"] = 10
	template := map[string]interface{}{
		"version": 1,
		"hooks": map[string]interface{}{
			"sessionStart": []interface{}{
				entry("cursor-session-start", true),
			},
			"beforeSubmitPrompt": []interface{}{
				entry("cursor-user-prompt-submit", true),
			},
			"preToolUse": []interface{}{
				preToolEntry,
			},
			"postToolUse": []interface{}{
				postToolEntry,
			},
			"beforeShellExecution": []interface{}{
				entry("cursor-before-shell-execution", true),
			},
			"afterShellExecution": []interface{}{
				entry("cursor-after-shell-execution", true),
			},
			"afterFileEdit": []interface{}{
				entry("cursor-after-file-edit", true),
			},
			"afterTabFileEdit": []interface{}{
				entry("cursor-after-tab-file-edit", true),
			},
			"stop": []interface{}{
				stopEntry,
			},
			"sessionEnd": []interface{}{
				entry("cursor-session-end", false),
			},
		},
	}
	data, _ := json.MarshalIndent(template, "", "  ")
	return &Artifact{
		Kind:       KindCursor,
		TargetPath: CursorHooksPath,
		Executable: false,
		Content:    string(data) + "\n",
	}
}

func generateAntigravity() *Artifact {
	repoExpr := "."
	command := func(event string, lifecycle Event) map[string]interface{} {
		return map[string]interface{}{
			"type":    "command",
			"command": runtimeCommand(repoExpr, event),
			"timeout": mustTimeoutSeconds(KindAntigravity, lifecycle),
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
	}
}

func runtimeCommand(repoExpr, event string) string {
	return fmt.Sprintf(`sh -lc '%s'`, shellRuntimeCommand(repoExpr, event))
}

func shellRuntimeCommand(repoExpr, event string) string {
	return fmt.Sprintf(`repo="%s"; hook="$repo/tools/reconc/bin/hook"; if [ -x "$hook" ]; then exec "$hook" %s "$repo"; fi; repo="$(git -C "$repo" rev-parse --show-toplevel 2>/dev/null || printf "%%s" "$repo")"; RECONC_HOOK_REPO_RESOLVED=1 exec "$repo/tools/reconc/bin/hook" %s "$repo"`, repoExpr, event, event)
}
