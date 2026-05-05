// Package hooks generates and installs platform-specific hook
// artifacts that wire reconc into git, Claude Code, and Codex.
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
	GitPreCommitPath       = ".git/hooks/pre-commit"
	ClaudeCodeSettingsPath = ".claude/settings.json"
	CodexHooksPath         = ".codex/hooks.json"
)

// Supported hook kinds.
const (
	KindGitPreCommit = "git-pre-commit"
	KindClaudeCode   = "claude-code"
	KindCodex        = "codex"
)

// SupportedKinds returns every kind reconc hook generate can produce.
func SupportedKinds() []string {
	return []string{KindGitPreCommit, KindClaudeCode, KindCodex}
}

// InstallableKinds returns the kinds that reconc hook install can
// write directly. All three supported kinds are now installable
// (Claude Code / Codex configs are merged non-destructively; git
// pre-commit is a fresh file write).
func InstallableKinds() []string {
	return []string{KindGitPreCommit, KindClaudeCode, KindCodex}
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

// Generate dispatches to the per-kind generator.
func Generate(kind string) (*Artifact, error) {
	switch kind {
	case KindGitPreCommit:
		return generateGitPreCommit(), nil
	case KindClaudeCode:
		return generateClaudeCode(), nil
	case KindCodex:
		return generateCodex(), nil
	}
	return nil, &rerrors.PolicySourceError{
		Message: fmt.Sprintf("unknown hook kind: %q (supported: %v)", kind, SupportedKinds()),
	}
}

// Install writes an installable hook into the repo. Refuses to
// overwrite an existing hook unless force is true.
//
// All three supported kinds are installable:
//   - git-pre-commit: creates .git/hooks/pre-commit (fresh file write,
//     refuses to clobber an existing hook unless --force is set)
//   - claude-code: merges reconc hook entries into .claude/settings.json
//     non-destructively. Idempotent: reconc-owned hook entries are
//     identified by their "reconc hook runtime" command prefix and
//     replaced wholesale on each install; non-reconc keys are preserved.
//   - codex: same merge strategy for .codex/hooks.json.
func Install(kind, repoRoot string, force bool) (*InstallReport, error) {
	switch kind {
	case KindGitPreCommit:
		return installGitPreCommit(repoRoot, force)
	case KindClaudeCode:
		return installJSONHooks(KindClaudeCode, ClaudeCodeSettingsPath, repoRoot, force)
	case KindCodex:
		return installJSONHooks(KindCodex, CodexHooksPath, repoRoot, force)
	}
	return nil, &rerrors.PolicySourceError{
		Message: fmt.Sprintf("unknown installable hook kind: %q (installable: %v)", kind, InstallableKinds()),
	}
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

if ! command -v reconc >/dev/null 2>&1; then
    echo "reconc pre-commit hook: 'reconc' is not on PATH; skipping policy check" >&2
    echo "  install reconc or remove .git/hooks/pre-commit to silence this notice" >&2
    exit 0
fi

repo_root=$(git rev-parse --show-toplevel)
exec reconc ci "$repo_root" --staged
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
	template := map[string]interface{}{
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": `reconc hook runtime claude-session-start "$CLAUDE_PROJECT_DIR"`,
						},
					},
				},
			},
			"PreToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "Edit|Write|MultiEdit",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": `reconc hook runtime claude-pre-tool-use "$CLAUDE_PROJECT_DIR"`,
						},
					},
				},
			},
			"PostToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "Read|Edit|Write|MultiEdit|Bash",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": `reconc hook runtime claude-post-tool-use "$CLAUDE_PROJECT_DIR"`,
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
							"command": `reconc hook runtime claude-post-tool-use-failure "$CLAUDE_PROJECT_DIR"`,
						},
					},
				},
			},
			"Stop": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": `reconc hook runtime claude-stop "$CLAUDE_PROJECT_DIR"`,
						},
					},
				},
			},
			"SessionEnd": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": `reconc hook runtime claude-session-end "$CLAUDE_PROJECT_DIR"`,
						},
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
	// Codex hooks: SessionStart + Bash-only PreToolUse/PostToolUse + Stop.
	template := map[string]interface{}{
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				map[string]interface{}{
					"matcher": "startup",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":          "command",
							"command":       "reconc hook runtime codex-session-start .",
							"statusMessage": "reconc: initializing policy session",
						},
					},
				},
			},
			"PreToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "Bash",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": "reconc hook runtime codex-pre-tool-use .",
						},
					},
				},
			},
			"PostToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "Bash",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": "reconc hook runtime codex-post-tool-use .",
						},
					},
				},
			},
			"Stop": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": "reconc hook runtime codex-stop .",
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

// installJSONHooks merges reconc's hook entries into a JSON settings
// file (Claude Code / Codex). Preserves any non-reconc keys the user
// has set. Idempotent: reconc-owned entries (identified by a
// "reconc hook runtime" command prefix) are replaced on each run, so
// running `reconc hook install claude-code` twice produces identical
// output.
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

// mergeReconcHooks merges reconcPart['hooks'] into dest['hooks'].
// For each event key (SessionStart, PreToolUse, etc.), removes any
// existing reconc-owned hook entries (those whose command starts
// with "reconc hook runtime") and appends the current generator's
// entries. Non-hook keys in dest are untouched. Non-reconc hook
// entries that the user may have added by hand are preserved.
// MergeOptions controls the hook-config merge behaviour. It distinguishes
// canonical reconc entries from user-modified reconc entries.
type MergeOptions struct {
	// KeepUserEdits preserves ModifiedReconc entries (entries whose
	// command starts with "reconc hook runtime " but doesn't match
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

		// Build per-event canonical command for classification. We
		// use the first entry's command since the generator only
		// ever emits one reconc entry per event.
		canonical := firstHookCommand(newEntries)

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

// firstHookCommand returns the command string of the first
// hooks[0].command in the given entries list, or "" if absent.
// Helper for classifier + diff reporting.
func firstHookCommand(entries []interface{}) string {
	if len(entries) == 0 {
		return ""
	}
	m, ok := entries[0].(map[string]interface{})
	if !ok {
		return ""
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
	// ModifiedReconc is a reconc-owned entry (command starts with
	// "reconc hook runtime ") but differs from the canonical form --
	// likely the user hand-edited it. Replaced on install by default,
	// preserved when --keep-user-edits is set.
	ModifiedReconc
)

// classifyHookEntry returns the classification for a single entry in
// a hooks.<event> array. Looks only at the `hooks[0].command` string
// -- the generator never emits multi-hook entries so this is the
// correct granularity.
func classifyHookEntry(entry interface{}, canonicalCommand string) HookEntryClass {
	m, ok := entry.(map[string]interface{})
	if !ok {
		return NonReconc
	}
	hookList, ok := m["hooks"].([]interface{})
	if !ok || len(hookList) == 0 {
		return NonReconc
	}
	hm, ok := hookList[0].(map[string]interface{})
	if !ok {
		return NonReconc
	}
	cmd, _ := hm["command"].(string)
	// Prefix-based detection. `strings.Contains` would over-match on
	// unrelated entries that merely mention reconc in a comment-like
	// field; a strict HasPrefix is both tighter and cheaper.
	if !strings.HasPrefix(strings.TrimSpace(cmd), "reconc hook runtime ") {
		return NonReconc
	}
	if canonicalCommand != "" && strings.TrimSpace(cmd) == strings.TrimSpace(canonicalCommand) {
		return CanonicalReconc
	}
	return ModifiedReconc
}
