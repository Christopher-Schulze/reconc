package hooks

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	rerrors "reconc.dev/reconc/internal/errors"
)

func TestPlatformRegistryOwnsEveryHookKind(t *testing.T) {
	want := []string{
		KindGitPreCommit,
		KindClaudeCode,
		KindCodex,
		KindGitHubCopilot,
		KindCursor,
		KindOpenCode,
		KindDevinCLI,
		KindAntigravity,
		KindKilo,
		KindGrok,
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
	platforms[1].Capabilities[0].Bindings[0].RuntimeEvent = "mutated"
	fresh := Platforms()
	if fresh[0].Activation.ConfigDirs[0] == "mutated" || fresh[1].Capabilities[0].Bindings[0].RuntimeEvent == "mutated" {
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
		if err := validatePlatform(platform); err != nil {
			t.Fatalf("validatePlatform(%s): %v", platform.Kind, err)
		}
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
			for _, binding := range capability.Bindings {
				if binding.RuntimeEvent == "" {
					continue
				}
				if owner, duplicate := routes[binding.RuntimeEvent]; duplicate {
					t.Fatalf("runtime route %s belongs to both %s and %s", binding.RuntimeEvent, owner, platform.Kind)
				}
				routes[binding.RuntimeEvent] = platform.Kind
				route, ok := RuntimeEvent(binding.RuntimeEvent)
				if !ok || route.PlatformKind != platform.Kind || route.Event != capability.Event {
					t.Fatalf("RuntimeEvent(%q) = %+v, %t", binding.RuntimeEvent, route, ok)
				}
			}
		}
		for _, event := range coreEvents {
			capability, ok := byEvent[event]
			if !ok {
				t.Fatalf("%s has no %s capability row", platform.Kind, event)
			}
			if event == EventSessionStart && capability.Support != SupportUnsupported && (capability.ErrorPolicy != FailureAllow || capability.TimeoutPolicy != FailureAllow) {
				t.Fatalf("%s SessionStart can wedge the host: %+v", platform.Kind, capability)
			}
		}
	}
}

func TestCursorBindingsDriveGeneratedContract(t *testing.T) {
	platform, ok := PlatformForKind(KindCursor)
	if !ok {
		t.Fatal("Cursor platform missing")
	}
	artifact, err := Generate(KindCursor)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Hooks map[string][]map[string]interface{} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(artifact.Content), &document); err != nil {
		t.Fatal(err)
	}
	generatedRoutes := map[string]struct{}{}
	for _, capability := range platform.Capabilities {
		for _, binding := range capability.Bindings {
			if binding.Compatibility || binding.RuntimeEvent == "" || capability.Support == SupportUnsupported {
				continue
			}
			entries := document.Hooks[binding.NativeEvent]
			if len(entries) != 1 {
				t.Fatalf("%s entry count = %d", binding.NativeEvent, len(entries))
			}
			entry := entries[0]
			command, _ := entry["command"].(string)
			if !strings.Contains(command, binding.RuntimeEvent) {
				t.Fatalf("%s command misses %s: %#v", binding.NativeEvent, binding.RuntimeEvent, entry)
			}
			if entry["timeout"] != float64(capability.TimeoutSeconds) {
				t.Fatalf("%s timeout = %#v, want %d", binding.NativeEvent, entry["timeout"], capability.TimeoutSeconds)
			}
			if entry["matcher"] != nil && entry["matcher"] != binding.Matcher {
				t.Fatalf("%s matcher = %#v, want %q", binding.NativeEvent, entry["matcher"], binding.Matcher)
			}
			if entry["loop_limit"] != nil && entry["loop_limit"] != float64(binding.LoopLimit) {
				t.Fatalf("%s loop limit = %#v, want %d", binding.NativeEvent, entry["loop_limit"], binding.LoopLimit)
			}
			wantFailClosed := (binding.ResponseMode == CursorResponseDecision || binding.ResponseMode == CursorResponseStopFollowup) &&
				capability.ErrorPolicy == FailureBlock && capability.TimeoutPolicy == FailureBlock
			if entry["failClosed"] != wantFailClosed {
				t.Fatalf("%s failClosed = %#v, want %v", binding.NativeEvent, entry["failClosed"], wantFailClosed)
			}
			generatedRoutes[binding.RuntimeEvent] = struct{}{}
		}
	}
	for _, route := range platformRuntimeEvents(platform) {
		if _, generated := generatedRoutes[route]; !generated {
			t.Fatalf("registered Cursor route %s has no generated binding", route)
		}
	}
}

