// Package hooks generates and installs platform-specific hook
// artifacts that wire reconc into git, Claude Code, Codex, Cursor,
// OpenCode, and Antigravity.
//
// Generators are pure functions (return a string + metadata).
// Installers are the only entry points that touch the filesystem and
// they refuse to clobber existing hooks unless Force=true.
//
// Generated commands use reconc-specific paths and command names.
package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
)

// Hook artifact paths.
const (
	GitPreCommitPath         = ".git/hooks/pre-commit"
	GitPreCommitScaffoldPath = ".githooks/pre-commit"
	ClaudeCodeSettingsPath   = ".claude/settings.json"
	CodexHooksPath           = ".codex/hooks.json"
	CursorHooksPath          = ".cursor/hooks.json"
	OpenCodePluginPath       = ".opencode/plugins/reconc.js"
	AntigravityHooksPath     = ".agents/hooks.json"
)

// Supported hook kinds.
const (
	KindGitPreCommit = "git-pre-commit"
	KindClaudeCode   = "claude-code"
	KindCodex        = "codex"
	KindCursor       = "cursor"
	KindOpenCode     = "opencode"
	KindAntigravity  = "antigravity"
)

// SupportedKinds returns every kind reconc hook generate can produce.
func SupportedKinds() []string {
	return []string{KindGitPreCommit, KindClaudeCode, KindCodex, KindCursor, KindOpenCode, KindAntigravity}
}

// InstallableKinds returns the kinds that reconc hook install can
// write directly. JSON configs are merged non-destructively; git
// pre-commit is a fresh file write.
func InstallableKinds() []string {
	return []string{KindGitPreCommit, KindClaudeCode, KindCodex, KindCursor, KindOpenCode, KindAntigravity}
}

// Artifact is one generated hook script + enough context to render it.
type Artifact struct {
	Kind       string `json:"kind"`
	TargetPath string `json:"target_path"`
	Executable bool   `json:"executable"`
	Content    string `json:"content"`
}

// InstallReport is the deterministic outcome of an install call.
type InstallReport struct {
	Kind       string `json:"kind"`
	RepoRoot   string `json:"repo_root"`
	TargetPath string `json:"target_path"`
	Action     string `json:"action"` // "created" | "updated"
	Executable bool   `json:"executable"`
	NextAction string `json:"next_action"`
	// DroppedUserEdits lists any hooks-entry strings classified as
	// user-modified reconc entries that were replaced during a JSON
	// merge install. Callers typically surface these as stderr
	// warnings so users know their edits were overwritten.
	DroppedUserEdits []string `json:"dropped_user_edits,omitempty"`
}

// ScaffoldSyncReport is the deterministic result of syncing generated
// hook artifacts into a repo-root scaffold folder. Unlike Install,
// this writes only source-controlled scaffold artifacts and never
// touches a live .git/hooks directory.
type ScaffoldSyncReport struct {
	ScaffoldRoot string                   `json:"scaffold_root"`
	Artifacts    []ScaffoldArtifactReport `json:"artifacts"`
}

// ScaffoldArtifactReport describes one generated scaffold artifact.
type ScaffoldArtifactReport struct {
	Kind       string `json:"kind"`
	TargetPath string `json:"target_path"`
	Action     string `json:"action"` // "created" | "updated" | "unchanged"
	Executable bool   `json:"executable"`
}

// Generate dispatches to the per-kind generator.
func Generate(kind string) (*Artifact, error) {
	switch kind {
	case KindGitPreCommit:
		return generateGitPreCommit(), nil
	case KindClaudeCode:
		return generateClaudeCode(), nil
	case KindCodex:
		return generateCodex(), nil
	case KindCursor:
		return generateCursor(), nil
	case KindOpenCode:
		return generateOpenCode(), nil
	case KindAntigravity:
		return generateAntigravity(), nil
	}
	return nil, &rerrors.PolicySourceError{
		Message: fmt.Sprintf("unknown hook kind: %q (supported: %v)", kind, SupportedKinds()),
	}
}

