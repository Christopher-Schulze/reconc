package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHostSetting(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func useClaudeManagedDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous := claudeManagedSettingsDir
	claudeManagedSettingsDir = func() string { return dir }
	t.Cleanup(func() { claudeManagedSettingsDir = previous })
	return dir
}

func TestClaudeHooksDisableSwitchFollowsSettingsPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		managed  map[string]string
		local    string
		project  string
		user     string
		wantPath string
	}{
		{name: "nothing set"},
		{name: "user disables", user: `{"disableAllHooks":true}`, wantPath: "user"},
		{name: "project false overrides user true", project: `{"disableAllHooks":false}`, user: `{"disableAllHooks":true}`},
		{name: "local true overrides project false", local: `{"disableAllHooks":true}`, project: `{"disableAllHooks":false}`, wantPath: "local"},
		{name: "local false overrides project true", local: `{"disableAllHooks":false}`, project: `{"disableAllHooks":true}`},
		{name: "non-boolean value is skipped", local: `{"disableAllHooks":"yes"}`, project: `{"disableAllHooks":true}`, wantPath: "project"},
		{name: "invalid local file is skipped", local: `{`, user: `{"disableAllHooks":true}`, wantPath: "user"},
		{name: "managed disable wins", managed: map[string]string{"managed-settings.json": `{"disableAllHooks":true}`}, local: `{"disableAllHooks":false}`, wantPath: "managed-settings.json"},
		{name: "managed false overrides local true", managed: map[string]string{"managed-settings.json": `{"disableAllHooks":false}`}, local: `{"disableAllHooks":true}`},
		{name: "later drop-in overrides managed file", managed: map[string]string{"managed-settings.json": `{"disableAllHooks":true}`, "managed-settings.d/20-hooks.json": `{"disableAllHooks":false}`}},
		{name: "hidden and non-json drop-ins are ignored", managed: map[string]string{"managed-settings.d/.10.json": `{"disableAllHooks":true}`, "managed-settings.d/20.txt": `{"disableAllHooks":true}`}},
		{name: "managed hooks only blocks project hooks", managed: map[string]string{"managed-settings.d/10-lock.json": `{"allowManagedHooksOnly":true}`}, wantPath: "10-lock.json"},
		{name: "later drop-in releases hooks-only lock", managed: map[string]string{"managed-settings.json": `{"allowManagedHooksOnly":true}`, "managed-settings.d/20-hooks.json": `{"allowManagedHooksOnly":false}`}},
		{name: "later hooks-only lock wins", managed: map[string]string{"managed-settings.json": `{"allowManagedHooksOnly":false}`, "managed-settings.d/20-hooks.json": `{"allowManagedHooksOnly":true}`}, wantPath: "20-hooks.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			managed := useClaudeManagedDir(t)
			userDir := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", userDir)
			for name, content := range test.managed {
				writeHostSetting(t, filepath.Join(managed, filepath.FromSlash(name)), content)
			}
			for path, content := range map[string]string{
				filepath.Join(repo, ".claude", "settings.local.json"): test.local,
				filepath.Join(repo, ".claude", "settings.json"):       test.project,
				filepath.Join(userDir, "settings.json"):               test.user,
			} {
				if content != "" {
					writeHostSetting(t, path, content)
				}
			}
			got, err := claudeHooksDisableSwitch(repo)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantPath == "" {
				if got != nil {
					t.Fatalf("disable switch = %#v, want none", got)
				}
				return
			}
			want := map[string]string{
				"user":    filepath.Join(userDir, "settings.json"),
				"local":   filepath.Join(repo, ".claude", "settings.local.json"),
				"project": filepath.Join(repo, ".claude", "settings.json"),
			}[test.wantPath]
			if got == nil || (want != "" && got.path != want) || (want == "" && !strings.HasSuffix(got.path, test.wantPath)) {
				t.Fatalf("disable switch = %#v, want %s", got, test.wantPath)
			}
		})
	}
}

