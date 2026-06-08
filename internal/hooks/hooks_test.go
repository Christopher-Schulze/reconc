package hooks

import (
	"encoding/json"
	stderrors "errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	rerrors "reconc.dev/reconc/internal/errors"
)

func TestGenerateGitPreCommitContainsReconcCI(t *testing.T) {
	a, err := Generate(KindGitPreCommit)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if a.Kind != KindGitPreCommit {
		t.Errorf("kind wrong: %s", a.Kind)
	}
	if !a.Executable {
		t.Error("git pre-commit should be marked executable")
	}
	if !strings.Contains(a.Content, "reconc ci") {
		t.Errorf("content should reference `reconc ci`, got: %s", a.Content)
	}
	for _, token := range []string{
		`release_reconc="reconc-0.5.0-$reconc_os-$reconc_arch$reconc_ext"`,
		`"$repo_root/tools/reconc/dist/$release_reconc"`,
		`"$repo_root/dist/$release_reconc"`,
		`"$repo_root/.build/bin/reconc"`,
		`"$repo_root/reconc"`,
	} {
		if !strings.Contains(a.Content, token) {
			t.Errorf("pre-commit missing %q:\n%s", token, a.Content)
		}
	}
	if !strings.HasPrefix(a.Content, "#!/bin/sh") {
		t.Errorf("content should start with shebang, got: %s", a.Content[:50])
	}
}

func TestGenerateClaudeCodeIsValidJSON(t *testing.T) {
	a, err := Generate(KindClaudeCode)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(a.Content), &payload); err != nil {
		t.Fatalf("content is not valid JSON: %v\n%s", err, a.Content)
	}
	if _, ok := payload["hooks"]; !ok {
		t.Errorf("expected 'hooks' key in Claude Code template")
	}
	if !strings.Contains(a.Content, "tools/reconc/bin/hook") ||
		!strings.Contains(a.Content, "claude-pre-tool-use") ||
		strings.Contains(a.Content, "reconc hook runtime") {
		t.Errorf("expected reconc routes in template")
	}
	if !strings.Contains(a.Content, "claude-user-prompt-submit") {
		t.Errorf("expected Claude Code user-prompt route in template")
	}
	if !strings.Contains(a.Content, "claude-permission-request") {
		t.Errorf("expected Claude Code permission-request route in template")
	}
	if !strings.Contains(a.Content, "tools/reconc/bin/hook") {
		t.Errorf("expected Claude Code hooks to prefer repo-local wrapper")
	}
	if !strings.Contains(a.Content, `"args": [`) ||
		!strings.Contains(a.Content, `"${CLAUDE_PROJECT_DIR}"`) {
		t.Errorf("expected Claude Code hooks to use exec-form args with CLAUDE_PROJECT_DIR")
	}
	if strings.Contains(a.Content, "sh -lc") ||
		strings.Contains(a.Content, `git -C \"$repo\" rev-parse --show-toplevel`) {
		t.Errorf("Claude Code hooks should use exec form without shell/git launcher")
	}
	if strings.Contains(a.Content, "tools/reconc/dist/reconc-0.5.0-darwin-arm64") {
		t.Errorf("Claude Code hooks should delegate binary fallback to the repo-local wrapper")
	}
	if !strings.Contains(a.Content, `"matcher": "Edit|Write|MultiEdit|Bash"`) {
		t.Errorf("expected Claude Code PreToolUse/PermissionRequest to cover write/shell matchers")
	}
	if got := matchersForEvent(t, a.Content, "hooks", "PreToolUse"); strings.Join(got, "|") != "Edit|Write|MultiEdit|Bash" {
		t.Errorf("Claude PreToolUse matcher = %v", got)
	}
	if got := matchersForEvent(t, a.Content, "hooks", "PermissionRequest"); strings.Join(got, "|") != "Edit|Write|MultiEdit|Bash" {
		t.Errorf("Claude PermissionRequest matcher = %v", got)
	}
	if got := matchersForEvent(t, a.Content, "hooks", "PostToolUse"); strings.Join(got, "|") != "Read|Edit|Write|MultiEdit|Bash" {
		t.Errorf("Claude PostToolUse matcher = %v", got)
	}
}