// Install writes an installable hook into the repo. Refuses to
// overwrite an existing hook unless force is true.
//
// Supported kinds are installable:
//   - git-pre-commit: creates .git/hooks/pre-commit (fresh file write,
//     refuses to clobber an existing hook unless --force is set)
//   - claude-code: merges reconc hook entries into .claude/settings.json
//     non-destructively. Idempotent: reconc-owned hook entries are
//     identified by their repo-local wrapper/runtime signature and replaced
//     wholesale on each install; non-reconc keys are preserved.
//   - codex: same merge strategy for .codex/hooks.json.
//   - cursor: same merge strategy for .cursor/hooks.json.
//   - opencode: writes .opencode/plugins/reconc.js as a project-local
//     plugin, refusing to clobber non-reconc plugin content unless
//     --force is set.
//   - antigravity: merges a top-level "reconc" JSON hook definition into
//     .agents/hooks.json, preserving other Antigravity customizations.
func Install(kind, repoRoot string, force bool) (*InstallReport, error) {
	switch kind {
	case KindGitPreCommit:
		return installGitPreCommit(repoRoot, force)
	case KindClaudeCode:
		return installJSONHooks(KindClaudeCode, ClaudeCodeSettingsPath, repoRoot, force)
	case KindCodex:
		return installJSONHooks(KindCodex, CodexHooksPath, repoRoot, force)
	case KindCursor:
		return installJSONHooks(KindCursor, CursorHooksPath, repoRoot, force)
	case KindOpenCode:
		return installOpenCode(repoRoot, force)
	case KindAntigravity:
		return installAntigravity(repoRoot, force)
	}
	return nil, &rerrors.PolicySourceError{
		Message: fmt.Sprintf("unknown installable hook kind: %q (installable: %v)", kind, InstallableKinds()),
	}
}

// ScaffoldKinds returns every generated hook artifact that belongs in
// repo-root-scaffold. Git pre-commit is mapped to .githooks/pre-commit
// because .git/hooks is clone-local and cannot be source-controlled.
func ScaffoldKinds() []string {
	return []string{KindGitPreCommit, KindClaudeCode, KindCodex, KindCursor, KindOpenCode, KindAntigravity}
}

// GenerateScaffoldArtifact returns the generated artifact at its
// source-controlled scaffold path.
func GenerateScaffoldArtifact(kind string) (*Artifact, error) {
	artifact, err := Generate(kind)
	if err != nil {
		return nil, err
	}
	if kind == KindGitPreCommit {
		copy := *artifact
		copy.TargetPath = GitPreCommitScaffoldPath
		return &copy, nil
	}
	return artifact, nil
}

// SyncRepoRootScaffold writes all generated hook artifacts into a
// repo-root-scaffold directory. It is intentionally generator-driven:
// no root repo or source-specific harness is used as a source of truth.
func SyncRepoRootScaffold(scaffoldRoot string) (*ScaffoldSyncReport, error) {
	root, err := filepath.Abs(scaffoldRoot)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "resolve scaffold path", Cause: err}
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "scaffold path does not exist: " + root, Cause: err}
	}
	if !info.IsDir() {
		return nil, &rerrors.PolicySourceError{Message: "scaffold path is not a directory: " + root}
	}

	report := &ScaffoldSyncReport{
		ScaffoldRoot: root,
		Artifacts:    []ScaffoldArtifactReport{},
	}
	for _, kind := range ScaffoldKinds() {
		artifact, err := GenerateScaffoldArtifact(kind)
		if err != nil {
			return nil, err
		}
		target := filepath.Join(root, filepath.FromSlash(artifact.TargetPath))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, &rerrors.PolicySourceError{Message: "create parent dir of " + target, Cause: err}
		}
		action, err := writeGeneratedArtifact(target, artifact.Content, artifact.Executable)
		if err != nil {
			return nil, err
		}
		report.Artifacts = append(report.Artifacts, ScaffoldArtifactReport{
			Kind:       kind,
			TargetPath: artifact.TargetPath,
			Action:     action,
			Executable: artifact.Executable,
		})
	}
	return report, nil
}

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

