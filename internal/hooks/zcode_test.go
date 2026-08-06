package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGenerateZCodeMatchesRegistryContract(t *testing.T) {
	artifact, err := Generate(KindZCode)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Hooks struct {
			Enabled bool                                `json:"enabled"`
			Events  map[string][]map[string]interface{} `json:"events"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(artifact.Content), &document); err != nil {
		t.Fatal(err)
	}
	if !document.Hooks.Enabled || len(document.Hooks.Events) != 7 {
		t.Fatalf("ZCode hook tree = %#v", document.Hooks)
	}
	platform, ok := PlatformForKind(KindZCode)
	if !ok {
		t.Fatal("ZCode platform is not registered")
	}
	for _, capability := range platform.Capabilities {
		if capability.Support == SupportUnsupported || len(capability.Bindings) == 0 {
			continue
		}
		binding := capability.Bindings[0]
		route, registered := RuntimeEvent(binding.RuntimeEvent)
		if !registered || route.PlatformKind != KindZCode || route.Event != capability.Event {
			t.Fatalf("%s runtime route = %+v, registered=%t", binding.RuntimeEvent, route, registered)
		}
		groups := document.Hooks.Events[binding.NativeEvent]
		if len(groups) != 1 {
			t.Fatalf("%s groups = %d, want 1", binding.NativeEvent, len(groups))
		}
		if toolEvent := capability.Event == EventPreToolUse || capability.Event == EventPermissionRequest || capability.Event == EventPostToolUse || capability.Event == EventPostToolUseFailure; toolEvent {
			if groups[0]["matcher"] != "*" {
				t.Fatalf("%s matcher = %#v, want *", binding.NativeEvent, groups[0]["matcher"])
			}
		} else if _, exists := groups[0]["matcher"]; exists {
			t.Fatalf("%s has an unexpected matcher", binding.NativeEvent)
		}
		hooks, ok := groups[0]["hooks"].([]interface{})
		if !ok || len(hooks) != 1 {
			t.Fatalf("%s process hooks = %#v", binding.NativeEvent, groups[0]["hooks"])
		}
		process := hooks[0].(map[string]interface{})
		wantArgs := []interface{}{WrapperPath, binding.RuntimeEvent, "."}
		if process["type"] != "process" || process["command"] != "sh" || !reflect.DeepEqual(process["args"], wantArgs) {
			t.Fatalf("%s process = %#v", binding.NativeEvent, process)
		}
		if process["timeoutMs"] != float64(capability.TimeoutSeconds*1000) {
			t.Fatalf("%s timeoutMs = %#v, want %d", binding.NativeEvent, process["timeoutMs"], capability.TimeoutSeconds*1000)
		}
	}
	var stop *Capability
	for index := range platform.Capabilities {
		if platform.Capabilities[index].Event == EventStop {
			stop = &platform.Capabilities[index]
			break
		}
	}
	if stop == nil || stop.MaxContinuations != 3 || stop.ErrorPolicy != FailureBlock || stop.TimeoutPolicy != FailureAllow {
		t.Fatalf("ZCode Stop semantics = %+v", stop)
	}
}

func TestZCodeInstallPreservesForeignConfigurationAndIsIdempotent(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, filepath.FromSlash(ZCodeConfigPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := map[string]interface{}{
		"theme": "dark",
		"hooks": map[string]interface{}{
			"enabled":   false,
			"timeoutMs": float64(60000),
			"events": map[string]interface{}{
				"SessionStart": []interface{}{map[string]interface{}{
					"hooks": []interface{}{map[string]interface{}{"type": "process", "command": "echo", "args": []interface{}{"user"}}},
				}},
			},
		},
	}
	writeJSONForZCodeTest(t, target, foreign)
	report, err := Install(KindZCode, repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Action != "updated" || report.BackupPath != "" {
		t.Fatalf("install report = %+v", report)
	}
	installed := readJSONForZCodeTest(t, target)
	if installed["theme"] != "dark" {
		t.Fatalf("top-level user config was not preserved: %#v", installed)
	}
	hooks := installed["hooks"].(map[string]interface{})
	if hooks["enabled"] != true || hooks["timeoutMs"] != float64(60000) {
		t.Fatalf("ZCode hook settings were not preserved/enabled: %#v", hooks)
	}
	events := hooks["events"].(map[string]interface{})
	if len(events) != 7 || len(events["SessionStart"].([]interface{})) != 2 {
		t.Fatalf("merged ZCode events = %#v", events)
	}
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := Install(KindZCode, repo, false)
	if err != nil || repeated.Action != "unchanged" {
		t.Fatalf("idempotent install = %+v, %v", repeated, err)
	}
	after, err := os.ReadFile(target)
	if err != nil || string(after) != string(before) {
		t.Fatalf("idempotent bytes changed: %v", err)
	}
}

func TestZCodeInstallSplitsMixedGroupsWithoutDroppingForeignProcesses(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindZCode, repo, false); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, filepath.FromSlash(ZCodeConfigPath))
	document := readJSONForZCodeTest(t, target)
	events := document["hooks"].(map[string]interface{})["events"].(map[string]interface{})
	groups := events["PreToolUse"].([]interface{})
	managedGroup := groups[0].(map[string]interface{})
	managed := managedGroup["hooks"].([]interface{})
	foreign := map[string]interface{}{"type": "process", "command": "echo", "args": []interface{}{"user"}, "timeoutMs": float64(7000)}
	managedGroup["hooks"] = append([]interface{}{foreign}, managed...)
	writeJSONForZCodeTest(t, target, document)

	report, err := Install(KindZCode, repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Action != "updated" || len(report.DroppedUserEdits) != 1 || !strings.Contains(report.DroppedUserEdits[0], "zcode-pre-tool-use") {
		t.Fatalf("mixed-group install report = %+v", report)
	}
	installed := readJSONForZCodeTest(t, target)
	installedEvents := installed["hooks"].(map[string]interface{})["events"].(map[string]interface{})
	installedGroups := installedEvents["PreToolUse"].([]interface{})
	if len(installedGroups) != 2 {
		t.Fatalf("mixed group was not split into foreign and canonical groups: %#v", installedGroups)
	}
	foreignHooks := installedGroups[0].(map[string]interface{})["hooks"].([]interface{})
	canonicalHooks := installedGroups[1].(map[string]interface{})["hooks"].([]interface{})
	if len(foreignHooks) != 1 || foreignHooks[0].(map[string]interface{})["command"] != "echo" ||
		len(canonicalHooks) != 1 || canonicalHooks[0].(map[string]interface{})["command"] != "sh" {
		t.Fatalf("mixed-group process ownership was not preserved: %#v", installedGroups)
	}
}

func TestZCodeInstallFailsClosedOnInvalidHookShapeAndForceRepairsWithBackup(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, filepath.FromSlash(ZCodeConfigPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"theme":"dark","hooks":{"timeoutMs":60000,"events":[]}}` + "\n")
	if err := os.WriteFile(target, original, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(KindZCode, repo, false); err == nil || !strings.Contains(err.Error(), "hooks.events must be an object") {
		t.Fatalf("invalid shape error = %v", err)
	}
	unchanged, err := os.ReadFile(target)
	if err != nil || string(unchanged) != string(original) {
		t.Fatalf("refused install mutated config: %q, %v", unchanged, err)
	}
	report, err := Install(KindZCode, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.BackupPath == "" {
		t.Fatalf("forced repair omitted backup: %+v", report)
	}
	backup, err := os.ReadFile(report.BackupPath)
	if err != nil || string(backup) != string(original) {
		t.Fatalf("backup = %q, %v", backup, err)
	}
	repaired := readJSONForZCodeTest(t, target)
	if repaired["theme"] != "dark" {
		t.Fatalf("forced repair lost unrelated config: %#v", repaired)
	}
	hooks := repaired["hooks"].(map[string]interface{})
	if hooks["timeoutMs"] != float64(60000) || hooks["enabled"] != true {
		t.Fatalf("forced repair lost valid hook settings: %#v", hooks)
	}
}

func TestZCodeInstallRejectsInvalidEnabledTypeWithoutMutation(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, filepath.FromSlash(ZCodeConfigPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"hooks":{"enabled":"yes","events":{}}}` + "\n")
	if err := os.WriteFile(target, original, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(KindZCode, repo, false); err == nil || !strings.Contains(err.Error(), "hooks.enabled must be a boolean") {
		t.Fatalf("invalid enabled error = %v", err)
	}
	unchanged, err := os.ReadFile(target)
	if err != nil || string(unchanged) != string(original) {
		t.Fatalf("refused install mutated config: %q, %v", unchanged, err)
	}
}

func TestZCodeUninstallPreservesForeignHooksAndRefusesModifiedEntries(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindZCode, repo, false); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, filepath.FromSlash(ZCodeConfigPath))
	document := readJSONForZCodeTest(t, target)
	document["theme"] = "dark"
	hooks := document["hooks"].(map[string]interface{})
	hooks["timeoutMs"] = float64(60000)
	events := hooks["events"].(map[string]interface{})
	events["SessionStart"] = append(events["SessionStart"].([]interface{}), map[string]interface{}{
		"hooks": []interface{}{map[string]interface{}{"type": "process", "command": "echo", "args": []interface{}{"user"}}},
	})
	writeJSONForZCodeTest(t, target, document)
	report, err := Uninstall(KindZCode, repo)
	if err != nil {
		t.Fatal(err)
	}
	if report.Action != "updated" || report.RemovedEntries != 7 {
		t.Fatalf("uninstall report = %+v", report)
	}
	preserved, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(preserved), "echo") || strings.Contains(string(preserved), WrapperPath) {
		t.Fatalf("strict uninstall ownership failed: %s", preserved)
	}

	if _, err := Install(KindZCode, repo, false); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	modified := []byte(strings.Replace(string(installed), "zcode-pre-tool-use", "zcode-pre-tool-use-custom", 1))
	if err := os.WriteFile(target, modified, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(KindZCode, repo); err == nil || !strings.Contains(err.Error(), "modified Reconc entry") {
		t.Fatalf("modified uninstall error = %v", err)
	}
	current, err := os.ReadFile(target)
	if err != nil || string(current) != string(modified) {
		t.Fatalf("refused uninstall mutated config: %v", err)
	}
}

func TestZCodeUninstallRefusesMatcherOrTimeoutDrift(t *testing.T) {
	for _, mutation := range []struct {
		name   string
		mutate func(map[string]interface{})
	}{
		{name: "matcher", mutate: func(group map[string]interface{}) { group["matcher"] = "Write" }},
		{name: "timeout", mutate: func(group map[string]interface{}) {
			process := group["hooks"].([]interface{})[0].(map[string]interface{})
			process["timeoutMs"] = float64(9999)
		}},
		{name: "prepended foreign process", mutate: func(group map[string]interface{}) {
			managed := group["hooks"].([]interface{})
			foreign := map[string]interface{}{"type": "process", "command": "echo", "args": []interface{}{"user"}}
			group["hooks"] = append([]interface{}{foreign}, managed...)
		}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			repo := t.TempDir()
			if _, err := Install(KindZCode, repo, false); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(repo, filepath.FromSlash(ZCodeConfigPath))
			document := readJSONForZCodeTest(t, target)
			events := document["hooks"].(map[string]interface{})["events"].(map[string]interface{})
			group := events["PreToolUse"].([]interface{})[0].(map[string]interface{})
			mutation.mutate(group)
			writeJSONForZCodeTest(t, target, document)
			before, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Uninstall(KindZCode, repo); err == nil || !strings.Contains(err.Error(), "modified Reconc entry") {
				t.Fatalf("drifted uninstall error = %v", err)
			}
			after, err := os.ReadFile(target)
			if err != nil || string(after) != string(before) {
				t.Fatalf("refused uninstall mutated config: %v", err)
			}
		})
	}
}

func TestZCodeStatusRequiresEnabledConfiguration(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindZCode, repo, false); err != nil {
		t.Fatal(err)
	}
	if status := statusForKind(t, repo, KindZCode); status.State != StateConfigured {
		t.Fatalf("installed ZCode status = %+v", status)
	}
	target := filepath.Join(repo, filepath.FromSlash(ZCodeConfigPath))
	document := readJSONForZCodeTest(t, target)
	document["hooks"].(map[string]interface{})["enabled"] = false
	writeJSONForZCodeTest(t, target, document)
	if status := statusForKind(t, repo, KindZCode); status.State != StateDegraded || !strings.Contains(status.Detail, "hooks.enabled must be true") {
		t.Fatalf("disabled ZCode status = %+v", status)
	}
}

func readJSONForZCodeTest(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func writeJSONForZCodeTest(t *testing.T, path string, document map[string]interface{}) {
	t.Helper()
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o640); err != nil {
		t.Fatal(err)
	}
}