func TestCursorEventDispositionsAreCompleteAndGeneratorExact(t *testing.T) {
	artifact, err := Generate(KindCursor)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Hooks map[string][]map[string]interface{} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(artifact.Content), &document); err != nil {
		t.Fatal(err)
	}
	dispositions := CursorEventDispositions()
	if len(dispositions) != 21 {
		t.Fatalf("Cursor event dispositions = %d, want 21", len(dispositions))
	}
	seen := map[string]bool{}
	for _, disposition := range dispositions {
		if disposition.NativeEvent == "" || seen[disposition.NativeEvent] {
			t.Fatalf("missing or duplicate Cursor event disposition: %#v", disposition)
		}
		seen[disposition.NativeEvent] = true
		_, generated := document.Hooks[disposition.NativeEvent]
		if generated != disposition.Install {
			t.Fatalf("%s generated=%v install=%v", disposition.NativeEvent, generated, disposition.Install)
		}
		if disposition.Evidence == "" || disposition.Support == "" || disposition.ErrorPolicy == "" || disposition.TimeoutPolicy == "" {
			t.Fatalf("%s has incomplete semantics: %#v", disposition.NativeEvent, disposition)
		}
		if disposition.Install && len(disposition.Surfaces) == 0 {
			t.Fatalf("%s has no documented surface classification", disposition.NativeEvent)
		}
		if !disposition.Install && disposition.Limitation == "" {
			t.Fatalf("%s has no explicit exclusion reason", disposition.NativeEvent)
		}
	}
	for event := range document.Hooks {
		if !seen[event] {
			t.Fatalf("generated unknown Cursor event %s", event)
		}
	}
}

func TestCursorDocumentedSurfacesStayEventSpecific(t *testing.T) {
	byEvent := map[string]CursorEventDisposition{}
	for _, disposition := range CursorEventDispositions() {
		byEvent[disposition.NativeEvent] = disposition
		for _, surface := range disposition.Surfaces {
			if surface == HostSurfaceCursorCLIInteractive || surface == HostSurfaceCursorCLIPrint {
				t.Fatalf("%s claims unproved Cursor CLI delivery on %s", disposition.NativeEvent, surface)
			}
		}
	}
	tests := []struct {
		event    string
		surfaces []HostSurface
	}{
		{
			event:    "afterTabFileEdit",
			surfaces: []HostSurface{HostSurfaceCursorTab},
		},
		{
			event: "sessionStart",
			surfaces: []HostSurface{
				HostSurfaceCursorDesktopAgent,
				HostSurfaceCursorDesktopCmdK,
			},
		},
		{
			event: "beforeMCPExecution",
			surfaces: []HostSurface{
				HostSurfaceCursorDesktopAgent,
				HostSurfaceCursorDesktopCmdK,
			},
		},
		{
			event: "postToolUse",
			surfaces: []HostSurface{
				HostSurfaceCursorDesktopAgent,
				HostSurfaceCursorDesktopCmdK,
				HostSurfaceCursorCloud,
			},
		},
	}
	for _, test := range tests {
		disposition, ok := byEvent[test.event]
		if !ok {
			t.Fatalf("Cursor disposition missing %s", test.event)
		}
		if !reflect.DeepEqual(disposition.Surfaces, test.surfaces) {
			t.Fatalf("%s surfaces = %v, want %v", test.event, disposition.Surfaces, test.surfaces)
		}
	}
}