func TestCopilotHooksDisableSwitchFollowsSettingsPrecedence(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	writeHostSetting(t, filepath.Join(home, "settings.json"), `{"disableAllHooks":true}`)
	got, err := copilotHooksDisableSwitch(repo)
	if err != nil || got == nil || got.path != filepath.Join(home, "settings.json") {
		t.Fatalf("user disable = %#v, %v", got, err)
	}
	writeHostSetting(t, filepath.Join(repo, ".github", "copilot", "settings.json"), `{"disableAllHooks":false}`)
	if got, err := copilotHooksDisableSwitch(repo); err != nil || got != nil {
		t.Fatalf("repository false must override user true: %#v, %v", got, err)
	}
	writeHostSetting(t, filepath.Join(repo, ".github", "copilot", "settings.local.json"), `{"disableAllHooks":true}`)
	got, err = copilotHooksDisableSwitch(repo)
	if err != nil || got == nil || !strings.HasSuffix(got.path, "settings.local.json") {
		t.Fatalf("local true must override repository false: %#v, %v", got, err)
	}
}

func TestOMPExtensionDisableSwitchHonorsLayerReplacement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CONFIG_DIR", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("PI_PROFILE", "")
	t.Setenv("OMP_PROFILE", "")
	userConfig := filepath.Join(home, ".omp", "agent", "config.yml")

	repo := t.TempDir()
	if got, err := ompExtensionDisableSwitch(repo); err != nil || got != nil {
		t.Fatalf("no settings = %#v, %v", got, err)
	}
	writeHostSetting(t, userConfig, "disabledExtensions:\n  - extension-module:reconc\n")
	if got, err := ompExtensionDisableSwitch(repo); err != nil || got == nil || got.path != userConfig {
		t.Fatalf("user list = %#v, %v", got, err)
	}
	writeHostSetting(t, filepath.Join(repo, ".omp", "settings.json"), `{"disabledExtensions":["extension-module:other"]}`)
	if got, err := ompExtensionDisableSwitch(repo); err != nil || got != nil {
		t.Fatalf("a project list replaces the user list: %#v, %v", got, err)
	}
	writeHostSetting(t, filepath.Join(repo, ".omp", "config.yml"), "disabledExtensions: [extension-module:reconc]\n")
	if got, err := ompExtensionDisableSwitch(repo); err != nil || got == nil || !strings.HasSuffix(got.path, "config.yml") {
		t.Fatalf("project list naming Reconc = %#v, %v", got, err)
	}
	writeHostSetting(t, filepath.Join(repo, ".omp", "settings.json"), `{"disabledExtensions":["extension-module:reconc"]}`)
	writeHostSetting(t, filepath.Join(repo, ".omp", "config.yml"), "disabledExtensions: []\n")
	if got, err := ompExtensionDisableSwitch(repo); err != nil || got != nil {
		t.Fatalf("native empty list must override the legacy project and user lists: %#v, %v", got, err)
	}

	fallbackRepo := t.TempDir()
	if err := os.Remove(userConfig); err != nil {
		t.Fatal(err)
	}
	yamlFallback := filepath.Join(home, ".omp", "agent", "config.yaml")
	writeHostSetting(t, yamlFallback, "disabledExtensions:\n  - extension-module:reconc\n")
	if got, err := ompExtensionDisableSwitch(fallbackRepo); err != nil || got == nil || got.path != yamlFallback {
		t.Fatalf("config.yaml fallback = %#v, %v", got, err)
	}
	writeHostSetting(t, userConfig, "theme: dark\n")
	if got, err := ompExtensionDisableSwitch(fallbackRepo); err != nil || got != nil {
		t.Fatalf("config.yml shadows the config.yaml fallback: %#v, %v", got, err)
	}

	profileConfig := filepath.Join(home, ".omp", "profiles", "work", "agent", "config.yml")
	writeHostSetting(t, profileConfig, "disabledExtensions: [extension-module:reconc]\n")
	t.Setenv("OMP_PROFILE", "work")
	if got, err := ompExtensionDisableSwitch(fallbackRepo); err != nil || got == nil || got.path != profileConfig {
		t.Fatalf("named profile config = %#v, %v", got, err)
	}
	t.Setenv("OMP_PROFILE", "")
	t.Setenv("PI_PROFILE", "work")
	if got, err := ompExtensionDisableSwitch(fallbackRepo); err != nil || got != nil {
		t.Fatalf("an empty OMP_PROFILE selects the default profile over PI_PROFILE: %#v, %v", got, err)
	}
	agentDir := filepath.Join(fallbackRepo, "agent-override")
	writeHostSetting(t, filepath.Join(agentDir, "config.yml"), "disabledExtensions: [extension-module:reconc]\n")
	t.Setenv("PI_CODING_AGENT_DIR", "agent-override")
	if got, err := ompExtensionDisableSwitch(fallbackRepo); err != nil || got == nil || got.path != filepath.Join(agentDir, "config.yml") {
		t.Fatalf("relative PI_CODING_AGENT_DIR = %#v, %v", got, err)
	}
}

