// Package hooks generates, installs, and inspects platform-specific hook
// artifacts that wire reconc into git and supported agent runtimes.
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
	DevinHooksPath           = ".devin/hooks.v1.json"
	AntigravityHooksPath     = ".agents/hooks.json"
	CopilotHooksPath         = ".github/hooks/reconc.json"
	KiloPluginPath           = ".kilo/plugin/reconc.js"
)

// Supported hook kinds.
const (
	KindGitPreCommit = "git-pre-commit"
	KindClaudeCode   = "claude-code"
	KindCodex        = "codex"
	KindCursor       = "cursor"
	KindOpenCode     = "opencode"
	KindDevinCLI     = "devin-cli"
	KindAntigravity  = "antigravity"
	KindCopilot      = "copilot"
	KindKilo         = "kilo"
)

// SupportedKinds returns every kind reconc hook generate can produce.
func SupportedKinds() []string {
	return platformKinds(false)
}

// InstallableKinds returns the kinds that reconc hook install can write
// directly. JSON configs are merged non-destructively; an unchanged managed
// git hook is reused without another filesystem write.
func InstallableKinds() []string {
	return platformKinds(false)
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
	Action     string `json:"action"` // "created" | "updated" | "unchanged"
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

// Generate dispatches through the platform registry.
func Generate(kind string) (*Artifact, error) {
	definition, ok := lookupPlatformDefinition(kind)
	if !ok {
		return nil, &rerrors.PolicySourceError{
			Message: fmt.Sprintf("unknown hook kind: %q (supported: %v)", kind, SupportedKinds()),
		}
	}
	switch definition.generator {
	case generatorGitPreCommit:
		return generateGitPreCommit(), nil
	case generatorClaudeCode:
		return generateClaudeCode(), nil
	case generatorCodex:
		return generateCodex(), nil
	case generatorCursor:
		return generateCursor(), nil
	case generatorOpenCode:
		return generateOpenCodeThin(), nil
	case generatorDevinCLI:
		return generateDevinCLI(), nil
	case generatorAntigravity:
		return generateAntigravity(), nil
	case generatorCopilot:
		return generateCopilot(), nil
	case generatorKilo:
		return generateKilo(), nil
	}
	return nil, &rerrors.PolicySourceError{Message: fmt.Sprintf("hook kind %q has no generator", kind)}
}

// Install writes an installable hook into the repo. Refuses to
// overwrite an existing hook unless force is true.
//
// Supported kinds are installable:
//   - git-pre-commit: creates .git/hooks/pre-commit, reuses an identical
//     managed hook, and refuses to clobber different content unless --force
//   - claude-code: merges reconc hook entries into .claude/settings.json
//     non-destructively. Idempotent: reconc-owned hook entries are
//     identified by their repo-local wrapper/runtime signature and replaced
//     wholesale on each install; non-reconc keys are preserved.
//   - codex: same merge strategy for .codex/hooks.json.
//   - cursor: same merge strategy for .cursor/hooks.json.
//   - opencode: writes .opencode/plugins/reconc.js as a project-local
//     plugin, refusing to clobber non-reconc plugin content unless
//     --force is set.
//   - JSON platforms merge reconc-owned entries without replacing unrelated
//     hooks; plugin platforms replace only reconc-managed plugin files.
func Install(kind, repoRoot string, force bool) (*InstallReport, error) {
	definition, ok := lookupPlatformDefinition(kind)
	if !ok {
		return nil, &rerrors.PolicySourceError{
			Message: fmt.Sprintf("unknown installable hook kind: %q (installable: %v)", kind, InstallableKinds()),
		}
	}
	switch definition.InstallMode {
	case InstallExecutable:
		return installGitPreCommit(repoRoot, force)
	case InstallNestedJSON:
		return installJSONHooks(kind, definition.TargetPath, repoRoot, force)
	case InstallFlatJSON:
		return installDevinCLI(repoRoot, force)
	case InstallOwnedJSON:
		return installAntigravity(repoRoot, force)
	case InstallManagedJSON:
		return installCopilot(repoRoot, force)
	case InstallPlugin:
		if kind == KindOpenCode {
			return installOpenCode(repoRoot, force)
		}
		return installKilo(repoRoot, force)
	}
	return nil, &rerrors.PolicySourceError{Message: fmt.Sprintf("hook kind %q has no installer", kind)}
}

// ScaffoldKinds returns every generated hook artifact that belongs in
// repo-root-scaffold. Git pre-commit is mapped to .githooks/pre-commit
// because .git/hooks is clone-local and cannot be source-controlled.
func ScaffoldKinds() []string {
	return platformKinds(false)
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
	if existing, err := os.ReadFile(target); err == nil {
		info, statErr := os.Stat(target)
		if statErr != nil {
			return nil, &rerrors.PolicySourceError{Message: "stat " + target, Cause: statErr}
		}
		if string(existing) == artifact.Content && info.Mode().Perm() == 0o755 {
			action = "unchanged"
		} else if !force {
			return nil, &rerrors.PolicySourceError{
				Message: GitPreCommitPath + " already exists; pass --force to overwrite",
			}
		} else {
			action = "updated"
		}
	} else if !os.IsNotExist(err) {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}
	if action != "unchanged" {
		writeAction, err := writeGeneratedArtifact(target, artifact.Content, true)
		if err != nil {
			return nil, err
		}
		action = writeAction
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

// installJSONHooks merges reconc's hook entries into a nested JSON settings
// file. Preserves any non-reconc keys the user
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
	if writeAction, err := writeGeneratedArtifact(target, string(append(out, '\n')), false); err != nil {
		return nil, err
	} else if writeAction == "unchanged" {
		action = writeAction
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
	artifact, err := Generate(KindOpenCode)
	if err != nil {
		return nil, err
	}
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
	if writeAction, err := writeGeneratedArtifact(target, artifact.Content, false); err != nil {
		return nil, err
	} else if writeAction == "unchanged" {
		action = writeAction
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
	if writeAction, err := writeGeneratedArtifact(target, string(append(out, '\n')), false); err != nil {
		return nil, err
	} else if writeAction == "unchanged" {
		action = writeAction
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
