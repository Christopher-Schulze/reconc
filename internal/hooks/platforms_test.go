package hooks

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPlatformRegistryOwnsEveryHookKind(t *testing.T) {
	want := []string{
		KindGitPreCommit,
		KindClaudeCode,
		KindCodex,
		KindCursor,
		KindOpenCode,
		KindDevinCLI,
		KindAntigravity,
		KindCopilot,
		KindKilo,
	}
	if got := SupportedKinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SupportedKinds() = %v, want %v", got, want)
	}
	if got := InstallableKinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("InstallableKinds() = %v, want %v", got, want)
	}
	if got := ScaffoldKinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ScaffoldKinds() = %v, want %v", got, want)
	}
	if _, ok := RuntimeEvent("git-pre-commit"); ok {
		t.Fatal("git pre-commit must not expose a non-executable hook-runtime route")
	}

	platforms := Platforms()
	if len(platforms) != len(want) {
		t.Fatalf("Platforms() has %d entries, want %d", len(platforms), len(want))
	}
	platforms[0].Activation.ConfigDirs[0] = "mutated"
	platforms[1].Capabilities[0].RuntimeEvents[0] = "mutated"
	fresh := Platforms()
	if fresh[0].Activation.ConfigDirs[0] == "mutated" || fresh[1].Capabilities[0].RuntimeEvents[0] == "mutated" {
		t.Fatal("Platforms() exposed mutable registry slices")
	}
}

func TestPlatformRegistryCapabilitiesAreCompleteAndBounded(t *testing.T) {
	coreEvents := []Event{
		EventSessionStart,
		EventPreToolUse,
		EventPermissionRequest,
		EventPostToolUse,
		EventPostToolUseFailure,
		EventStop,
		EventSessionEnd,
		EventPostCompaction,
	}
	routes := map[string]string{}
	for _, platform := range AgentPlatforms() {
		byEvent := map[Event]Capability{}
		for _, capability := range platform.Capabilities {
			if _, duplicate := byEvent[capability.Event]; duplicate {
				t.Fatalf("%s duplicates capability %s", platform.Kind, capability.Event)
			}
			byEvent[capability.Event] = capability
			if capability.MaxOutputBytes <= 0 {
				t.Fatalf("%s %s has no output budget", platform.Kind, capability.Event)
			}
			if capability.Support != SupportUnsupported && capability.TimeoutSeconds <= 0 {
				t.Fatalf("%s %s has no timeout budget", platform.Kind, capability.Event)
			}
			for _, event := range append(append([]string{}, capability.RuntimeEvents...), capability.CompatibilityEvents...) {
				if owner, duplicate := routes[event]; duplicate {
					t.Fatalf("runtime route %s belongs to both %s and %s", event, owner, platform.Kind)
				}
				routes[event] = platform.Kind
				route, ok := RuntimeEvent(event)
				if !ok || route.PlatformKind != platform.Kind || route.Event != capability.Event {
					t.Fatalf("RuntimeEvent(%q) = %+v, %t", event, route, ok)
				}
			}
		}
		for _, event := range coreEvents {
			if _, ok := byEvent[event]; !ok {
				t.Fatalf("%s has no %s capability row", platform.Kind, event)
			}
		}
	}
}

func TestGenerateEveryRegisteredArtifact(t *testing.T) {
	for _, platform := range Platforms() {
		t.Run(platform.Kind, func(t *testing.T) {
			artifact, err := Generate(platform.Kind)
			if err != nil {
				t.Fatalf("Generate(%s): %v", platform.Kind, err)
			}
			if artifact.Kind != platform.Kind || artifact.TargetPath != platform.TargetPath || artifact.Executable != platform.Executable {
				t.Fatalf("artifact metadata drift: %+v platform=%+v", artifact, platform)
			}
			if requiresJSON(platform.InstallMode) && !json.Valid([]byte(artifact.Content)) {
				t.Fatalf("%s generated invalid JSON:\n%s", platform.Kind, artifact.Content)
			}
			for _, missing := range missingRuntimeEvents(platform, artifact.Content) {
				t.Errorf("generated artifact misses registry route %s", missing)
			}
			for _, removed := range []string{"UserPromptSubmit", "beforeSubmitPrompt", "chat.message", "-user-prompt-submit"} {
				if strings.Contains(artifact.Content, removed) {
					t.Errorf("generated artifact retained removed prompt route %q", removed)
				}
			}
		})
	}
}