func TestCursorBindingValidationRejectsIndependentDrift(t *testing.T) {
	base, ok := PlatformForKind(KindCursor)
	if !ok {
		t.Fatal("Cursor platform missing")
	}
	tests := []struct {
		name   string
		mutate func(*Platform)
	}{
		{name: "missing timeout", mutate: func(platform *Platform) { platform.Capabilities[0].TimeoutSeconds = 0 }},
		{name: "policy mismatch", mutate: func(platform *Platform) { platform.Capabilities[0].TimeoutPolicy = FailureBlock }},
		{name: "missing response", mutate: func(platform *Platform) { platform.Capabilities[0].Bindings[0].ResponseMode = "" }},
		{name: "invalid loop limit", mutate: func(platform *Platform) { platform.Capabilities[0].Bindings[0].LoopLimit = 1 }},
		{name: "duplicate runtime", mutate: func(platform *Platform) {
			platform.Capabilities[1].Bindings[0].RuntimeEvent = platform.Capabilities[0].Bindings[0].RuntimeEvent
		}},
		{name: "duplicate native", mutate: func(platform *Platform) {
			platform.Capabilities[1].Bindings[0].NativeEvent = platform.Capabilities[0].Bindings[0].NativeEvent
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			platform := clonePlatform(base)
			test.mutate(&platform)
			if err := validatePlatform(platform); err == nil {
				t.Fatal("drifted Cursor contract passed validation")
			}
		})
	}
	nonCursor, _ := PlatformForKind(KindCodex)
	nonCursor.Capabilities[0].Bindings[0].Matcher = "Write"
	if err := validatePlatform(nonCursor); err == nil {
		t.Fatal("Cursor-only binding field passed on Codex")
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
		})
	}
}

func TestGeneratorRegistryDriftReturnsTypedErrors(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "missing platform timeout",
			run: func() error {
				_, err := requiredTimeouts("missing-platform", EventStop)
				return err
			},
		},
		{
			name: "missing event timeout",
			run: func() error {
				_, err := requiredTimeouts(KindCodex, Event("missing-event"))
				return err
			},
		},
		{
			name: "missing plugin platform",
			run: func() error {
				_, err := bunRouteBudgets("missing-platform")
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var target *rerrors.PolicySourceError
			if err := test.run(); !errors.As(err, &target) {
				t.Fatalf("expected PolicySourceError, got %T", err)
			}
		})
	}
}