func TestHookStatusReportsHostDisableSwitch(t *testing.T) {
	useClaudeManagedDir(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	if _, err := Install(KindClaudeCode, repo, false); err != nil {
		t.Fatal(err)
	}
	report, err := InspectPlatform(repo, KindClaudeCode)
	if err != nil || report.State != StateConfigured {
		t.Fatalf("installed Claude hooks = %#v, %v", report, err)
	}
	writeHostSetting(t, filepath.Join(repo, ".claude", "settings.local.json"), `{"disableAllHooks":true}`)
	report, err = InspectPlatform(repo, KindClaudeCode)
	if err != nil || report.State != StateInstalled || !strings.Contains(report.Detail, "disableAllHooks=true") {
		t.Fatalf("disabled Claude hooks = %#v, %v", report, err)
	}
}

func TestClaudeConfigChangeBlockReason(t *testing.T) {
	useClaudeManagedDir(t)
	userDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", userDir)
	repo := t.TempDir()
	if _, err := Install(KindClaudeCode, repo, false); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"project_settings", "local_settings", "user_settings", "skills", "policy_settings"} {
		if reason, err := ClaudeConfigChangeBlockReason(repo, source); err != nil || reason != "" {
			t.Fatalf("%s on intact settings blocked: %q, %v", source, reason, err)
		}
	}
	writeHostSetting(t, filepath.Join(userDir, "settings.json"), `{"disableAllHooks":true}`)
	if reason, err := ClaudeConfigChangeBlockReason(repo, "user_settings"); err != nil || !strings.Contains(reason, "disableAllHooks=true") {
		t.Fatalf("user disableAllHooks was not blocked: %q, %v", reason, err)
	}
	if reason, err := ClaudeConfigChangeBlockReason(repo, "skills"); err != nil || reason != "" {
		t.Fatalf("a skills change cannot disable hooks: %q, %v", reason, err)
	}
	writeHostSetting(t, filepath.Join(userDir, "settings.json"), `{}`)
	settings := filepath.Join(repo, ".claude", "settings.json")
	writeHostSetting(t, settings, `{"hooks":{}}`)
	if reason, err := ClaudeConfigChangeBlockReason(repo, "project_settings"); err != nil || !strings.Contains(reason, "managed Claude Code hooks") {
		t.Fatalf("removing Reconc's project hooks was not blocked: %q, %v", reason, err)
	}
	if reason, err := ClaudeConfigChangeBlockReason(repo, "local_settings"); err != nil || reason != "" {
		t.Fatalf("a local change is judged only by disableAllHooks: %q, %v", reason, err)
	}
	// Only the settings decide: an outdated wrapper or an unresolvable binary
	// must not freeze settings edits, including the edit that restores the
	// managed entries.
	if _, err := Install(KindClaudeCode, repo, true); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(repo, filepath.FromSlash(WrapperPath))
	body, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, append(body, []byte("# outdated\n")...), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if reason, err := ClaudeConfigChangeBlockReason(repo, "project_settings"); err != nil || reason != "" {
		t.Fatalf("a settings change was blocked for a wrapper problem: %q, %v", reason, err)
	}
}