func TestNewPlatformArtifactsUseCurrentContracts(t *testing.T) {
	claude, err := Generate(KindClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{`"matcher": "compact"`, "claude-post-compaction", `"timeout": 5`, `"timeout": 10`, `"timeout": 30`} {
		if !strings.Contains(claude.Content, token) {
			t.Fatalf("Claude artifact missing %q:\n%s", token, claude.Content)
		}
	}
	if strings.Contains(claude.Content, `"PostCompact"`) {
		t.Fatal("Claude PostCompact cannot inject recovery context; use SessionStart(compact) without an extra process")
	}

	devin, err := Generate(KindDevinCLI)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{`"PostCompaction"`, "devin-post-compaction", `"timeout": 5`, `"matcher": "^(exec|edit)$"`} {
		if !strings.Contains(devin.Content, token) {
			t.Fatalf("Devin artifact missing %q:\n%s", token, devin.Content)
		}
	}

	copilot, err := Generate(KindCopilot)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{`"version": 1`, `"SessionStart"`, `"PreToolUse"`, `"PermissionRequest"`, `"Stop"`, `"timeoutSec": 10`, `"powershell"`} {
		if !strings.Contains(copilot.Content, token) {
			t.Fatalf("Copilot artifact missing %q:\n%s", token, copilot.Content)
		}
	}
	if strings.Contains(copilot.Content, `"UserPromptSubmit"`) || strings.Contains(copilot.Content, "copilot-user-prompt-submit") {
		t.Fatal("Copilot artifact retained removed user-prompt route")
	}
	if strings.Contains(copilot.Content, `"PreCompact"`) || strings.Contains(copilot.Content, "copilot-post-compaction") {
		t.Fatal("Copilot preCompact is notification-only and must not spawn a no-op Reconc process")
	}

	kilo, err := Generate(KindKilo)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{`export default { id: "reconc", server: ReconcKiloServer }`, `"experimental.session.compacting"`, "kilo-pre-tool-use", `"timeoutMilliseconds":10000`, `maxBuffer: budget.maxOutputBytes`, `killSignal: "SIGKILL"`} {
		if !strings.Contains(kilo.Content, token) {
			t.Fatalf("Kilo artifact missing %q:\n%s", token, kilo.Content)
		}
	}
	if len(kilo.Content) > 12*1024 {
		t.Fatalf("Kilo adapter is not thin: %d bytes", len(kilo.Content))
	}

	antigravity, err := Generate(KindAntigravity)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(antigravity.Content, `"timeout": 120`) {
		t.Fatal("Antigravity retained the old 120-second blanket timeout")
	}
}

func TestBunAdapterRoutesAreRegistered(t *testing.T) {
	for kind, events := range map[string][]string{
		KindOpenCode: {
			"opencode-session-start", "opencode-pre-tool-use",
			"opencode-permission-request", "opencode-post-tool-use", "opencode-post-tool-use-failure",
			"opencode-post-compaction", "opencode-session-end", "opencode-stop",
		},
		KindKilo: {
			"kilo-session-start", "kilo-pre-tool-use",
			"kilo-permission-request", "kilo-post-tool-use", "kilo-post-tool-use-failure",
			"kilo-post-compaction", "kilo-session-end", "kilo-stop",
		},
	} {
		for _, event := range events {
			route, ok := RuntimeEvent(event)
			if !ok || route.PlatformKind != kind {
				t.Fatalf("adapter route %s = %+v, %t; want %s", event, route, ok, kind)
			}
		}
	}
	for _, event := range []string{
		"claude-user-prompt-submit",
		"codex-user-prompt-submit",
		"cursor-user-prompt-submit",
		"opencode-user-prompt-submit",
		"devin-user-prompt-submit",
		"antigravity-user-prompt-submit",
		"copilot-user-prompt-submit",
		"kilo-user-prompt-submit",
	} {
		if _, ok := RuntimeEvent(event); ok {
			t.Fatalf("removed user-prompt route %s is still registered", event)
		}
	}
}

func BenchmarkRuntimeEventLookup(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, ok := RuntimeEvent("antigravity-post-tool-use"); !ok {
			b.Fatal("route missing")
		}
	}
}

func BenchmarkGenerateAgentArtifacts(b *testing.B) {
	kinds := platformKinds(true)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, kind := range kinds {
			if _, err := Generate(kind); err != nil {
				b.Fatal(err)
			}
		}
	}
}
