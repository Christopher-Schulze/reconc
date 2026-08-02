package hooks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEveryInstallablePlatformHasAnExactRoundTrip(t *testing.T) {
	for _, kind := range InstallableKinds() {
		t.Run(kind, func(t *testing.T) {
			repo := t.TempDir()
			if kind == KindKimiCode {
				enableKimiCodeCLIForTest(t)
				t.Setenv("KIMI_CODE_HOME", t.TempDir())
			}
			command := exec.Command("git", "-C", repo, "init", "--quiet")
			if body, err := command.CombinedOutput(); err != nil {
				t.Fatalf("git init: %v\n%s", err, body)
			}
			installed, err := Install(kind, repo, false)
			if err != nil {
				t.Fatalf("Install(%q): %v", kind, err)
			}
			if installed.Kind != kind || installed.Action != "created" || installed.TargetPath == "" {
				t.Fatalf("install report = %+v", installed)
			}
			status := statusForKind(t, repo, kind)
			if !status.Installed || !status.Generated {
				t.Fatalf("installed platform status = %+v", status)
			}
			repeated, err := Install(kind, repo, false)
			if err != nil || repeated.Action != "unchanged" {
				t.Fatalf("idempotent Install(%q) = %+v, %v", kind, repeated, err)
			}
			removed, err := Uninstall(kind, repo)
			if err != nil {
				t.Fatalf("Uninstall(%q): %v", kind, err)
			}
			if removed.Kind != kind || (removed.Action != "removed" && removed.Action != "updated") || removed.RemovedEntries == 0 {
				t.Fatalf("uninstall report = %+v", removed)
			}
			absent, err := Uninstall(kind, repo)
			if err != nil || (absent.Action != "absent" && absent.Action != "unchanged") || absent.RemovedEntries != 0 {
				t.Fatalf("idempotent Uninstall(%q) = %+v, %v", kind, absent, err)
			}
		})
	}
}

func TestDevinInstallerPreservesUserHooksAndBacksUpMalformedState(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, filepath.FromSlash(DevinHooksPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	user := map[string]interface{}{
		"custom": []interface{}{map[string]interface{}{"type": "command", "command": "echo user"}},
	}
	body, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, body, 0o640); err != nil {
		t.Fatal(err)
	}
	report, err := Install(KindDevinCLI, repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Action != "updated" {
		t.Fatalf("merge report = %+v", report)
	}
	merged, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(merged), "echo user") {
		t.Fatalf("user hook was not preserved: %s, %v", merged, err)
	}

	if err := os.WriteFile(target, []byte("{"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(KindDevinCLI, repo, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("malformed Devin config error = %v", err)
	}
	report, err = Install(KindDevinCLI, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.BackupPath == "" {
		t.Fatalf("forced repair omitted backup: %+v", report)
	}
	if backup, err := os.ReadFile(report.BackupPath); err != nil || string(backup) != "{" {
		t.Fatalf("backup = %q, %v", backup, err)
	}
}

func TestManagedPlatformFilesRefuseForeignOwnership(t *testing.T) {
	tests := []struct {
		kind        string
		path        string
		forceWorks  bool
		wantManaged string
	}{
		{kind: KindKilo, path: KiloPluginPath, forceWorks: true, wantManaged: "kilo-pre-tool-use"},
		{kind: KindGrok, path: GrokHooksPath, forceWorks: true, wantManaged: "grok-pre-tool-use"},
		{kind: KindOMP, path: OMPExtensionPath, forceWorks: false, wantManaged: "omp-pre-tool-use"},
		{kind: KindGitHubCopilot, path: GitHubCopilotHooksPath, forceWorks: false, wantManaged: "copilot-pre-tool-use"},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			repo := t.TempDir()
			target := filepath.Join(repo, filepath.FromSlash(test.path))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("foreign"), 0o640); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(test.kind, repo, false); err == nil || !strings.Contains(err.Error(), "not reconc-managed") {
				t.Fatalf("foreign ownership error = %v", err)
			}
			report, err := Install(test.kind, repo, true)
			if !test.forceWorks {
				if err == nil || !strings.Contains(err.Error(), "user-owned") {
					t.Fatalf("force accepted user-owned %s config: %+v, %v", test.kind, report, err)
				}
				body, readErr := os.ReadFile(target)
				if readErr != nil || string(body) != "foreign" {
					t.Fatalf("refused force mutated foreign file: %q, %v", body, readErr)
				}
				return
			}
			if err != nil || report.Action != "updated" {
				t.Fatalf("forced managed-file repair = %+v, %v", report, err)
			}
			body, err := os.ReadFile(target)
			if err != nil || !strings.Contains(string(body), test.wantManaged) {
				t.Fatalf("repaired artifact = %q, %v", body, err)
			}
		})
	}
}

func TestRuntimeRegistryViewsAreCompleteStableAndIsolated(t *testing.T) {
	events := RuntimeEvents()
	if len(events) == 0 {
		t.Fatal("runtime registry is empty")
	}
	seen := map[string]bool{}
	for _, event := range events {
		if seen[event] {
			t.Fatalf("duplicate runtime event %q", event)
		}
		seen[event] = true
		route, ok := RuntimeEvent(event)
		if !ok || route.PlatformKind == "" || route.Event == "" || route.TimeoutSeconds <= 0 || route.MaxOutputBytes <= 0 {
			t.Fatalf("runtime route %q = %+v, %t", event, route, ok)
		}
	}
	if _, ok := RuntimeEvent("unknown"); ok {
		t.Fatal("unknown runtime event resolved")
	}

	platforms := Platforms()
	copy := Platforms()
	if !reflect.DeepEqual(platforms, copy) {
		t.Fatal("platform registry clone is not deterministic")
	}
	for _, platform := range platforms {
		for _, capability := range platform.Capabilities {
			native := capability.PrimaryNativeEvent()
			hasPrimary := false
			for _, binding := range capability.Bindings {
				if !binding.Compatibility {
					hasPrimary = true
					if native != binding.NativeEvent {
						t.Fatalf("%s/%s primary event = %q, want %q", platform.Kind, capability.Event, native, binding.NativeEvent)
					}
					break
				}
			}
			if !hasPrimary && native != "" {
				t.Fatalf("%s/%s has unexpected primary event %q", platform.Kind, capability.Event, native)
			}
		}
	}
	if len(platforms) > 0 && len(platforms[0].Capabilities) > 0 {
		platforms[0].Capabilities[0].Bindings = nil
		if reflect.DeepEqual(platforms, Platforms()) {
			t.Fatal("caller mutation changed the platform registry")
		}
	}
}