func TestNewPlatformArtifactsUseCurrentContracts(t *testing.T) {
	claude, err := Generate(KindClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{`"matcher": "compact"`, "claude-compaction-recovery", `"PostCompact"`, "claude-post-compaction", `"timeout": 5`, `"timeout": 10`, `"timeout": 30`} {
		if !strings.Contains(claude.Content, token) {
			t.Fatalf("Claude artifact missing %q:\n%s", token, claude.Content)
		}
	}

	devin, err := Generate(KindDevinCLI)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{`"UserPromptSubmit"`, "devin-user-prompt-submit", `"PostCompaction"`, "devin-post-compaction", `"timeout": 5`, `"matcher": "^(exec|edit)$"`} {
		if !strings.Contains(devin.Content, token) {
			t.Fatalf("Devin artifact missing %q:\n%s", token, devin.Content)
		}
	}

	kilo, err := Generate(KindKilo)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{`export default { id: "reconc", server: ReconcKiloServer }`, `"experimental.session.compacting"`, "kilo-pre-tool-use", `"timeoutMilliseconds":10000`, `readCombined(proc.stdout, proc.stderr, budget.maxOutputBytes, outputAbort.signal)`, `stream.getReader()`, `killSignal: "SIGKILL"`, `process.platform === "win32"`, `["sh", wrapper, event, repo]`, `client.session.promptAsync({`} {
		if !strings.Contains(kilo.Content, token) {
			t.Fatalf("Kilo artifact missing %q:\n%s", token, kilo.Content)
		}
	}
	if len(kilo.Content) > 28*1024 {
		t.Fatalf("Kilo adapter is not thin: %d bytes", len(kilo.Content))
	}

	antigravity, err := Generate(KindAntigravity)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(antigravity.Content, `"timeout": 120`) {
		t.Fatal("Antigravity retained the old 120-second blanket timeout")
	}

	grok, err := Generate(KindGrok)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{
		`"reconcManaged": true`,
		`"UserPromptSubmit"`,
		`"PermissionDenied"`,
		`"SubagentStart"`,
		`"PreCompact"`,
		`grok-pre-tool-use`,
		`{\"decision\":\"deny\"`,
		`"timeout": 10`,
		`run_terminal_command`,
		`run_terminal_cmd`,
		`hashline_edit`,
	} {
		if !strings.Contains(grok.Content, token) {
			t.Fatalf("Grok artifact missing %q:\n%s", token, grok.Content)
		}
	}
	if strings.Contains(grok.Content, "claude-") || strings.Contains(grok.Content, "cursor-") {
		t.Fatalf("Grok artifact is not first-class:\n%s", grok.Content)
	}
}

func TestBunAdapterRoutesAreRegistered(t *testing.T) {
	for kind, events := range map[string][]string{
		KindOpenCode: {
			"opencode-session-start", "opencode-user-prompt-submit", "opencode-pre-tool-use",
			"opencode-permission-request", "opencode-post-tool-use", "opencode-post-tool-use-failure",
			"opencode-pre-compaction", "opencode-post-compaction", "opencode-session-end", "opencode-stop",
			"opencode-continuation-accepted", "opencode-continuation-failed",
			"opencode-continuation-unavailable", "opencode-continuation-suppressed",
		},
		KindKilo: {
			"kilo-session-start", "kilo-user-prompt-submit", "kilo-pre-tool-use",
			"kilo-permission-request", "kilo-post-tool-use", "kilo-post-tool-use-failure",
			"kilo-pre-compaction", "kilo-post-compaction", "kilo-session-end", "kilo-stop",
			"kilo-continuation-accepted", "kilo-continuation-failed",
			"kilo-continuation-unavailable", "kilo-continuation-suppressed",
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
		"opencode-user-prompt-submit",
		"devin-user-prompt-submit",
		"kilo-user-prompt-submit",
	} {
		if _, ok := RuntimeEvent(event); !ok {
			t.Fatalf("native user-prompt route %s is not registered", event)
		}
	}
	for _, event := range []string{
		"antigravity-user-prompt-submit",
	} {
		if _, ok := RuntimeEvent(event); ok {
			t.Fatalf("unsupported user-prompt route %s is registered", event)
		}
	}
	for _, event := range []string{
		"grok-session-start",
		"grok-user-prompt-submit",
		"grok-pre-tool-use",
		"grok-post-tool-use",
		"grok-post-tool-use-failure",
		"grok-permission-denied",
		"grok-stop",
		"grok-stop-failure",
		"grok-notification",
		"grok-subagent-start",
		"grok-subagent-stop",
		"grok-pre-compaction",
		"grok-post-compaction",
		"grok-session-end",
	} {
		route, ok := RuntimeEvent(event)
		if !ok || route.PlatformKind != KindGrok {
			t.Fatalf("Grok route %s = %+v, %t", event, route, ok)
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

func TestGrokRuntimeEventsAndExactTargetMatching(t *testing.T) {
	events := GrokRuntimeEvents()
	if len(events) != 14 {
		t.Fatalf("Grok runtime routes = %d, want 14: %v", len(events), events)
	}
	if !GrokTargetHasRuntimeEvent("tools/reconc/bin/hook grok-stop .", "grok-stop") {
		t.Fatal("exact Grok route was not matched")
	}
	if GrokTargetHasRuntimeEvent("tools/reconc/bin/hook grok-stop-failure .", "grok-stop") {
		t.Fatal("grok-stop-failure must not satisfy grok-stop")
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
