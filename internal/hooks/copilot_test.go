package hooks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGenerateGitHubCopilotMatchesOfficialRepositoryHookShape(t *testing.T) {
	artifact, err := Generate(KindGitHubCopilot)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.TargetPath != ".github/hooks/reconc.json" || artifact.Executable {
		t.Fatalf("artifact metadata = %+v", artifact)
	}
	var document struct {
		Version int                                 `json:"version"`
		Hooks   map[string][]map[string]interface{} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(artifact.Content), &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != 1 {
		t.Fatalf("version = %d, want 1", document.Version)
	}
	wantEvents := []string{
		"Notification", "PermissionRequest", "PostToolUse", "PostToolUseFailure",
		"PreCompact", "PreToolUse", "SessionEnd", "SessionStart", "Stop",
		"SubagentStop", "UserPromptSubmit", "subagentStart",
	}
	if len(document.Hooks) != len(wantEvents) {
		t.Fatalf("events = %d, want %d: %#v", len(document.Hooks), len(wantEvents), document.Hooks)
	}
	for _, event := range wantEvents {
		entries := document.Hooks[event]
		if len(entries) != 1 {
			t.Fatalf("%s entries = %d, want 1", event, len(entries))
		}
		entry := entries[0]
		if entry["type"] != "command" || entry["cwd"] != "." || entry["bash"] == "" || entry["powershell"] == "" {
			t.Fatalf("%s command = %#v", event, entry)
		}
		if _, legacy := entry["timeout"]; legacy {
			t.Fatalf("%s uses legacy timeout alias: %#v", event, entry)
		}
		if _, ok := entry["timeoutSec"].(float64); !ok {
			t.Fatalf("%s has no numeric timeoutSec: %#v", event, entry)
		}
	}
	for _, unsupported := range []string{"PostCompact", "ErrorOccurred", "userPromptTransformed"} {
		if _, ok := document.Hooks[unsupported]; ok {
			t.Fatalf("unsupported event %s was generated", unsupported)
		}
	}
	if document.Hooks["PreToolUse"][0]["matcher"] != "Bash|Edit|Write" ||
		document.Hooks["PostToolUse"][0]["matcher"] != "Read|Bash|Edit|Write" {
		t.Fatalf("Claude-compatible matchers drifted: %#v", document.Hooks)
	}
	for _, event := range []string{"Stop", "SubagentStop"} {
		entry := document.Hooks[event][0]
		if !strings.Contains(entry["bash"].(string), `{"decision":"block"`) ||
			!strings.Contains(entry["powershell"].(string), `{"decision":"block"`) ||
			!strings.Contains(entry["powershell"].(string), "Get-Command sh -ErrorAction SilentlyContinue") {
			t.Fatalf("%s lacks missing-runtime block fallback: %#v", event, entry)
		}
	}
	// Copilot denials are exit 0 + JSON. PreToolUse / PermissionRequest must
	// emit deny envelopes when the wrapper/binary is missing; a bare non-zero
	// exit is host fail-open.
	pre := document.Hooks["PreToolUse"][0]
	if !strings.Contains(pre["bash"].(string), `"permissionDecision":"deny"`) ||
		!strings.Contains(pre["powershell"].(string), `"permissionDecision":"deny"`) ||
		!strings.Contains(pre["powershell"].(string), "Get-Command sh -ErrorAction SilentlyContinue") {
		t.Fatalf("PreToolUse lacks missing-runtime deny fallback: %#v", pre)
	}
	perm := document.Hooks["PermissionRequest"][0]
	if !strings.Contains(perm["bash"].(string), `"behavior":"deny"`) ||
		!strings.Contains(perm["powershell"].(string), `"behavior":"deny"`) ||
		!strings.Contains(perm["powershell"].(string), "Get-Command sh -ErrorAction SilentlyContinue") {
		t.Fatalf("PermissionRequest lacks missing-runtime deny fallback: %#v", perm)
	}
}

func TestInstallGitHubCopilotIsOwnedIdempotentAndPreservesSiblingHooks(t *testing.T) {
	repo := t.TempDir()
	sibling := filepath.Join(repo, ".github", "hooks", "team.json")
	if err := os.MkdirAll(filepath.Dir(sibling), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibling, []byte(`{"version":1,"hooks":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Install(KindGitHubCopilot, repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Action != "created" || report.TargetPath != GitHubCopilotHooksPath || !strings.Contains(report.NextAction, "cloud agent") {
		t.Fatalf("install report = %+v", report)
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("sibling hook changed: %v", err)
	}
	report, err = Install(KindGitHubCopilot, repo, false)
	if err != nil || report.Action != "unchanged" {
		t.Fatalf("second install = %+v, %v", report, err)
	}
}

func TestInstallGitHubCopilotNeverOverwritesForeignTarget(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, filepath.FromSlash(GitHubCopilotHooksPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"version":1,"hooks":{"PreToolUse":[{"type":"command","bash":"tools/reconc/bin/hook copilot-pre-tool-use .","powershell":"& sh tools/reconc/bin/hook copilot-pre-tool-use .","cwd":"."}],"Stop":[{"type":"command","bash":"tools/reconc/bin/hook copilot-stop .","powershell":"& sh tools/reconc/bin/hook copilot-stop .","cwd":"."}]}}` + "\n")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, force := range []bool{false, true} {
		if _, err := Install(KindGitHubCopilot, repo, force); err == nil || !strings.Contains(err.Error(), "user-owned") {
			t.Fatalf("force=%t error = %v", force, err)
		}
		current, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(current) != string(original) {
			t.Fatalf("force=%t changed foreign hook", force)
		}
	}
}

func TestInstallGitHubCopilotUpdatesRecognizedManagedRevision(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindGitHubCopilot, repo, false); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, filepath.FromSlash(GitHubCopilotHooksPath))
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	drifted := strings.Replace(string(data), `"timeoutSec": 10`, `"timeoutSec": 9`, 1)
	if drifted == string(data) {
		t.Fatal("fixture did not drift")
	}
	if err := os.WriteFile(target, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Install(KindGitHubCopilot, repo, false)
	if err != nil || report.Action != "updated" {
		t.Fatalf("managed update = %+v, %v", report, err)
	}
	artifact, err := Generate(KindGitHubCopilot)
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(target)
	if err != nil || string(current) != artifact.Content {
		t.Fatalf("managed target did not converge: %v", err)
	}
}

func TestInspectGitHubCopilotRequiresWrapper(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindGitHubCopilot, repo, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(WrapperPath))); err != nil {
		t.Fatal(err)
	}
	reports, err := InspectPlatforms(repo)
	if err != nil {
		t.Fatal(err)
	}
	report := platformStatusForTest(t, reports, KindGitHubCopilot)
	if report.State != StateDegraded || !strings.Contains(report.Detail, WrapperPath) {
		t.Fatalf("status without wrapper = %+v", report)
	}
}

