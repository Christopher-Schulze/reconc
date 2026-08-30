package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMergePreservesForeignCommandsInsideMixedGroups(t *testing.T) {
	canonical := reconcHookEntry("claude-stop").(map[string]interface{})
	canonicalHook := canonical["hooks"].([]interface{})[0]
	foreignBefore := map[string]interface{}{"type": "command", "command": "echo before"}
	foreignAfter := map[string]interface{}{"type": "command", "command": "echo after"}
	modified := map[string]interface{}{
		"type":    "command",
		"command": "${CLAUDE_PROJECT_DIR}/tools/reconc/bin/hook",
		"args":    []interface{}{"claude-stop-modified", "${CLAUDE_PROJECT_DIR}"},
	}
	mixed := map[string]interface{}{
		"matcher": "write|shell",
		"hooks":   []interface{}{foreignBefore, canonicalHook, modified, foreignAfter, canonicalHook},
	}
	destination := map[string]interface{}{"hooks": map[string]interface{}{"Stop": []interface{}{mixed}}}
	generated := map[string]interface{}{"hooks": map[string]interface{}{"Stop": []interface{}{canonical}}}

	diff := mergeReconcHooks(destination, generated, MergeOptions{})
	if len(diff.Removed) != 1 || !strings.Contains(diff.Removed[0], "claude-stop-modified") {
		t.Fatalf("dropped-edit report = %v", diff.Removed)
	}
	entries := destination["hooks"].(map[string]interface{})["Stop"].([]interface{})
	if len(entries) != 2 {
		t.Fatalf("merged entries = %#v", entries)
	}
	preserved := entries[0].(map[string]interface{})
	if preserved["matcher"] != "write|shell" || !reflect.DeepEqual(preserved["hooks"], []interface{}{foreignBefore, foreignAfter}) {
		t.Fatalf("foreign sibling order changed: %#v", preserved)
	}
	if !reflect.DeepEqual(entries[1], canonical) {
		t.Fatalf("canonical entry was not appended exactly once: %#v", entries)
	}
}

func TestMergeRequiresForceForIncompatibleShapes(t *testing.T) {
	foreign := map[string]interface{}{"owner": "user"}
	generated := map[string]interface{}{"hooks": map[string]interface{}{"Stop": []interface{}{reconcHookEntry("claude-stop")}}}
	destination := map[string]interface{}{"hooks": map[string]interface{}{"Stop": foreign, "Custom": "keep"}}

	diff := mergeReconcHooks(destination, generated, MergeOptions{})
	if len(diff.Blocked) != 1 || !strings.Contains(diff.Blocked[0], "non-array object preserved") {
		t.Fatalf("blocked report = %v", diff.Blocked)
	}
	if !reflect.DeepEqual(destination["hooks"].(map[string]interface{})["Stop"], foreign) {
		t.Fatal("blocked merge changed the foreign event value")
	}

	diff = mergeReconcHooks(destination, generated, MergeOptions{Force: true})
	if len(diff.Blocked) != 0 || len(diff.Removed) != 1 || !strings.Contains(diff.Removed[0], "non-array object overwritten") {
		t.Fatalf("forced report = %+v", diff)
	}
	hooks := destination["hooks"].(map[string]interface{})
	if hooks["Custom"] != "keep" || !reflect.DeepEqual(hooks["Stop"], generated["hooks"].(map[string]interface{})["Stop"]) {
		t.Fatalf("forced merge scope drift: %#v", hooks)
	}

	nullEvent := map[string]interface{}{"hooks": map[string]interface{}{"Stop": nil}}
	diff = mergeReconcHooks(nullEvent, generated, MergeOptions{})
	if len(diff.Blocked) != 1 || !strings.Contains(diff.Blocked[0], "non-array null preserved") {
		t.Fatalf("null event report = %+v", diff)
	}
}