case "$(uname -s)" in
    Darwin) reconc_os="darwin" ;;
    Linux) reconc_os="linux" ;;
    CYGWIN*|MINGW*|MSYS*) reconc_os="windows" ;;
    *) reconc_os="" ;;
esac

case "$(uname -m)" in
    arm64|aarch64) reconc_arch="arm64" ;;
    x86_64|amd64) reconc_arch="amd64" ;;
    *) reconc_arch="" ;;
esac

if [ -n "$reconc_os" ] && [ -n "$reconc_arch" ]; then
    reconc_ext=""
    if [ "$reconc_os" = "windows" ]; then
        reconc_ext=".exe"
    fi
    release_reconc="reconc-0.5.0-$reconc_os-$reconc_arch$reconc_ext"
    for local_reconc in \
        "$repo_root/tools/reconc/dist/$release_reconc" \
        "$repo_root/dist/$release_reconc"
    do
        if [ -x "$local_reconc" ]; then
            exec "$local_reconc" ci "$repo_root" --staged
        fi
    done
fi

for dev_reconc in "$repo_root/.build/bin/reconc" "$repo_root/reconc"; do
    if [ -x "$dev_reconc" ]; then
        exec "$dev_reconc" ci "$repo_root" --staged
    fi
done

if command -v reconc >/dev/null 2>&1; then
    exec reconc ci "$repo_root" --staged
fi

