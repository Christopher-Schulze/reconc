package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var generatedHookScaffoldFiles = []string{
	".codex/config.toml",
	".codex/hooks.json",
	".github/hooks/reconc.json",
	".cursor/hooks.json",
	".claude/settings.json",
	".devin/hooks.v1.json",
	".opencode/plugins/reconc.js",
	".agents/hooks.json",
	".kilo/plugin/reconc.js",
	".grok/hooks/reconc.json",
	".omp/extensions/reconc.ts",
	".pi/extensions/reconc.ts",
	".zcode/config.json",
}

func installGeneratedHookScaffold(t *testing.T, root string) {
	t.Helper()
	writeFile(t, root, stackConfigRel, `agent_hooks:
  require_codex_config: true
  require_codex_hook_file: true
  require_github_copilot_hooks: true
  require_cursor_hooks: true
  require_claude_settings: true
  require_opencode_plugin: true
  require_antigravity_hooks: true
  require_devin_hooks: true
  require_kilo_plugin: true
  require_grok_hooks: true
  require_omp_extension: true
  require_pi_extension: true
  require_zcode_hooks: true
`)
	for _, relative := range generatedHookScaffoldFiles {
		data, err := os.ReadFile(filepath.Join("..", "repo-root-scaffold", filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read generated scaffold %s: %v", relative, err)
		}
		writeFile(t, root, relative, string(data))
	}
}

func TestAuditAgentHooksAcceptsAllGeneratedPlatforms(t *testing.T) {
	root := t.TempDir()
	installGeneratedHookScaffold(t, root)

	if failures := auditAgentHooks(root); len(failures) > 0 {
		t.Fatalf("all generated hook platforms must pass:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditAgentHooksRejectsMissingGeneratedPlatformContracts(t *testing.T) {
	tests := []struct {
		name     string
		relative string
		token    string
	}{
		{name: "codex config", relative: ".codex/config.toml", token: "hooks = true"},
		{name: "codex hooks", relative: ".codex/hooks.json", token: "codex-subagent-stop"},
		{name: "GitHub Copilot hooks", relative: ".github/hooks/reconc.json", token: "copilot-subagent-stop"},
		{name: "cursor hooks", relative: ".cursor/hooks.json", token: "cursor-session-end"},
		{name: "claude hooks", relative: ".claude/settings.json", token: "claude-compaction-recovery"},
		{name: "devin hooks", relative: ".devin/hooks.v1.json", token: "devin-user-prompt-submit"},
		{name: "opencode plugin", relative: ".opencode/plugins/reconc.js", token: "opencode-pre-compaction"},
		{name: "antigravity hooks", relative: ".agents/hooks.json", token: "antigravity-post-invocation"},
		{name: "kilo plugin", relative: ".kilo/plugin/reconc.js", token: "kilo-user-prompt-submit"},
		{name: "grok hooks", relative: ".grok/hooks/reconc.json", token: "grok-notification"},
		{name: "OMP extension", relative: ".omp/extensions/reconc.ts", token: "omp-post-compaction"},
		{name: "Pi extension", relative: ".pi/extensions/reconc.ts", token: "pi-continuation-requested"},
		{name: "ZCode hooks", relative: ".zcode/config.json", token: "zcode-permission-request"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			installGeneratedHookScaffold(t, root)
			path := filepath.Join(root, filepath.FromSlash(test.relative))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			if !strings.Contains(content, test.token) {
				t.Fatalf("fixture %s does not contain %q", test.relative, test.token)
			}
			writeFile(t, root, test.relative, strings.ReplaceAll(content, test.token, "reconc-route-removed"))

			failures := auditAgentHooks(root)
			if !containsFailure(failures, "missing required Reconc hook token") {
				t.Fatalf("missing %s contract must fail the audit:\n%s", test.name, strings.Join(failures, "\n"))
			}
		})
	}
}

func TestAuditAgentHooksRejectsStaleTimeoutBudgets(t *testing.T) {
	for _, relative := range []string{
		".devin/hooks.v1.json",
		".agents/hooks.json",
		".grok/hooks/reconc.json",
	} {
		t.Run(relative, func(t *testing.T) {
			root := t.TempDir()
			installGeneratedHookScaffold(t, root)
			path := filepath.Join(root, filepath.FromSlash(relative))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			if !strings.Contains(content, `"timeout": 10`) {
				t.Fatalf("fixture %s has no 10-second guard budget", relative)
			}
			writeFile(t, root, relative, strings.Replace(content, `"timeout": 10`, `"timeout": 120`, 1))

			failures := auditAgentHooks(root)
			if !containsFailure(failures, "timeout for") {
				t.Fatalf("stale %s timeout must fail the audit:\n%s", relative, strings.Join(failures, "\n"))
			}
		})
	}
}

func TestAuditAgentHooksRejectsStaleGrokObservationTimeout(t *testing.T) {
	root := t.TempDir()
	installGeneratedHookScaffold(t, root)
	relative := ".grok/hooks/reconc.json"
	path := filepath.Join(root, filepath.FromSlash(relative))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"timeout": 5`) {
		t.Fatal("generated Grok artifact has no observation timeout")
	}
	writeFile(t, root, relative, strings.Replace(content, `"timeout": 5`, `"timeout": 120`, 1))
	if failures := auditAgentHooks(root); !containsFailure(failures, "timeout for") {
		t.Fatalf("stale Grok observation timeout must fail the audit:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditAgentHooksRejectsStaleGitHubCopilotTimeout(t *testing.T) {
	root := t.TempDir()
	installGeneratedHookScaffold(t, root)
	relative := ".github/hooks/reconc.json"
	path := filepath.Join(root, filepath.FromSlash(relative))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"timeoutSec": 10`) {
		t.Fatal("generated GitHub Copilot artifact has no guard timeout")
	}
	writeFile(t, root, relative, strings.Replace(content, `"timeoutSec": 10`, `"timeoutSec": 120`, 1))
	if failures := auditAgentHooks(root); !containsFailure(failures, "timeout for") {
		t.Fatalf("stale GitHub Copilot timeout must fail the audit:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditAgentHooksRejectsStaleOMPStopBudget(t *testing.T) {
	root := t.TempDir()
	installGeneratedHookScaffold(t, root)
	relative := ".omp/extensions/reconc.ts"
	path := filepath.Join(root, filepath.FromSlash(relative))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	const current = `"omp-stop":{"timeoutMilliseconds":29000`
	if !strings.Contains(content, current) {
		t.Fatal("generated OMP extension has no 29-second Stop budget")
	}
	writeFile(t, root, relative, strings.Replace(content, current, `"omp-stop":{"timeoutMilliseconds":120000`, 1))
	if failures := auditAgentHooks(root); !containsFailure(failures, "missing required Reconc hook token") {
		t.Fatalf("stale OMP Stop budget must fail the audit:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditAgentHooksRejectsStalePiStopBudget(t *testing.T) {
	root := t.TempDir()
	installGeneratedHookScaffold(t, root)
	relative := ".pi/extensions/reconc.ts"
	path := filepath.Join(root, filepath.FromSlash(relative))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	const current = `"pi-stop":{"timeoutMilliseconds":30000`
	if !strings.Contains(content, current) {
		t.Fatal("generated Pi extension has no 30-second settled budget")
	}
	writeFile(t, root, relative, strings.Replace(content, current, `"pi-stop":{"timeoutMilliseconds":120000`, 1))
	if failures := auditAgentHooks(root); !containsFailure(failures, "missing required Reconc hook token") {
		t.Fatalf("stale Pi Stop budget must fail the audit:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditAgentHooksRejectsZCodeContractDrift(t *testing.T) {
	root := t.TempDir()
	installGeneratedHookScaffold(t, root)
	relative := ".zcode/config.json"
	path := filepath.Join(root, filepath.FromSlash(relative))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"timeoutMs": 30000`) {
		t.Fatal("generated ZCode config has no 30-second Stop budget")
	}
	writeFile(t, root, relative, strings.Replace(content, `"timeoutMs": 30000`, `"timeoutMs": 120000`, 1))
	if failures := auditAgentHooks(root); !containsFailure(failures, "drifted process, args, matcher, or timeout contract") {
		t.Fatalf("ZCode contract drift must fail the audit:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditAgentHooksRejectsGitHubCopilotContractDrift(t *testing.T) {
	root := t.TempDir()
	installGeneratedHookScaffold(t, root)
	relative := ".github/hooks/reconc.json"
	path := filepath.Join(root, filepath.FromSlash(relative))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"cwd": "."`) {
		t.Fatal("generated GitHub Copilot artifact has no repository cwd")
	}
	writeFile(t, root, relative, strings.Replace(content, `"cwd": "."`, `"cwd": ".."`, 1))
	if failures := auditAgentHooks(root); !containsFailure(failures, "drifted command, cwd, matcher, or cross-platform route") {
		t.Fatalf("GitHub Copilot contract drift must fail the audit:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditAgentHooksRejectsProjectSpecificPluginState(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, stackConfigRel, `agent_hooks:
  require_codex_config: false
  require_codex_hook_file: false
  require_github_copilot_hooks: false
  require_cursor_hooks: false
  require_claude_settings: false
  require_antigravity_hooks: false
  require_devin_hooks: false
  require_kilo_plugin: false
  require_grok_hooks: false
  require_omp_extension: false
  require_pi_extension: false
  require_zcode_hooks: false
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
  require_github_copilot_hooks: false
  require_cursor_hooks: false
  require_claude_settings: false
  require_opencode_plugin: false
  require_antigravity_hooks: false
  require_devin_hooks: false
  require_kilo_plugin: true
  require_grok_hooks: false
  require_omp_extension: false
  require_pi_extension: false
  require_zcode_hooks: false
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