func TestMergeRequiresForceForMalformedContainers(t *testing.T) {
	flatGenerated := map[string]interface{}{"hooks": map[string]interface{}{"Stop": []interface{}{reconcHookEntry("claude-stop")}}}
	flatDestination := map[string]interface{}{"hooks": "foreign"}
	if diff := mergeReconcHooks(flatDestination, flatGenerated, MergeOptions{}); len(diff.Blocked) != 1 || flatDestination["hooks"] != "foreign" {
		t.Fatalf("flat container merge = %+v, destination=%#v", diff, flatDestination)
	}

	nestedGenerated := map[string]interface{}{"hooks": map[string]interface{}{"events": map[string]interface{}{"Stop": []interface{}{reconcHookEntry("zcode-stop")}}}}
	nestedDestination := map[string]interface{}{"hooks": map[string]interface{}{"events": false, "enabled": false}}
	if diff := mergeReconcNestedEventHooks(nestedDestination, nestedGenerated, MergeOptions{}); len(diff.Blocked) != 1 || nestedDestination["hooks"].(map[string]interface{})["events"] != false {
		t.Fatalf("nested container merge = %+v, destination=%#v", diff, nestedDestination)
	}
}

func TestExecOwnershipAndNULSignaturesUseParsedArguments(t *testing.T) {
	for _, test := range []struct {
		name      string
		command   string
		arguments []interface{}
		owned     bool
	}{
		{name: "bare reconc exec", command: "reconc", arguments: []interface{}{"hook", "runtime", "antigravity-stop", "."}, owned: true},
		{name: "wrapper exec", command: WrapperPath, arguments: []interface{}{"antigravity-stop", "."}, owned: true},
		{name: "interpreter exec", command: "sh", arguments: []interface{}{WrapperPath, "zcode-stop", "."}, owned: true},
		{name: "argument mention", command: "echo", arguments: []interface{}{WrapperPath, "antigravity-stop"}},
		{name: "NUL in command", command: "echo\x00" + WrapperPath, arguments: []interface{}{"antigravity-stop"}},
		{name: "NUL in argument", command: "reconc", arguments: []interface{}{"hook\x00runtime", "runtime", "antigravity-stop", "."}},
	} {
		t.Run(test.name, func(t *testing.T) {
			signature := commandSignature(test.command, test.arguments)
			if strings.ContainsRune(test.command, '\x00') || slices.ContainsFunc(test.arguments, func(value interface{}) bool {
				text, _ := value.(string)
				return strings.ContainsRune(text, '\x00')
			}) {
				if signature != "" {
					t.Fatalf("ambiguous signature = %q", signature)
				}
			}
			if got := reconcCommandOwned(signature); got != test.owned {
				t.Fatalf("ownership = %v, want %v for %q", got, test.owned, signature)
			}
		})
	}
}