echo "reconc pre-commit hook: no executable Reconc binary found" >&2
echo "expected repo-local release binary, dev binary, or reconc on PATH" >&2
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
	command := func(event string) map[string]interface{} {
		return map[string]interface{}{
			"type":    "command",
			"command": "${CLAUDE_PROJECT_DIR}/tools/reconc/bin/hook",
			"args": []interface{}{
				event,
				"${CLAUDE_PROJECT_DIR}",
			},
		}
	}
	template := map[string]interface{}{
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						command("claude-session-start"),
					},
				},
			},
			"PreToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "Edit|Write|MultiEdit|Bash",
					"hooks": []interface{}{
						command("claude-pre-tool-use"),
					},
				},
			},
			"PermissionRequest": []interface{}{
				map[string]interface{}{
					"matcher": "Edit|Write|MultiEdit|Bash",
					"hooks": []interface{}{
						command("claude-permission-request"),
					},
				},
			},
			"UserPromptSubmit": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						command("claude-user-prompt-submit"),
					},
				},
			},
			"PostToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "Read|Edit|Write|MultiEdit|Bash",
					"hooks": []interface{}{
						command("claude-post-tool-use"),
					},
				},
			},
			"PostToolUseFailure": []interface{}{
				map[string]interface{}{
					"matcher": "Bash",
					"hooks": []interface{}{
						command("claude-post-tool-use-failure"),
					},
				},
			},
			"Stop": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						command("claude-stop"),
					},
				},
			},
			"SessionEnd": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						command("claude-session-end"),
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
	command := func(event string) map[string]interface{} {
		return map[string]interface{}{
			"type":    "command",
			"command": runtimeCommand(repoExpr, event),
			"timeout": 120,
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
	toolEntry := func(event string, matcher string) map[string]interface{} {
		return map[string]interface{}{
			"matcher": matcher,
			"hooks": []interface{}{
				command(event),
			},
		}
	}
	template := map[string]interface{}{
		"reconc": map[string]interface{}{
			"PreInvocation": []interface{}{
				command("antigravity-pre-invocation"),
			},
			"PreToolUse": []interface{}{
				toolEntry("antigravity-pre-tool-use", preToolMatcher),
			},
			"PostToolUse": []interface{}{
				toolEntry("antigravity-post-tool-use", postToolMatcher),
			},
			"PostInvocation": []interface{}{
				command("antigravity-post-invocation"),
			},
			"Stop": []interface{}{
				command("antigravity-stop"),
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

func generateOpenCode() *Artifact {
	content := `// Managed by reconc. Project-local OpenCode plugin.
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
  const stateDir = repo + "/.reconc/degenmode"
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

  const disableDegenmode = async (state, reason, context = {}) => {
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

  const enableDegenmode = async (state, openCodeSessionID) => {
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
    await disableDegenmode({
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
      await enableDegenmode({ ...state, stop_anchor_message_id: "" }, targetSessionID)
      return
    }
    if (state.enabled && !sameActiveRun(state, targetSessionID)) return
    await disableDegenmode({
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

const degenPrompt = (task, head) => "degenmode autocontinue. Continue the repository task lifecycle without asking for routine permission. No ceremony, no confirmation questions - just work.\n\n" +
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

  const hasExplicitRunText = (text) => String(text || "").split(/\s+/).some((field) => {
    const token = field.replace(/^[\s.,;:!?()[\]{}<>]+|[\s.,;:!?()[\]{}<>]+$/g, "")
    return token === "/degenmode"
  })

  const maybeAutocontinue = async (event, stopResult) => {
    if (stopResult?.stdout && stopResult.stdout.includes('\"decision\":\"block\"')) return
    if (nudgeInFlight) return

    const openCodeSessionID = findSessionID(event)
    const state = await readState()

    const stopMarker = await readStopMarker()
    if (stopMarkerMatchesState(stopMarker, state)) {
      await disableDegenmode(state, "stop_file")
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
      await disableDegenmode({ ...state, session_id: targetSessionID, last_head: head, last_current: progress, no_progress_nudges: noProgressNudges }, "no_progress_guard")
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
          parts: [{ type: "text", text: degenPrompt(task, head) }],
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
          await disableDegenmode({ ...state, session_id: eventSessionID }, "session_error")
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
`
	return &Artifact{
		Kind:       KindOpenCode,
		TargetPath: OpenCodePluginPath,
		Executable: false,
		Content:    content,
	}
}

func installGitPreCommit(repoRoot string, force bool) (*InstallReport, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "resolve repo path", Cause: err}
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "repo path does not exist: " + root, Cause: err}
	}
	if !info.IsDir() {
		return nil, &rerrors.PolicySourceError{Message: "repo path is not a directory: " + root}
	}

	gitDir := filepath.Join(root, ".git")
	gitInfo, err := os.Stat(gitDir)
	if err != nil || !gitInfo.IsDir() {
		return nil, &rerrors.PolicySourceError{
			Message: "no .git directory at " + gitDir + "; run `git init` before installing the pre-commit hook",
		}
	}

	hooksDir := filepath.Join(gitDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "create .git/hooks/", Cause: err}
	}
	target := filepath.Join(hooksDir, "pre-commit")

	artifact := generateGitPreCommit()
	action := "created"
	if _, err := os.Stat(target); err == nil {
		if !force {
			return nil, &rerrors.PolicySourceError{
				Message: GitPreCommitPath + " already exists; pass --force to overwrite",
			}
		}
		action = "updated"
	}
	if err := os.WriteFile(target, []byte(artifact.Content), 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "write " + target, Cause: err}
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "chmod " + target, Cause: err}
	}

	return &InstallReport{
		Kind:       KindGitPreCommit,
		RepoRoot:   root,
		TargetPath: GitPreCommitPath,
		Action:     action,
		Executable: true,
		NextAction: "Stage a change and run `git commit` to verify the hook fires; use `git commit --no-verify` to bypass it for a single commit.",
	}, nil
}