func TestGenerateCodexIsValidJSON(t *testing.T) {
	a, err := Generate(KindCodex)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(a.Content), &payload); err != nil {
		t.Fatalf("content is not valid JSON: %v\n%s", err, a.Content)
	}
	if !strings.Contains(a.Content, "codex-pre-tool-use") {
		t.Errorf("expected codex routes in template")
	}
	if !strings.Contains(a.Content, "codex-session-end") {
		t.Errorf("expected codex session-end route in template")
	}
	if !strings.Contains(a.Content, "codex-post-tool-use-failure") {
		t.Errorf("expected codex post-tool-use-failure route in template")
	}
	if !strings.Contains(a.Content, "codex-user-prompt-submit") {
		t.Errorf("expected codex user-prompt route in template")
	}
	if !strings.Contains(a.Content, "codex-permission-request") {
		t.Errorf("expected codex permission-request route in template")
	}
	if !strings.Contains(a.Content, "Write|Edit|MultiEdit|Bash|apply_patch") {
		t.Errorf("expected codex pre-execution hooks to cover write/shell/apply_patch matchers")
	}
	if !strings.Contains(a.Content, "Read|Edit|Write|MultiEdit|Bash|apply_patch") {
		t.Errorf("expected codex post hook to keep read/write/shell evidence matchers")
	}
	if strings.Contains(a.Content, `"matcher": "Read|Write|Edit|MultiEdit|Bash|apply_patch"`) {
		t.Errorf("codex pre-execution hooks should not spawn for read-only tools")
	}
	if got := matchersForEvent(t, a.Content, "hooks", "PreToolUse"); strings.Join(got, "|") != "Write|Edit|MultiEdit|Bash|apply_patch" {
		t.Errorf("Codex PreToolUse matcher = %v", got)
	}
	if got := matchersForEvent(t, a.Content, "hooks", "PermissionRequest"); strings.Join(got, "|") != "Write|Edit|MultiEdit|Bash|apply_patch" {
		t.Errorf("Codex PermissionRequest matcher = %v", got)
	}
	if got := matchersForEvent(t, a.Content, "hooks", "PostToolUse"); strings.Join(got, "|") != "Read|Edit|Write|MultiEdit|Bash|apply_patch" {
		t.Errorf("Codex PostToolUse matcher = %v", got)
	}
	if strings.Contains(a.Content, "sh -lc") {
		t.Errorf("Codex hooks should rely on the host shell instead of spawning a nested shell")
	}
}

func TestGenerateCursorIsValidJSON(t *testing.T) {
	a, err := Generate(KindCursor)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(a.Content), &payload); err != nil {
		t.Fatalf("content is not valid JSON: %v\n%s", err, a.Content)
	}
	if a.TargetPath != CursorHooksPath {
		t.Fatalf("target path = %s", a.TargetPath)
	}
	for _, token := range []string{
		`"version": 1`,
		`"preToolUse"`,
		`"beforeSubmitPrompt"`,
		`"beforeShellExecution"`,
		`"afterShellExecution"`,
		`"afterFileEdit"`,
		`"afterTabFileEdit"`,
		`"stop"`,
		`"failClosed": true`,
		`"loop_limit": 10`,
		`"matcher": "Read|Write|Edit|MultiEdit|StrReplace|Delete|FileEdit|TabRead|TabWrite"`,
		`"matcher": "Write|Edit|MultiEdit|StrReplace|Delete|FileEdit|TabWrite"`,
		"cursor-pre-tool-use",
		"cursor-after-file-edit",
		"cursor-stop",
		"tools/reconc/bin/hook",
	} {
		if !strings.Contains(a.Content, token) {
			t.Fatalf("Cursor hook template missing %q:\n%s", token, a.Content)
		}
	}
	for _, forbidden := range []string{"beforeReadFile", "beforeTabFileRead", "cursor-before-read-file", "cursor-before-tab-file-read"} {
		if strings.Contains(a.Content, forbidden) {
			t.Fatalf("Cursor hook template should not spawn pre-execution hooks for read-only events %q:\n%s", forbidden, a.Content)
		}
	}
	if got := matchersForEvent(t, a.Content, "hooks", "preToolUse"); strings.Join(got, "|") != "Write|Edit|MultiEdit|StrReplace|Delete|FileEdit|TabWrite" {
		t.Errorf("Cursor preToolUse matcher = %v", got)
	}
	if got := matchersForEvent(t, a.Content, "hooks", "postToolUse"); strings.Join(got, "|") != "Read|Write|Edit|MultiEdit|StrReplace|Delete|FileEdit|TabRead|TabWrite" {
		t.Errorf("Cursor postToolUse matcher = %v", got)
	}
}

func TestRuntimeCommandPrefersRepoLocalHookWrapper(t *testing.T) {
	cmd := runtimeCommand("/repo", "cursor-post-tool-use")
	for _, token := range []string{
		`repo="/repo"`,
		`hook="$repo/tools/reconc/bin/hook"`,
		`if [ -x "$hook" ]; then exec "$hook" cursor-post-tool-use "$repo"; fi`,
		`git -C "$repo" rev-parse --show-toplevel`,
		`RECONC_HOOK_REPO_RESOLVED=1 exec "$repo/tools/reconc/bin/hook" cursor-post-tool-use "$repo"`,
	} {
		if !strings.Contains(cmd, token) {
			t.Fatalf("runtime command missing %q:\n%s", token, cmd)
		}
	}
	for _, forbidden := range []string{
		`tools/reconc/dist/reconc-0.5.0-darwin-arm64`,
		`exec reconc hook runtime cursor-post-tool-use "$repo"`,
		`for bin in`,
	} {
		if strings.Contains(cmd, forbidden) {
			t.Fatalf("runtime command should delegate %q to wrapper:\n%s", forbidden, cmd)
		}
	}
}

func TestShellRuntimeCommandAvoidsNestedShell(t *testing.T) {
	cmd := shellRuntimeCommand(".", "codex-post-tool-use")
	for _, token := range []string{
		`repo="."`,
		`hook="$repo/tools/reconc/bin/hook"`,
		`if [ -x "$hook" ]; then exec "$hook" codex-post-tool-use "$repo"; fi`,
		`git -C "$repo" rev-parse --show-toplevel`,
		`RECONC_HOOK_REPO_RESOLVED=1 exec "$repo/tools/reconc/bin/hook" codex-post-tool-use "$repo"`,
	} {
		if !strings.Contains(cmd, token) {
			t.Fatalf("shell runtime command missing %q:\n%s", token, cmd)
		}
	}
	if strings.Contains(cmd, "sh -lc") {
		t.Fatalf("shell runtime command must not spawn a nested shell:\n%s", cmd)
	}
}

