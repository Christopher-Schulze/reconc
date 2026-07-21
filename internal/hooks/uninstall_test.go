package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallWrapperDependentHookCreatesWorkingWrapper(t *testing.T) {
	repo := t.TempDir()
	report, err := Install(KindClaudeCode, repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.WrapperPath != WrapperPath || report.WrapperAction != "created" || report.Partial {
		t.Fatalf("wrapper install report = %+v", report)
	}
	wrapper := filepath.Join(repo, filepath.FromSlash(WrapperPath))
	body, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != GenerateWrapper().Content || !executableFile(wrapper) {
		t.Fatal("installed wrapper is not generator-exact and executable")
	}
	status := statusForKind(t, repo, KindClaudeCode)
	if !status.Generated || !status.Installed || !status.Executable || !status.Configured {
		t.Fatalf("installed hook status = %+v", status)
	}
}

func TestUninstallNestedJSONPreservesUserHooksAndSharedWrapper(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, filepath.FromSlash(ClaudeCodeSettingsPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	userConfig := `{"theme":"dark","hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo user"}]}]}}` + "\n"
	if err := os.WriteFile(target, []byte(userConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(KindClaudeCode, repo, false); err != nil {
		t.Fatal(err)
	}
	report, err := Uninstall(KindClaudeCode, repo)
	if err != nil {
		t.Fatal(err)
	}
	if report.Action != "updated" || report.RemovedEntries == 0 || report.WrapperAction != "preserved-shared" {
		t.Fatalf("uninstall report = %+v", report)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	if document["theme"] != "dark" || !strings.Contains(string(body), "echo user") || strings.Contains(string(body), "tools/reconc/bin/hook") {
		t.Fatalf("user config was not preserved exactly by ownership: %s", body)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(WrapperPath))); err != nil {
		t.Fatalf("shared wrapper was removed: %v", err)
	}
}

func TestUninstallRefusesModifiedReconcJSONEntryWithoutMutation(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindClaudeCode, repo, false); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, filepath.FromSlash(ClaudeCodeSettingsPath))
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	modified := []byte(strings.Replace(string(body), "tools/reconc/bin/hook", "tools/reconc/bin/hook-custom", 1))
	if err := os.WriteFile(target, modified, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(KindClaudeCode, repo); err == nil || !strings.Contains(err.Error(), "modified Reconc entry") {
		t.Fatalf("modified hook uninstall error = %v", err)
	}
	current, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(modified) {
		t.Fatal("failed uninstall mutated modified user content")
	}
}

func TestUninstallExactPluginRemovesOnlyManagedArtifact(t *testing.T) {
	repo := t.TempDir()
	other := filepath.Join(repo, ".kilo", "plugin", "team.js")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("export default {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(KindKilo, repo, false); err != nil {
		t.Fatal(err)
	}
	report, err := Uninstall(KindKilo, repo)
	if err != nil {
		t.Fatal(err)
	}
	if report.Action != "removed" {
		t.Fatalf("plugin uninstall = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(KiloPluginPath))); !os.IsNotExist(err) {
		t.Fatalf("managed plugin still exists: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("unrelated plugin was removed: %v", err)
	}
}

func TestUninstallCodexRemovesOnlyManagedActivationBlock(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindCodex, repo, false); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(repo, ".codex", "config.toml")
	content := "model = \"gpt-test\"\n\n[features]\n" + CodexActivationBlockStart + "\nhooks = true\n" + CodexActivationBlockEnd + "\n"
	if err := os.WriteFile(config, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Uninstall(KindCodex, repo)
	if err != nil {
		t.Fatal(err)
	}
	if report.ActivationAction != "removed-managed-block" {
		t.Fatalf("Codex activation report = %+v", report)
	}
	body, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `model = "gpt-test"`) || strings.Contains(string(body), CodexActivationBlockStart) {
		t.Fatalf("Codex activation cleanup changed user config: %s", body)
	}
}

func TestInstallCodexRequiresForceForExplicitFalseAndUninstallRestoresIt(t *testing.T) {
	repo := t.TempDir()
	config := filepath.Join(repo, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "model = \"gpt-test\"\n\n[features]\n  hooks = false # user choice\nexperimental = true\n"
	if err := os.WriteFile(config, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}

	if _, err := Install(KindCodex, repo, false); err == nil || !strings.Contains(err.Error(), "rerun with --force") {
		t.Fatalf("explicit false install error = %v", err)
	}
	for _, relative := range []string{WrapperPath, CodexHooksPath} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative))); !os.IsNotExist(err) {
			t.Fatalf("failed activation preflight wrote %s: %v", relative, err)
		}
	}

	report, err := Install(KindCodex, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.ActivationPath != codexActivationPath || report.ActivationAction != "updated" || report.Partial {
		t.Fatalf("Codex force install report = %+v", report)
	}
	installed, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(installed), CodexActivationBlockStart) || strings.Contains(string(installed), "hooks = false") {
		t.Fatalf("Codex activation was not marker-owned: %s", installed)
	}
	status := statusForKind(t, repo, KindCodex)
	if !status.Configured || status.State != StateConfigured {
		t.Fatalf("Codex force install status = %+v", status)
	}

	if _, err := Uninstall(KindCodex, repo); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != original {
		t.Fatalf("Codex uninstall did not restore exact user line:\nwant %q\n got %q", original, restored)
	}
}

func TestCodexDisabledStatusProvidesWorkingForceRemediation(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindCodex, repo, false); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(repo, ".codex", "config.toml")
	if err := os.WriteFile(config, []byte("[features]\nhooks = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := statusForKind(t, repo, KindCodex)
	if status.State != StateInstalled || !strings.Contains(status.Remediation, "--force") {
		t.Fatalf("disabled Codex status lacks force remediation: %+v", status)
	}
	if _, err := Install(KindCodex, repo, true); err != nil {
		t.Fatalf("advertised force remediation failed: %v", err)
	}
	status = statusForKind(t, repo, KindCodex)
	if status.State != StateConfigured {
		t.Fatalf("force remediation did not configure Codex: %+v", status)
	}
}

func TestHookUninstallRollbackRefusesToOverwriteConcurrentChanges(t *testing.T) {
	for _, test := range []struct {
		name     string
		mutation uninstallMutation
	}{
		{name: "removed-path-reappeared", mutation: uninstallMutation{display: "owned.json", before: []byte("owned\n"), remove: true, mode: 0o644}},
		{name: "updated-path-changed", mutation: uninstallMutation{display: "managed.json", before: []byte("before\n"), after: []byte("after\n"), mode: 0o644}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.mutation.display)
			test.mutation.path = path
			if err := os.WriteFile(path, []byte("external\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := rollbackUninstallMutations([]uninstallMutation{test.mutation}); err == nil || !strings.Contains(err.Error(), "refuse to overwrite") {
				t.Fatalf("rollback error = %v", err)
			}
			body, err := os.ReadFile(path)
			if err != nil || string(body) != "external\n" {
				t.Fatalf("concurrent content changed: body=%q err=%v", body, err)
			}
		})
	}
}
