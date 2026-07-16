package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditAgentHooksAcceptsGeneratedExtendedPlatforms(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, stackConfigRel, `agent_hooks:
  require_codex_config: false
  require_codex_hook_file: false
  require_cursor_hooks: false
  require_claude_settings: true
  require_opencode_plugin: false
  require_antigravity_hooks: false
  require_devin_hooks: true
  require_kilo_plugin: true
  require_grok_hooks: true
`)
	for _, relative := range []string{
		".claude/settings.json",
		".devin/hooks.v1.json",
		".kilo/plugin/reconc.js",
		".grok/hooks/reconc.json",
	} {
		data, err := os.ReadFile(filepath.Join("..", "repo-root-scaffold", filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read generated scaffold %s: %v", relative, err)
		}
		writeFile(t, root, relative, string(data))
	}

	if failures := auditAgentHooks(root); len(failures) > 0 {
		t.Fatalf("generated extended hook platforms must pass:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditAgentHooksRejectsProjectSpecificPluginState(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, stackConfigRel, `agent_hooks:
  require_codex_config: false
  require_codex_hook_file: false
  require_cursor_hooks: false
  require_claude_settings: false
  require_antigravity_hooks: false
  require_devin_hooks: false
  require_kilo_plugin: false
  require_grok_hooks: false
  require_opencode_plugin: true
`)
	data, err := os.ReadFile(filepath.Join("..", "repo-root-scaffold", ".opencode", "plugins", "reconc.js"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, ".opencode/plugins/reconc.js", string(data)+"\n// .reconc/runloop\n")

	failures := auditAgentHooks(root)
	if !containsFailure(failures, "forbidden Reconc hook token") {
		t.Fatalf("project-specific plugin state must fail the audit:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditAgentHooksRejectsVersionPinnedPluginBinary(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, stackConfigRel, `agent_hooks:
  require_codex_config: false
  require_codex_hook_file: false
  require_cursor_hooks: false
  require_claude_settings: false
  require_opencode_plugin: false
  require_antigravity_hooks: false
  require_devin_hooks: false
  require_kilo_plugin: true
  require_grok_hooks: false
`)
	data, err := os.ReadFile(filepath.Join("..", "repo-root-scaffold", ".kilo", "plugin", "reconc.js"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, ".kilo/plugin/reconc.js", string(data)+"\n// reconc-0.6.0-darwin-arm64\n")

	failures := auditAgentHooks(root)
	if !containsFailure(failures, "forbidden Reconc hook token") {
		t.Fatalf("version-pinned plugin binary must fail the audit:\n%s", strings.Join(failures, "\n"))
	}
}