func TestGenerateAntigravityIsValidJSON(t *testing.T) {
	a, err := Generate(KindAntigravity)
	if err != nil {
		t.Fatalf("generate antigravity: %v", err)
	}
	if a.TargetPath != AntigravityHooksPath {
		t.Fatalf("target path = %s", a.TargetPath)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(a.Content), &payload); err != nil {
		t.Fatalf("content is not valid JSON: %v\n%s", err, a.Content)
	}
	if _, ok := payload["reconc"].(map[string]interface{}); !ok {
		t.Fatalf("expected top-level reconc hook definition, got:\n%s", a.Content)
	}
	for _, token := range []string{
		`"PreInvocation"`,
		`"PreToolUse"`,
		`"PostToolUse"`,
		`"PostInvocation"`,
		`"Stop"`,
		`"matcher": "view_file|write_to_file|replace_file_content|multi_replace_file_content|list_dir|find_by_name|grep_search|run_command"`,
		`"matcher": "write_to_file|replace_file_content|multi_replace_file_content|run_command"`,
		"antigravity-pre-invocation",
		"antigravity-pre-tool-use",
		"antigravity-post-tool-use",
		"antigravity-post-invocation",
		"antigravity-stop",
		"tools/reconc/bin/hook",
	} {
		if !strings.Contains(a.Content, token) {
			t.Fatalf("Antigravity hook template missing %q:\n%s", token, a.Content)
		}
	}
	if got := matchersForEvent(t, a.Content, "reconc", "PreToolUse"); strings.Join(got, "|") != "write_to_file|replace_file_content|multi_replace_file_content|run_command" {
		t.Errorf("Antigravity PreToolUse matcher = %v", got)
	}
	if got := matchersForEvent(t, a.Content, "reconc", "PostToolUse"); strings.Join(got, "|") != "view_file|write_to_file|replace_file_content|multi_replace_file_content|list_dir|find_by_name|grep_search|run_command" {
		t.Errorf("Antigravity PostToolUse matcher = %v", got)
	}
}

func TestGenerateUnknownKind(t *testing.T) {
	_, err := Generate("not-a-kind")
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func matchersForEvent(t *testing.T, content string, rootKey string, event string) []string {
	t.Helper()
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		t.Fatalf("content is not valid JSON: %v", err)
	}
	root, ok := payload[rootKey].(map[string]interface{})
	if !ok {
		t.Fatalf("missing %s map", rootKey)
	}
	rawEntries, ok := root[event].([]interface{})
	if !ok {
		t.Fatalf("missing %s.%s entries", rootKey, event)
	}
	var matchers []string
	for _, raw := range rawEntries {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("%s.%s entry is not an object", rootKey, event)
		}
		if matcher, ok := entry["matcher"].(string); ok {
			matchers = append(matchers, matcher)
		}
	}
	return matchers
}

func TestInstallGitPreCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	c := exec.Command("git", "init", "--quiet")
	c.Dir = repo
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	report, err := Install(KindGitPreCommit, repo, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if report.Action != "created" {
		t.Errorf("expected 'created', got %s", report.Action)
	}
	if report.TargetPath != GitPreCommitPath {
		t.Errorf("target path wrong: %s", report.TargetPath)
	}
	target := filepath.Join(repo, ".git", "hooks", "pre-commit")
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("hook missing: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("hook should be executable")
	}
}

// gitInitRepo is a small helper that runs `git init` in the given dir
// (and only in that dir, not the test process cwd).
func gitInitRepo(t *testing.T, dir string) {
	t.Helper()
	c := exec.Command("git", "init", "--quiet")
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init in %s: %v\n%s", dir, err, out)
	}
}