func writeGeneratedArtifact(target, content string, executable bool) (string, error) {
	perm := os.FileMode(0o644)
	if executable {
		perm = 0o755
	}
	action := "created"
	if existing, err := os.ReadFile(target); err == nil {
		action = "updated"
		info, statErr := os.Stat(target)
		if statErr == nil && string(existing) == content && info.Mode()&0o777 == perm {
			return "unchanged", nil
		}
	} else if err != nil && !os.IsNotExist(err) {
		return "", &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}
	if err := os.WriteFile(target, []byte(content), perm); err != nil {
		return "", &rerrors.PolicySourceError{Message: "write " + target, Cause: err}
	}
	if err := os.Chmod(target, perm); err != nil {
		return "", &rerrors.PolicySourceError{Message: "chmod " + target, Cause: err}
	}
	return action, nil
}

// installJSONHooks merges reconc's hook entries into a JSON settings
// file (Claude Code / Codex). Preserves any non-reconc keys the user
// has set. Idempotent: reconc-owned entries (identified by a repo-local
// wrapper/runtime signature) are replaced on each run, so running
// `reconc hook install claude-code` twice produces identical output.
//
// Behaviour:
//   - Target missing       -> write the generated artefact verbatim.
//   - Target empty or "{}" -> treat as missing.
//   - Target has content   -> parse, merge, write.
//   - Non-reconc keys at any depth are preserved.
//   - Malformed JSON       -> error unless --force (then overwrite).
func installJSONHooks(kind, relPath, repoRoot string, force bool) (*InstallReport, error) {
	var mergeDiff MergeDiff
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "resolve repo path", Cause: err}
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, &rerrors.PolicySourceError{Message: "repo path is not a directory: " + root, Cause: err}
	}

	target := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "create parent dir of " + target, Cause: err}
	}

	artifact, err := Generate(kind)
	if err != nil {
		return nil, err
	}
	var reconcPart map[string]interface{}
	if err := json.Unmarshal([]byte(artifact.Content), &reconcPart); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "internal: generated artifact is not valid JSON", Cause: err}
	}

	action := "created"
	existing, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}

	var merged map[string]interface{}
	if len(existing) == 0 || strings.TrimSpace(string(existing)) == "{}" {
		merged = reconcPart
	} else {
		action = "updated"
		if err := json.Unmarshal(existing, &merged); err != nil {
			if !force {
				return nil, &rerrors.PolicySourceError{
					Message: target + " is not valid JSON; pass --force to overwrite with a fresh reconc config",
					Cause:   err,
				}
			}
			merged = reconcPart
		} else {
			// Collect dropped user-modified reconc entries so the caller
			// can warn. KeepUserEdits is false by default to preserve
			// reinstall semantics. Stored in the InstallReport.
			mergeDiff = mergeReconcHooks(merged, reconcPart, MergeOptions{KeepUserEdits: false})
		}
	}

	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "marshal merged config", Cause: err}
	}
	if err := os.WriteFile(target, append(out, '\n'), 0o644); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "write " + target, Cause: err}
	}

	nextAction := "Restart your agent session so it picks up the new hooks."
	return &InstallReport{
		Kind:             kind,
		RepoRoot:         root,
		TargetPath:       relPath,
		Action:           action,
		Executable:       false,
		NextAction:       nextAction,
		DroppedUserEdits: mergeDiff.Removed,
	}, nil
}

func installOpenCode(repoRoot string, force bool) (*InstallReport, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "resolve repo path", Cause: err}
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, &rerrors.PolicySourceError{Message: "repo path is not a directory: " + root, Cause: err}
	}
	target := filepath.Join(root, OpenCodePluginPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "create parent dir of " + target, Cause: err}
	}
	artifact := generateOpenCode()
	action := "created"
	if existing, err := os.ReadFile(target); err == nil {
		action = "updated"
		text := string(existing)
		if !force && !strings.Contains(text, "Managed by reconc") && !strings.Contains(text, "reconc hook runtime") {
			return nil, &rerrors.PolicySourceError{
				Message: OpenCodePluginPath + " already exists and is not reconc-managed; pass --force to overwrite",
			}
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}
	if err := os.WriteFile(target, []byte(artifact.Content), 0o644); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "write " + target, Cause: err}
	}
	return &InstallReport{
		Kind:       KindOpenCode,
		RepoRoot:   root,
		TargetPath: OpenCodePluginPath,
		Action:     action,
		Executable: false,
		NextAction: "Restart OpenCode in this repository so it loads .opencode/plugins/reconc.js.",
	}, nil
}

