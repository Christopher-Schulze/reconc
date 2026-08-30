package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateGrokOwnsNativeLifecycle(t *testing.T) {
	artifact, err := Generate(KindGrok)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Managed bool                                `json:"reconcManaged"`
		Hooks   map[string][]map[string]interface{} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(artifact.Content), &document); err != nil {
		t.Fatalf("decode Grok hook: %v", err)
	}
	if !document.Managed {
		t.Fatal("Grok hook is not marked reconc-managed")
	}
	for _, event := range []string{
		"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse",
		"PostToolUseFailure", "PermissionDenied", "Stop", "StopFailure",
		"Notification", "SubagentStart", "SubagentStop", "PreCompact",
		"PostCompact", "SessionEnd",
	} {
		if len(document.Hooks[event]) != 1 {
			t.Fatalf("Grok event %s has %d groups, want 1", event, len(document.Hooks[event]))
		}
	}
	if len(document.Hooks) != 14 {
		t.Fatalf("Grok hook has %d event types, want 14", len(document.Hooks))
	}
	if !strings.Contains(artifact.Content, `"matcher": "^(write|search_replace|hashline_edit|run_terminal_command|run_terminal_cmd)$"`) {
		t.Fatalf("Grok pre-tool matcher drifted:\n%s", artifact.Content)
	}
	for _, token := range []string{
		`"timeout": 600`,
		`{\"decision\":\"block\",\"reason\":\"Reconc could not evaluate this Grok Stop.`,
	} {
		if !strings.Contains(artifact.Content, token) {
			t.Fatalf("Grok native Stop contract missing %q:\n%s", token, artifact.Content)
		}
	}
}

func TestInstallGrokIsOwnedIdempotentAndPreservesOtherFiles(t *testing.T) {
	repo := t.TempDir()
	other := filepath.Join(repo, ".grok", "hooks", "team.json")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(`{"hooks":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Install(KindGrok, repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Action != "created" || report.TargetPath != GrokHooksPath ||
		!strings.Contains(report.NextAction, "/hooks-trust") {
		t.Fatalf("unexpected Grok install report: %+v", report)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("unrelated Grok hook was not preserved: %v", err)
	}
	report, err = Install(KindGrok, repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Action != "unchanged" {
		t.Fatalf("second Grok install = %s, want unchanged", report.Action)
	}
}

func TestInspectGrokRequiresGeneratorExactness(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindGrok, repo, false); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, filepath.FromSlash(GrokHooksPath))
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	reports, err := InspectPlatforms(repo)
	if err != nil {
		t.Fatal(err)
	}
	report := platformStatusForTest(t, reports, KindGrok)
	if report.State != StateDegraded || !strings.Contains(report.Detail, "differs from the current generator") {
		t.Fatalf("drifted Grok status = %+v", report)
	}
}

func TestInstallGrokRefusesUnmanagedTargetWithoutForce(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, filepath.FromSlash(GrokHooksPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"hooks":{"SessionStart":[]}}` + "\n")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(KindGrok, repo, false); err == nil {
		t.Fatal("unmanaged Grok target should require --force")
	}
	current, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(original) {
		t.Fatal("failed Grok install changed unmanaged content")
	}
	if _, err := Install(KindGrok, repo, true); err != nil {
		t.Fatalf("forced Grok install: %v", err)
	}
}

func TestInspectGrokRequiresWrapper(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindGrok, repo, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(WrapperPath))); err != nil {
		t.Fatal(err)
	}
	reports, err := InspectPlatforms(repo)
	if err != nil {
		t.Fatal(err)
	}
	report := platformStatusForTest(t, reports, KindGrok)
	if report.State != StateDegraded || !strings.Contains(report.Detail, WrapperPath) {
		t.Fatalf("Grok without wrapper = %+v", report)
	}
	wrapper := filepath.Join(repo, filepath.FromSlash(WrapperPath))
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte(GenerateWrapper().Content), 0o755); err != nil {
		t.Fatal(err)
	}
	reports, err = InspectPlatforms(repo)
	if err != nil {
		t.Fatal(err)
	}
	report = platformStatusForTest(t, reports, KindGrok)
	if report.State != StateConfigured {
		t.Fatalf("Grok with wrapper = %+v", report)
	}
}

func platformStatusForTest(t *testing.T, reports []PlatformStatus, kind string) PlatformStatus {
	t.Helper()
	for _, report := range reports {
		if report.Kind == kind {
			return report
		}
	}
	t.Fatalf("platform status missing for %s", kind)
	return PlatformStatus{}
}