func TestInstallGitPreCommitRefusesOverwriteWithoutForce(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	gitInitRepo(t, repo)

	if _, err := Install(KindGitPreCommit, repo, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	_, err := Install(KindGitPreCommit, repo, false)
	if err == nil {
		t.Fatal("expected error on second install without force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists', got: %v", err)
	}
}

func TestInstallGitPreCommitForceOverwrites(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	gitInitRepo(t, repo)

	if _, err := Install(KindGitPreCommit, repo, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	report, err := Install(KindGitPreCommit, repo, true)
	if err != nil {
		t.Fatalf("force install: %v", err)
	}
	if report.Action != "updated" {
		t.Errorf("expected 'updated', got %s", report.Action)
	}
}

func TestInstallGitPreCommitNonGitDirReturnsError(t *testing.T) {
	repo := t.TempDir()
	_, err := Install(KindGitPreCommit, repo, false)
	if err == nil {
		t.Fatal("expected error for non-git dir")
	}
	if !strings.Contains(err.Error(), "no .git") {
		t.Errorf("expected 'no .git' in error, got: %v", err)
	}
}

func TestInstallClaudeCodeCreatesFreshFile(t *testing.T) {
	repo := t.TempDir()
	report, err := Install(KindClaudeCode, repo, false)
	if err != nil {
		t.Fatalf("install claude-code: %v", err)
	}
	if report.Action != "created" {
		t.Errorf("expected action=created, got %s", report.Action)
	}
	data, err := os.ReadFile(filepath.Join(repo, ClaudeCodeSettingsPath))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if !strings.Contains(string(data), `tools/reconc/bin/hook`) ||
		!strings.Contains(string(data), `claude-session-start`) ||
		strings.Contains(string(data), `reconc hook runtime`) {
		t.Errorf("expected reconc session-start entry in settings.json, got:\n%s", string(data))
	}
}

func TestInstallClaudeCodeMergesExistingConfig(t *testing.T) {
	repo := t.TempDir()
	// Pre-existing settings.json with a user setting + a hand-written
	// non-reconc hook entry that reconc must NOT remove.
	pre := `{
  "editor": "vscode",
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "echo user-custom" } ] }
    ]
  }
}`
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ClaudeCodeSettingsPath), []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Install(KindClaudeCode, repo, false)
	if err != nil {
		t.Fatalf("install claude-code: %v", err)
	}
	if report.Action != "updated" {
		t.Errorf("expected action=updated, got %s", report.Action)
	}
	data, _ := os.ReadFile(filepath.Join(repo, ClaudeCodeSettingsPath))
	got := string(data)
	// User's non-reconc settings preserved.
	if !strings.Contains(got, `"editor": "vscode"`) {
		t.Errorf("user's editor setting lost:\n%s", got)
	}
	if !strings.Contains(got, `"echo user-custom"`) {
		t.Errorf("user's hand-written hook lost:\n%s", got)
	}
	// reconc's hooks present.
	if !strings.Contains(got, "tools/reconc/bin/hook") ||
		!strings.Contains(got, "claude-session-start") ||
		strings.Contains(got, "reconc hook runtime") {
		t.Errorf("reconc hook not merged in:\n%s", got)
	}
}

func TestInstallClaudeCodeIsIdempotent(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindClaudeCode, repo, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if _, err := Install(KindClaudeCode, repo, false); err != nil {
		t.Fatalf("second install: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, ClaudeCodeSettingsPath))
	// Count how many SessionStart hook entries are reconc-owned.
	// Should be exactly one even after running install twice.
	count := strings.Count(string(data), "claude-session-start")
	if count != 1 {
		t.Errorf("expected exactly 1 reconc session-start entry after double install, got %d\nfile:\n%s",
			count, string(data))
	}
}

func TestInstallCodexCreatesFreshFile(t *testing.T) {
	repo := t.TempDir()
	report, err := Install(KindCodex, repo, false)
	if err != nil {
		t.Fatalf("install codex: %v", err)
	}
	if report.Action != "created" {
		t.Errorf("expected action=created, got %s", report.Action)
	}
	data, _ := os.ReadFile(filepath.Join(repo, CodexHooksPath))
	if !strings.Contains(string(data), "tools/reconc/bin/hook") ||
		!strings.Contains(string(data), "codex-pre-tool-use") ||
		strings.Contains(string(data), "reconc hook runtime") {
		t.Errorf("expected reconc command in codex hooks.json, got:\n%s", string(data))
	}
}

func TestInstallCursorCreatesFreshFile(t *testing.T) {
	repo := t.TempDir()
	report, err := Install(KindCursor, repo, false)
	if err != nil {
		t.Fatalf("install cursor: %v", err)
	}
	if report.Action != "created" {
		t.Errorf("expected action=created, got %s", report.Action)
	}
	data, err := os.ReadFile(filepath.Join(repo, CursorHooksPath))
	if err != nil {
		t.Fatalf("read cursor hooks: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "tools/reconc/bin/hook") ||
		!strings.Contains(content, "cursor-pre-tool-use") ||
		!strings.Contains(content, `"failClosed": true`) ||
		strings.Contains(content, "reconc hook runtime") {
		t.Fatalf("expected Cursor reconc command and failClosed hooks, got:\n%s", content)
	}
}

func TestInstallCursorRemovesStaleReconcReadHooks(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, CursorHooksPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	pre := `{
  "version": 1,
  "hooks": {
    "beforeReadFile": [
      {
        "command": "sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" cursor-before-read-file \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/tools/reconc/bin/hook\" cursor-before-read-file \"$repo\"'",
        "failClosed": true
      }
    ],
    "beforeTabFileRead": [
      {
        "command": "sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" cursor-before-tab-file-read \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/tools/reconc/bin/hook\" cursor-before-tab-file-read \"$repo\"'",
        "failClosed": true
      }
    ]
  }
}`
	if err := os.WriteFile(target, []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Install(KindCursor, repo, false)
	if err != nil {
		t.Fatalf("install cursor: %v", err)
	}
	if len(report.DroppedUserEdits) != 2 {
		t.Fatalf("expected two stale reconc read hooks to be reported as removed, got %v", report.DroppedUserEdits)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, forbidden := range []string{"beforeReadFile", "beforeTabFileRead", "cursor-before-read-file", "cursor-before-tab-file-read"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("stale reconc read hook %q should be removed:\n%s", forbidden, content)
		}
	}
}

func TestInstallCursorPreservesUserOwnedStaleEventEntries(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, CursorHooksPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	pre := `{
  "version": 1,
  "hooks": {
    "beforeReadFile": [
      {
        "command": "echo user-read-hook",
        "failClosed": false
      },
      {
        "command": "sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" cursor-before-read-file \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/tools/reconc/bin/hook\" cursor-before-read-file \"$repo\"'",
        "failClosed": true
      }
    ]
  }
}`
	if err := os.WriteFile(target, []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(KindCursor, repo, false); err != nil {
		t.Fatalf("install cursor: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "echo user-read-hook") {
		t.Fatalf("user-owned stale event entry must be preserved:\n%s", content)
	}
	if strings.Contains(content, "cursor-before-read-file") {
		t.Fatalf("reconc-owned stale read entry must be removed while preserving user entry:\n%s", content)
	}
}

func TestInstallAntigravityCreatesFreshFile(t *testing.T) {
	repo := t.TempDir()
	report, err := Install(KindAntigravity, repo, false)
	if err != nil {
		t.Fatalf("install antigravity: %v", err)
	}
	if report.Action != "created" {
		t.Errorf("expected action=created, got %s", report.Action)
	}
	data, err := os.ReadFile(filepath.Join(repo, AntigravityHooksPath))
	if err != nil {
		t.Fatalf("read antigravity hooks: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "tools/reconc/bin/hook") ||
		!strings.Contains(content, "antigravity-pre-tool-use") ||
		!strings.Contains(content, `"reconc"`) ||
		strings.Contains(content, "reconc hook runtime") {
		t.Fatalf("expected Antigravity reconc command, got:\n%s", content)
	}
}

func TestInstallAntigravityMergesExistingHooks(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, AntigravityHooksPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	pre := `{"user-hook":{"Stop":[{"type":"command","command":"./scripts/user.sh"}]}}`
	if err := os.WriteFile(target, []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Install(KindAntigravity, repo, false)
	if err != nil {
		t.Fatalf("install antigravity: %v", err)
	}
	if report.Action != "updated" {
		t.Errorf("expected updated, got %s", report.Action)
	}
	data, _ := os.ReadFile(target)
	got := string(data)
	if !strings.Contains(got, `"user-hook"`) || !strings.Contains(got, `./scripts/user.sh`) {
		t.Fatalf("user Antigravity hook lost:\n%s", got)
	}
	if !strings.Contains(got, "antigravity-stop") {
		t.Fatalf("reconc Antigravity hook missing:\n%s", got)
	}
}

func TestInstallClaudeCodeMalformedJSONRejected(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ClaudeCodeSettingsPath), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Install(KindClaudeCode, repo, false)
	if err == nil {
		t.Fatal("expected error on malformed JSON without --force")
	}
	// With --force it should overwrite.
	if _, err := Install(KindClaudeCode, repo, true); err != nil {
		t.Errorf("--force should overwrite malformed JSON; got: %v", err)
	}
}

func TestInstallUnknownKind(t *testing.T) {
	_, err := Install("not-a-kind", t.TempDir(), false)
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestPolicySourceErrorTypeUsed(t *testing.T) {
	_, err := Generate("not-a-kind")
	var pse *rerrors.PolicySourceError
	if !stderrors.As(err, &pse) {
		t.Errorf("expected *PolicySourceError, got %T", err)
	}
}

// --- classifier + user-edit surfacing -------------------------------

func TestClassifyHookEntryCanonicalVsModified(t *testing.T) {
	canonical := `reconc hook runtime claude-session-start "$CLAUDE_PROJECT_DIR"`
	canonicalSignature := commandSignature(canonical, nil)
	mkEntry := func(cmd string) map[string]interface{} {
		return map[string]interface{}{
			"hooks": []interface{}{
				map[string]interface{}{"type": "command", "command": cmd},
			},
		}
	}
	cases := []struct {
		name string
		cmd  string
		want HookEntryClass
	}{
		{"canonical", canonical, CanonicalReconc},
		{"user modified with --debug", canonical + " --debug", ModifiedReconc},
		{"user wrapped in sh -c", `sh -c 'reconc hook runtime claude-session-start .'`, ModifiedReconc},
		{"user custom echo", `echo user-custom`, NonReconc},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyHookEntry(mkEntry(c.cmd), canonicalSignature)
			if got != c.want {
				t.Errorf("classifyHookEntry(%q) = %d, want %d", c.cmd, got, c.want)
			}
		})
	}
}

func TestClassifyCursorDirectCommandEntry(t *testing.T) {
	canonical := `sh -lc 'reconc hook runtime cursor-pre-tool-use .'`
	canonicalSignature := commandSignature(canonical, nil)
	entry := map[string]interface{}{
		"command":    canonical,
		"failClosed": true,
	}
	if firstHookCommand([]interface{}{entry}) != canonical {
		t.Fatalf("direct Cursor command was not discovered")
	}
	if got := classifyHookEntry(entry, canonicalSignature); got != CanonicalReconc {
		t.Fatalf("direct Cursor command classified as %d", got)
	}
	modified := map[string]interface{}{"command": canonical + " --debug"}
	if got := classifyHookEntry(modified, canonicalSignature); got != ModifiedReconc {
		t.Fatalf("modified direct Cursor command classified as %d", got)
	}
}

func TestClassifyClaudeExecArgsByFullArgv(t *testing.T) {
	canonical := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": "${CLAUDE_PROJECT_DIR}/tools/reconc/bin/hook",
				"args":    []interface{}{"claude-session-start", "${CLAUDE_PROJECT_DIR}"},
			},
		},
	}
	canonicalSignature := hookEntrySignature(canonical)
	if got := classifyHookEntry(canonical, canonicalSignature); got != CanonicalReconc {
		t.Fatalf("canonical exec-form hook classified as %d", got)
	}
	wrongArgs := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": "${CLAUDE_PROJECT_DIR}/tools/reconc/bin/hook",
				"args":    []interface{}{"claude-stop", "${CLAUDE_PROJECT_DIR}"},
			},
		},
	}
	if got := classifyHookEntry(wrongArgs, canonicalSignature); got != ModifiedReconc {
		t.Fatalf("same command with wrong args must be modified, got %d", got)
	}
}

func TestAntigravityManagedDetectionAcceptsWrapper(t *testing.T) {
	managed := map[string]interface{}{
		"PreInvocation": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": `sh -lc 'repo="."; exec "$repo/tools/reconc/bin/hook" antigravity-pre-invocation "$repo"'`,
			},
		},
	}
	if !antigravityHookObjectIsReconcManaged(managed) {
		t.Fatal("expected repo-local wrapper Antigravity hook to be reconc-managed")
	}
	unrelated := map[string]interface{}{
		"PreInvocation": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": `./scripts/custom-hook.sh`,
			},
		},
	}
	if antigravityHookObjectIsReconcManaged(unrelated) {
		t.Fatal("custom Antigravity hook must not be treated as reconc-managed")
	}
}