func installAntigravity(repoRoot string, force bool) (*InstallReport, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "resolve repo path", Cause: err}
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, &rerrors.PolicySourceError{Message: "repo path is not a directory: " + root, Cause: err}
	}
	target := filepath.Join(root, AntigravityHooksPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "create parent dir of " + target, Cause: err}
	}
	artifact := generateAntigravity()
	var reconcPart map[string]interface{}
	if err := json.Unmarshal([]byte(artifact.Content), &reconcPart); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "internal: generated Antigravity artifact is not valid JSON", Cause: err}
	}

	action := "created"
	existing, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}

	merged := map[string]interface{}{}
	if len(existing) == 0 || strings.TrimSpace(string(existing)) == "{}" {
		merged = reconcPart
	} else {
		action = "updated"
		if err := json.Unmarshal(existing, &merged); err != nil {
			if !force {
				return nil, &rerrors.PolicySourceError{
					Message: target + " is not valid JSON; pass --force to overwrite with a fresh reconc config",
					Cause:   err,
				}
			}
			merged = reconcPart
		} else if existingReconc, ok := merged["reconc"]; ok && !force && !antigravityHookObjectIsReconcManaged(existingReconc) {
			return nil, &rerrors.PolicySourceError{
				Message: AntigravityHooksPath + " has a non-reconc top-level `reconc` hook; pass --force to overwrite it",
			}
		} else {
			merged["reconc"] = reconcPart["reconc"]
		}
	}

	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "marshal merged Antigravity hooks", Cause: err}
	}
	if err := os.WriteFile(target, append(out, '\n'), 0o644); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "write " + target, Cause: err}
	}
	return &InstallReport{
		Kind:       KindAntigravity,
		RepoRoot:   root,
		TargetPath: AntigravityHooksPath,
		Action:     action,
		Executable: false,
		NextAction: "Restart Antigravity CLI in this repository so it reloads .agents/hooks.json.",
	}, nil
}

func antigravityHookObjectIsReconcManaged(value interface{}) bool {
	body, err := json.Marshal(value)
	if err != nil {
		return false
	}
	text := string(body)
	return strings.Contains(text, "reconc hook runtime ") ||
		(strings.Contains(text, "tools/reconc/dist/reconc-") && strings.Contains(text, " hook runtime ")) ||
		strings.Contains(text, "tools/reconc/bin/hook")
}

// mergeReconcHooks merges reconcPart['hooks'] into dest['hooks'].
// For each current event key (SessionStart, PreToolUse, etc.), removes
// existing reconc-owned hook entries (runtime commands that call reconc)
// and appends the current generator's entries. For stale event keys that
// no longer exist in the generator, reconc-owned entries are removed while
// user-owned entries are preserved. Non-hook keys in dest are untouched.
// Non-reconc hook entries that the user may have added by hand are
// preserved.
// MergeOptions controls the hook-config merge behaviour. It distinguishes
// canonical reconc entries from user-modified reconc entries.
type MergeOptions struct {
	// KeepUserEdits preserves ModifiedReconc entries (entries whose
	// command contains a reconc runtime invocation but doesn't match
	// the generator's current canonical string). Default false --
	// the merge drops them but reports them via Removed so the
	// caller can surface a stderr warning.
	KeepUserEdits bool
}

// MergeDiff describes what mergeReconcHooks did per event. Used by
// the Install layer to emit informative warnings when the merge had
// to clobber user customisations.
type MergeDiff struct {
	// Removed is a list of "event:command" strings that were classified
	// as ModifiedReconc and dropped (unless KeepUserEdits is true).
	Removed []string
	// Kept is a list of modified-reconc entries preserved because
	// KeepUserEdits was set.
	Kept []string
}