func TestGitHubCopilotStopFallbackBlocksWhenBinaryIsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fallback execution is covered on POSIX hosts; PowerShell shape is contract-tested")
	}
	repo := filepath.Join(t.TempDir(), "repo with spaces")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(KindGitHubCopilot, repo, false); err != nil {
		t.Fatal(err)
	}
	artifact, err := Generate(KindGitHubCopilot)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Hooks map[string][]map[string]interface{} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(artifact.Content), &document); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", "-c", document.Hooks["Stop"][0]["bash"].(string))
	command.Dir = repo
	command.Env = []string{"PATH=/usr/bin:/bin"}
	command.Stdin = strings.NewReader(`{"hook_event_name":"Stop","session_id":"missing-binary","cwd":"` + repo + `"}`)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fallback command: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `{"decision":"block"`) || !strings.Contains(string(output), "could not evaluate") {
		t.Fatalf("missing binary did not produce explicit Stop block:\n%s", output)
	}
}

func TestGitHubCopilotPreToolUseFallbackDeniesWhenBinaryIsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fallback execution is covered on POSIX hosts; PowerShell shape is contract-tested")
	}
	repo := t.TempDir()
	if _, err := Install(KindGitHubCopilot, repo, false); err != nil {
		t.Fatal(err)
	}
	artifact, err := Generate(KindGitHubCopilot)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Hooks map[string][]map[string]interface{} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(artifact.Content), &document); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", "-c", document.Hooks["PreToolUse"][0]["bash"].(string))
	command.Dir = repo
	command.Env = []string{"PATH=/usr/bin:/bin"}
	command.Stdin = strings.NewReader(`{"hook_event_name":"PreToolUse","session_id":"missing-binary","tool_name":"Write","tool_input":{"file_path":"src/x.go"}}`)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fallback command: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `"permissionDecision":"deny"`) || !strings.Contains(string(output), "could not evaluate") {
		t.Fatalf("missing binary did not produce explicit PreToolUse deny:\n%s", output)
	}
}