func TestInstallReportsDroppedUserEdits(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing settings with a user-modified reconc entry (adds
	// --debug to the canonical command).
	preExisting := `{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "reconc hook runtime claude-session-start --debug \"$CLAUDE_PROJECT_DIR\"" } ] }
    ]
  }
}`
	if err := os.WriteFile(filepath.Join(repo, ClaudeCodeSettingsPath), []byte(preExisting), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Install(KindClaudeCode, repo, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(report.DroppedUserEdits) == 0 {
		t.Fatal("expected DroppedUserEdits to report the --debug entry")
	}
	found := false
	for _, e := range report.DroppedUserEdits {
		if strings.Contains(e, "--debug") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected --debug entry in DroppedUserEdits, got %v", report.DroppedUserEdits)
	}
}

func TestGenerateOpenCodePlugin(t *testing.T) {
	a, err := Generate(KindOpenCode)
	if err != nil {
		t.Fatalf("generate opencode: %v", err)
	}
	if a.TargetPath != OpenCodePluginPath {
		t.Fatalf("target path wrong: %s", a.TargetPath)
	}
	for _, token := range []string{
		"tool.execute.before",
		"tool.execute.after",
		"chat.message",
		"session.idle",
		"opencode-pre-tool-use",
		"opencode-post-tool-use",
		"opencode-stop",
		"reconcReleaseName",
		`repo + "/tools/reconc/dist/" + reconcReleaseName`,
		`repo + "/dist/" + reconcReleaseName`,
		`Bun.spawn(["git", "-C", candidate, "rev-parse", "--show-toplevel"]`,
		"await resolveRepoRoot(worktree || directory || process.cwd())",
		"isSyntheticMessage",
		"isUserRole",
		"handleUserPromptText",
		`disabled_reason: reason`,
		"const readStopMarker = async",
		"stopMarkerMatchesState",
		`await runCommand(["rm", "-f", stopFile])`,
	} {
		if !strings.Contains(a.Content, token) {
			t.Fatalf("OpenCode plugin missing %q:\n%s", token, a.Content)
		}
	}
}

func TestGenerateOpenCodePluginLeanContinuationPrompt(t *testing.T) {
	a, err := Generate(KindOpenCode)
	if err != nil {
		t.Fatalf("generate opencode: %v", err)
	}
	// The continuation prompt must keep the internal marker (so a re-submitted
	// autocontinue is recognized as runtime-internal and does not disable the
	// run) and carry the quality gate without re-injecting entry-only ceremony.
	for _, want := range []string{
		"degenmode autocontinue. Continue the repository task lifecycle",
		"Quality gate (mandatory before any Done)",
		"Never declare NO_SPEC_SURFACE without grepping docs/spec.md first",
		"Verify goal by goal, atomically - no sampling",
		"per-TASK Reality-Check loop in docs/task-loop-workflow.md",
	} {
		if !strings.Contains(a.Content, want) {
			t.Fatalf("OpenCode continuation prompt missing %q:\n%s", want, a.Content)
		}
	}
	for _, banned := range []string{
		"The second visible line must be a fresh",
		"Inspiration bank",
	} {
		if strings.Contains(a.Content, banned) {
			t.Fatalf("OpenCode continuation prompt must not re-inject entry ceremony %q:\n%s", banned, a.Content)
		}
	}
}

func TestGenerateOpenCodePluginDegenModeStateMachine(t *testing.T) {
	a, err := Generate(KindOpenCode)
	if err != nil {
		t.Fatalf("generate opencode: %v", err)
	}
	content := a.Content

	// readState heals inconsistent state when disabled_reason or a scoped stopfile
	// applies to the active run.
	if !strings.Contains(content, "(state.disabled_reason || stopApplies) && state.enabled") {
		t.Fatal("missing readState auto-heal for inconsistent enabled state")
	}
	if !strings.Contains(content, "state.enabled = false") {
		t.Fatal("missing readState enabled=false correction")
	}

	// enableDegenmode clears disabled_reason and stop_anchor_message_id
	if !strings.Contains(content, `disabled_reason: ""`) {
		t.Fatal("missing enableDegenmode disabled_reason clear")
	}
	if !strings.Contains(content, `stop_anchor_message_id: ""`) {
		t.Fatal("missing enableDegenmode stop_anchor_message_id clear")
	}

	// disableDegenmode resets active_run_id
	if !strings.Contains(content, `active_run_id: ""`) {
		t.Fatal("missing disableDegenmode active_run_id reset")
	}

	// chat.message is the authoritative OpenCode user-prompt switch:
	// standalone /degenmode flag enables; same-run normal prompts disable.
	if !strings.Contains(content, `"chat.message": async`) || !strings.Contains(content, "handleUserPromptText") {
		t.Fatal("missing chat.message prompt switch")
	}
	if !strings.Contains(content, `return token === "/degenmode"`) {
		t.Fatal("OpenCode activation must use standalone /degenmode flag")
	}
	if !strings.Contains(content, "sameActiveRun") {
		t.Fatal("OpenCode activation must preserve active run across other sessions")
	}
	if !strings.Contains(content, `disabledReason: "user_prompt"`) && !strings.Contains(content, `"user_prompt"`) {
		t.Fatal("missing user_prompt disable reason")
	}

	// maybeAutocontinue re-checks the scoped stop marker live.
	if !strings.Contains(content, "const stopMarker = await readStopMarker()") {
		t.Fatal("missing live stop marker re-check in maybeAutocontinue")
	}

	// Internal OpenCode errors must not be confused with user aborts. Only
	// explicit interrupt signals map to user_interrupt; other session errors
	// stop the driver as session_error.
	for _, forbidden := range []string{
		"abortTypePattern",
		"isUserAbortEvent",
		"disableOnUserAbort",
		"abortPayloadPattern",
		"safeJSON(event)",
		"user_abort_or_session_error",
		"degenmode user abort detected",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("BUG: %s still present — removed to prevent false aborts from internal OpenCode events", forbidden)
		}
	}

	for _, token := range []string{"tui.command.execute", "session.interrupt", "MessageAbortedError", "user_interrupt", "session_error", "setStopFile(eventSessionID"} {
		if !strings.Contains(content, token) {
			t.Fatalf("missing OpenCode interrupt/error handling token %q", token)
		}
	}
	if !strings.Contains(content, `"permission.ask": async`) || !strings.Contains(content, "permissionPayload") {
		t.Fatal("missing OpenCode permission.ask Reconc gate")
	}
	if !strings.Contains(content, "session.interrupted_by_user") {
		t.Fatal("missing explicit user interrupt stop handling")
	}

	// no_progress_guard stops at maxNoProgressNudges
	if !strings.Contains(content, "no_progress_guard") {
		t.Fatal("missing no_progress_guard disable reason")
	}
	// Other sessions must not disable an active run.
	if !strings.Contains(content, "state.active_run_id !== targetSessionID") {
		t.Fatal("missing other-session no-op guard")
	}
	if strings.Contains(content, "plugin_load") {
		t.Fatal("plugin reload must not disable degenmode")
	}
	if !strings.Contains(content, "opencode_continuation_driver") {
		t.Fatal("missing OpenCode continuation driver marker")
	}

	// nudgeInFlight prevents double-prompt
	if !strings.Contains(content, "nudgeInFlight") {
		t.Fatal("missing nudgeInFlight guard")
	}

	// isSyntheticMessage filters compaction messages
	if !strings.Contains(content, "isSyntheticMessage") {
		t.Fatal("missing isSyntheticMessage filter")
	}

	// isUserRole correctly identifies user/human roles
	if !strings.Contains(content, "isUserRole") {
		t.Fatal("missing isUserRole check")
	}
}

func TestInstallOpenCodeCreatesPlugin(t *testing.T) {
	repo := t.TempDir()
	report, err := Install(KindOpenCode, repo, false)
	if err != nil {
		t.Fatalf("install opencode: %v", err)
	}
	if report.Action != "created" {
		t.Fatalf("expected created, got %s", report.Action)
	}
	data, err := os.ReadFile(filepath.Join(repo, OpenCodePluginPath))
	if err != nil {
		t.Fatalf("read opencode plugin: %v", err)
	}
	if !strings.Contains(string(data), "Managed by reconc") || !strings.Contains(string(data), "opencode-session-start") {
		t.Fatalf("unexpected opencode plugin:\n%s", string(data))
	}
}

func TestInstallOpenCodeRefusesNonReconcExistingWithoutForce(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, OpenCodePluginPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("export const UserPlugin = async () => ({})\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(KindOpenCode, repo, false); err == nil {
		t.Fatal("expected non-reconc plugin overwrite refusal")
	}
	if _, err := Install(KindOpenCode, repo, true); err != nil {
		t.Fatalf("force install should overwrite: %v", err)
	}
}

func TestGenerateScaffoldArtifactMapsGitHookToSourceControlledPath(t *testing.T) {
	artifact, err := GenerateScaffoldArtifact(KindGitPreCommit)
	if err != nil {
		t.Fatalf("generate scaffold git hook: %v", err)
	}
	if artifact.TargetPath != GitPreCommitScaffoldPath {
		t.Fatalf("scaffold git target = %s, want %s", artifact.TargetPath, GitPreCommitScaffoldPath)
	}
	if !artifact.Executable {
		t.Fatal("scaffold git hook must remain executable")
	}
	if !strings.Contains(artifact.Content, "reconc ci") {
		t.Fatalf("scaffold git hook lost reconc ci content:\n%s", artifact.Content)
	}
}

func TestSyncRepoRootScaffoldWritesGeneratorArtifacts(t *testing.T) {
	scaffoldRoot := t.TempDir()
	report, err := SyncRepoRootScaffold(scaffoldRoot)
	if err != nil {
		t.Fatalf("sync scaffold: %v", err)
	}
	if len(report.Artifacts) != len(ScaffoldKinds()) {
		t.Fatalf("synced %d artifacts, want %d: %+v", len(report.Artifacts), len(ScaffoldKinds()), report.Artifacts)
	}
	for _, kind := range ScaffoldKinds() {
		artifact, err := GenerateScaffoldArtifact(kind)
		if err != nil {
			t.Fatalf("generate scaffold artifact %s: %v", kind, err)
		}
		target := filepath.Join(scaffoldRoot, filepath.FromSlash(artifact.TargetPath))
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("read synced %s at %s: %v", kind, artifact.TargetPath, err)
		}
		if string(data) != artifact.Content {
			t.Fatalf("synced %s differs from generator", kind)
		}
		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("stat synced %s: %v", kind, err)
		}
		if artifact.Executable && info.Mode()&0o111 == 0 {
			t.Fatalf("synced %s must be executable", kind)
		}
		if !artifact.Executable && info.Mode()&0o111 != 0 {
			t.Fatalf("synced %s must not be executable, mode=%v", kind, info.Mode())
		}
	}

	second, err := SyncRepoRootScaffold(scaffoldRoot)
	if err != nil {
		t.Fatalf("second sync scaffold: %v", err)
	}
	for _, artifact := range second.Artifacts {
		if artifact.Action != "unchanged" {
			t.Fatalf("second sync should be idempotent, got %+v", second.Artifacts)
		}
	}
}

func TestSyncRepoRootScaffoldRejectsMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "repo-root-scaffold")
	_, err := SyncRepoRootScaffold(missing)
	if err == nil || !strings.Contains(err.Error(), "scaffold path does not exist") {
		t.Fatalf("expected missing scaffold error, got %v", err)
	}
}

func TestTemplateRepoRootScaffoldHooksMatchGenerator(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	scaffoldRoot := filepath.Join(moduleRoot, "harness", "template", "repo-root-scaffold")
	if info, err := os.Stat(scaffoldRoot); err != nil || !info.IsDir() {
		t.Fatalf("template repo-root-scaffold missing at %s: %v", scaffoldRoot, err)
	}
	for _, kind := range ScaffoldKinds() {
		artifact, err := GenerateScaffoldArtifact(kind)
		if err != nil {
			t.Fatalf("generate scaffold artifact %s: %v", kind, err)
		}
		target := filepath.Join(scaffoldRoot, filepath.FromSlash(artifact.TargetPath))
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("template scaffold missing %s at %s: %v", kind, artifact.TargetPath, err)
		}
		if string(data) != artifact.Content {
			t.Fatalf("template scaffold %s at %s differs from generator; run `reconc hook sync-scaffold tools/reconc/harness/template/repo-root-scaffold`", kind, artifact.TargetPath)
		}
		if strings.Contains(artifact.Content, "Project Complete Candidate") {
			t.Fatalf("template scaffold %s still contains old final-hold wording", kind)
		}
		if kind == KindOpenCode && !strings.Contains(artifact.Content, "zero-finding Terminal Gate") {
			t.Fatalf("template scaffold %s must point Degenmode at the zero-finding Terminal Gate", kind)
		}
		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("stat template scaffold %s: %v", kind, err)
		}
		if artifact.Executable && info.Mode()&0o111 == 0 {
			t.Fatalf("template scaffold %s must be executable", kind)
		}
		if !artifact.Executable && info.Mode()&0o111 != 0 {
			t.Fatalf("template scaffold %s must not be executable, mode=%v", kind, info.Mode())
		}
	}
}

// --- merge validates array type -------------------------------------

func TestInstallSurfacesNonArrayHooksEvent(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// User accidentally made hooks.SessionStart an OBJECT not an array.
	pre := `{
  "hooks": {
    "SessionStart": { "note": "I did this wrong" }
  }
}`
	if err := os.WriteFile(filepath.Join(repo, ClaudeCodeSettingsPath), []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Install(KindClaudeCode, repo, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	// The user's malformed shape is overwritten, but it must be
	// reported via DroppedUserEdits so the CLI can warn.
	found := false
	for _, e := range report.DroppedUserEdits {
		if strings.Contains(e, "SessionStart") && strings.Contains(e, "object") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected non-array SessionStart to be reported, got: %v", report.DroppedUserEdits)
	}
}