func mergeReconcHooks(dest, reconcPart map[string]interface{}, opts MergeOptions) MergeDiff {
	var diff MergeDiff
	reconcHooks, ok := reconcPart["hooks"].(map[string]interface{})
	if !ok {
		return diff
	}
	destHooks, ok := dest["hooks"].(map[string]interface{})
	if !ok {
		destHooks = map[string]interface{}{}
		dest["hooks"] = destHooks
	}

	for event, newEntriesRaw := range reconcHooks {
		newEntries, _ := newEntriesRaw.([]interface{})

		// Validate the destination event's type before treating it as an
		// array. If the user hand-edited their
		// settings into a non-array shape (e.g. wrapped in an object
		// by mistake), we MUST NOT silently replace it -- surface the
		// event and its observed type via the MergeDiff so the caller
		// can warn. Currently we still replace it (otherwise the
		// install does nothing), but the warning makes the behaviour
		// visible.
		var existingEntries []interface{}
		if raw, ok := destHooks[event]; ok && raw != nil {
			arr, isArr := raw.([]interface{})
			if !isArr {
				diff.Removed = append(diff.Removed,
					event+": (non-array "+describeJSONType(raw)+" overwritten)")
			} else {
				existingEntries = arr
			}
		}

		// Build per-event canonical signature for classification. We
		// include args because Claude Code exec-form hooks use the same
		// command path for every event and distinguish routes by argv.
		canonical := firstHookSignature(newEntries)

		filtered := make([]interface{}, 0, len(existingEntries))
		for _, e := range existingEntries {
			switch classifyHookEntry(e, canonical) {
			case NonReconc:
				filtered = append(filtered, e)
			case CanonicalReconc:
				// Drop silently; about to be re-added from newEntries.
			case ModifiedReconc:
				cmd := firstHookCommand([]interface{}{e})
				if opts.KeepUserEdits {
					filtered = append(filtered, e)
					diff.Kept = append(diff.Kept, event+": "+cmd)
				} else {
					diff.Removed = append(diff.Removed, event+": "+cmd)
				}
			}
		}
		filtered = append(filtered, newEntries...)
		destHooks[event] = filtered
	}
	for event, existingRaw := range destHooks {
		if _, stillGenerated := reconcHooks[event]; stillGenerated {
			continue
		}
		existingEntries, ok := existingRaw.([]interface{})
		if !ok {
			continue
		}
		filtered := make([]interface{}, 0, len(existingEntries))
		for _, e := range existingEntries {
			switch classifyHookEntry(e, "") {
			case NonReconc:
				filtered = append(filtered, e)
			case CanonicalReconc, ModifiedReconc:
				cmd := firstHookCommand([]interface{}{e})
				if opts.KeepUserEdits {
					filtered = append(filtered, e)
					diff.Kept = append(diff.Kept, event+": "+cmd)
				} else {
					diff.Removed = append(diff.Removed, event+": "+cmd)
				}
			}
		}
		if len(filtered) == 0 {
			delete(destHooks, event)
			continue
		}
		destHooks[event] = filtered
	}
	return diff
}

// describeJSONType returns a human-readable label for a
// json.Unmarshal'd value's concrete Go type, mapped to the JSON
// vocabulary users actually recognise. Used in merge warnings so
// "your hooks.SessionStart is an object" lands better than
// "your hooks.SessionStart is a map[string]interface {}".
func describeJSONType(v interface{}) string {
	switch v.(type) {
	case map[string]interface{}:
		return "object"
	case []interface{}:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	}
	return "unknown"
}