func TestInstallRejectsMalformedEventShapeUntilForced(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, filepath.FromSlash(ClaudeCodeSettingsPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"hooks":{"Stop":{"command":"foreign"},"Custom":"keep"},"setting":true}`)
	if err := os.WriteFile(target, original, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(KindClaudeCode, repo, false); err == nil || !strings.Contains(err.Error(), "pass --force") {
		t.Fatalf("malformed event shape was not blocked: %v", err)
	}
	if body, err := os.ReadFile(target); err != nil || !reflect.DeepEqual(body, original) {
		t.Fatalf("blocked install changed target: %q, %v", body, err)
	}
	report, err := Install(KindClaudeCode, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.DroppedUserEdits) != 1 || !strings.Contains(report.DroppedUserEdits[0], "Stop: (non-array object overwritten)") {
		t.Fatalf("forced dropped-edit report = %v", report.DroppedUserEdits)
	}
	var merged map[string]interface{}
	readJSONFile(t, target, &merged)
	hooks := merged["hooks"].(map[string]interface{})
	if hooks["Custom"] != "keep" || merged["setting"] != true {
		t.Fatalf("forced install lost foreign state: %#v", merged)
	}
}

func TestInstallAntigravityPreservesMixedNamespaceEntries(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, filepath.FromSlash(AntigravityHooksPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := map[string]interface{}{
		"top-level": "keep",
		"reconc": map[string]interface{}{
			"PreInvocation": []interface{}{
				map[string]interface{}{"type": "command", "command": "echo before"},
				map[string]interface{}{"type": "command", "command": "reconc", "args": []interface{}{"hook", "runtime", "antigravity-pre-invocation", "."}},
				map[string]interface{}{"type": "command", "command": "echo after"},
			},
			"Custom":   []interface{}{map[string]interface{}{"type": "command", "command": "./custom.sh"}},
			"metadata": map[string]interface{}{"owner": "user"},
		},
	}
	writeJSONFile(t, target, existing)
	report, err := Install(KindAntigravity, repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.DroppedUserEdits) != 1 || !strings.Contains(report.DroppedUserEdits[0], "reconc hook runtime antigravity-pre-invocation") {
		t.Fatalf("Antigravity dropped-edit report = %v", report.DroppedUserEdits)
	}
	var merged map[string]interface{}
	readJSONFile(t, target, &merged)
	if merged["top-level"] != "keep" {
		t.Fatalf("top-level owner lost: %#v", merged)
	}
	namespace := merged["reconc"].(map[string]interface{})
	if !reflect.DeepEqual(namespace["metadata"], map[string]interface{}{"owner": "user"}) || namespace["Custom"] == nil {
		t.Fatalf("foreign namespace entries lost: %#v", namespace)
	}
	entries := namespace["PreInvocation"].([]interface{})
	commands := make([]string, 0, len(entries))
	for _, raw := range entries {
		entry := raw.(map[string]interface{})
		command, _ := entry["command"].(string)
		commands = append(commands, command)
	}
	if len(commands) != 3 || commands[0] != "echo before" || commands[1] != "echo after" || !strings.Contains(commands[2], "antigravity-pre-invocation") {
		t.Fatalf("Antigravity command order = %v", commands)
	}
}

func TestInstallAntigravityNamespaceCollisionRequiresForce(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, filepath.FromSlash(AntigravityHooksPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"reconc":"foreign","top-level":"keep"}`)
	if err := os.WriteFile(target, original, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(KindAntigravity, repo, false); err == nil || !strings.Contains(err.Error(), "pass --force") {
		t.Fatalf("namespace collision was not blocked: %v", err)
	}
	if body, err := os.ReadFile(target); err != nil || !reflect.DeepEqual(body, original) {
		t.Fatalf("blocked collision changed target: %q, %v", body, err)
	}
	report, err := Install(KindAntigravity, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.DroppedUserEdits) != 1 || report.DroppedUserEdits[0] != "reconc: (non-object string overwritten)" {
		t.Fatalf("namespace collision report = %v", report.DroppedUserEdits)
	}
	var merged map[string]interface{}
	readJSONFile(t, target, &merged)
	if merged["top-level"] != "keep" {
		t.Fatalf("forced collision repair lost foreign top-level state: %#v", merged)
	}
	if _, ok := merged["reconc"].(map[string]interface{}); !ok {
		t.Fatalf("forced collision repair did not install namespace: %#v", merged)
	}
}

func TestExistingEmptyHookFilesReportUpdated(t *testing.T) {
	for _, kind := range []string{KindClaudeCode, KindCodex, KindCursor, KindDevinCLI, KindAntigravity, KindZCode} {
		for _, content := range []string{"", "{}"} {
			t.Run(kind+"/"+content, func(t *testing.T) {
				repo := t.TempDir()
				artifact, err := Generate(kind)
				if err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(repo, filepath.FromSlash(artifact.TargetPath))
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte(content), 0o640); err != nil {
					t.Fatal(err)
				}
				report, err := Install(kind, repo, false)
				if err != nil {
					t.Fatal(err)
				}
				if report.Action != "updated" {
					t.Fatalf("action = %q, want updated", report.Action)
				}
			})
		}
	}
}

func writeJSONFile(t *testing.T, path string, value interface{}) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o640); err != nil {
		t.Fatal(err)
	}
}

func readJSONFile(t *testing.T, path string, destination interface{}) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, destination); err != nil {
		t.Fatal(err)
	}
}