// firstHookCommand returns the command string of the first direct
// command or hooks[0].command in the given entries list, or "" if absent.
// Helper for classifier + diff reporting.
func firstHookCommand(entries []interface{}) string {
	if len(entries) == 0 {
		return ""
	}
	m, ok := entries[0].(map[string]interface{})
	if !ok {
		return ""
	}
	if cmd, _ := m["command"].(string); strings.TrimSpace(cmd) != "" {
		return strings.TrimSpace(cmd)
	}
	hookList, ok := m["hooks"].([]interface{})
	if !ok || len(hookList) == 0 {
		return ""
	}
	hm, ok := hookList[0].(map[string]interface{})
	if !ok {
		return ""
	}
	cmd, _ := hm["command"].(string)
	return strings.TrimSpace(cmd)
}

func firstHookSignature(entries []interface{}) string {
	if len(entries) == 0 {
		return ""
	}
	return hookEntrySignature(entries[0])
}

func hookEntrySignature(entry interface{}) string {
	m, ok := entry.(map[string]interface{})
	if !ok {
		return ""
	}
	if cmd, _ := m["command"].(string); strings.TrimSpace(cmd) != "" {
		return commandSignature(cmd, m["args"])
	}
	hookList, ok := m["hooks"].([]interface{})
	if !ok || len(hookList) == 0 {
		return ""
	}
	hm, ok := hookList[0].(map[string]interface{})
	if !ok {
		return ""
	}
	cmd, _ := hm["command"].(string)
	return commandSignature(cmd, hm["args"])
}

func commandSignature(command string, argsValue interface{}) string {
	parts := []string{strings.TrimSpace(command)}
	if args, ok := argsValue.([]interface{}); ok {
		for _, raw := range args {
			if s, ok := raw.(string); ok {
				parts = append(parts, s)
			} else {
				parts = append(parts, fmt.Sprintf("%v", raw))
			}
		}
	}
	return strings.Join(parts, "\x00")
}

// HookEntryClass classifies a hooks array entry in a JSON settings
// file so the merge logic can treat canonical reconc entries,
// user-edited reconc entries, and unrelated user entries differently.
type HookEntryClass int

const (
	// NonReconc is any entry that does not reference reconc runtime.
	// Preserved on install.
	NonReconc HookEntryClass = iota
	// CanonicalReconc is a reconc-owned entry whose command matches
	// the generator's current canonical form. Replaced silently on
	// install (idempotent).
	CanonicalReconc
	// ModifiedReconc is a reconc-owned entry (command contains
	// a reconc runtime invocation) but differs from the canonical form --
	// likely the user hand-edited it. Replaced on install by default,
	// preserved when --keep-user-edits is set.
	ModifiedReconc
)

// classifyHookEntry returns the classification for a single entry in
// a hooks.<event> array. Looks at Cursor-style direct `command` first,
// then Claude/Codex-style `hooks[0].command`; the generator never emits
// multi-hook entries so this is the correct granularity.
func classifyHookEntry(entry interface{}, canonicalSignature string) HookEntryClass {
	m, ok := entry.(map[string]interface{})
	if !ok {
		return NonReconc
	}
	cmd, _ := m["command"].(string)
	if strings.TrimSpace(cmd) == "" {
		hookList, ok := m["hooks"].([]interface{})
		if !ok || len(hookList) == 0 {
			return NonReconc
		}
		hm, ok := hookList[0].(map[string]interface{})
		if !ok {
			return NonReconc
		}
		cmd, _ = hm["command"].(string)
	}
	// Generated hooks may call a repo-local binary directly or wrap the
	// runtime invocation in a shell resolver so it works without PATH. We
	// still classify both shapes as reconc-owned.
	trimmed := strings.TrimSpace(cmd)
	if !strings.Contains(trimmed, "reconc hook runtime ") &&
		!(strings.Contains(trimmed, "tools/reconc/dist/reconc-") && strings.Contains(trimmed, " hook runtime ")) &&
		!strings.Contains(trimmed, "tools/reconc/bin/hook") {
		return NonReconc
	}
	if canonicalSignature != "" && hookEntrySignature(entry) == canonicalSignature {
		return CanonicalReconc
	}
	return ModifiedReconc
}
