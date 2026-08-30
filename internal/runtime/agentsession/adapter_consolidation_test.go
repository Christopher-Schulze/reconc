package agentsession

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestSingleJSONDecoderPreservesTypesAndDiagnostics(t *testing.T) {
	diagnostics := singleJSONDiagnostics{
		decodePrefix: "decode fixture", multipleValues: "multiple fixture values", trailingPrefix: "fixture trailing data",
	}
	var decoded map[string]interface{}
	if err := decodeSingleJSONValue([]byte(`{"count":9007199254740993}`), &decoded, true, diagnostics); err != nil {
		t.Fatal(err)
	}
	if decoded["count"] != json.Number("9007199254740993") {
		t.Fatalf("number type/value = %#v", decoded["count"])
	}
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "decode", body: `{`, want: "decode fixture:"},
		{name: "multiple", body: `{} {}`, want: "multiple fixture values"},
		{name: "trailing", body: `{} !`, want: "fixture trailing data:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := decodeSingleJSONValue([]byte(test.body), &decoded, false, diagnostics); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestNativeEventRegistriesPreserveContracts(t *testing.T) {
	registries := map[string]struct {
		registry nativeEventRegistry
		want     map[string]string
	}{
		"Pi": {registry: piNativeEvents, want: map[string]string{
			"pi-session-start": "session_start", "pi-user-prompt-submit": "input", "pi-pre-tool-use": "tool_call",
			"pi-user-bash": "user_bash", "pi-post-tool-use": "tool_result", "pi-post-tool-use-failure": "tool_result",
			"pi-stop": "agent_settled", "pi-continuation-requested": "agent_settled", "pi-continuation-failed": "agent_settled",
			"pi-continuation-suppressed": "agent_settled", "pi-session-end": "session_shutdown",
			"pi-pre-compaction": "session_before_compact", "pi-post-compaction": "session_compact",
		}},
		"OMP": {registry: ompNativeEvents, want: map[string]string{
			"omp-session-start": "session_start", "omp-user-prompt-submit": "input", "omp-pre-tool-use": "tool_call",
			"omp-user-bash": "user_bash", "omp-user-python": "user_python", "omp-permission-request": "tool_approval_requested",
			"omp-permission-result": "tool_approval_resolved", "omp-post-tool-use": "tool_result",
			"omp-post-tool-use-failure": "tool_result", "omp-stop": "session_stop", "omp-session-end": "session_shutdown",
			"omp-pre-compaction": "session_before_compact", "omp-post-compaction": "session_compact",
		}},
		"ZCode": {registry: zcodeNativeEvents, want: map[string]string{
			"zcode-session-start": "SessionStart", "zcode-user-prompt-submit": "UserPromptSubmit",
			"zcode-pre-tool-use": "PreToolUse", "zcode-permission-request": "PermissionRequest",
			"zcode-post-tool-use": "PostToolUse", "zcode-post-tool-use-failure": "PostToolUseFailure", "zcode-stop": "Stop",
		}},
		"Devin": {registry: devinNativeEvents, want: map[string]string{
			"devin-session-start": "SessionStart", "devin-user-prompt-submit": "UserPromptSubmit",
			"devin-pre-tool-use": "PreToolUse", "devin-permission-request": "PermissionRequest",
			"devin-post-tool-use": "PostToolUse", "devin-stop": "Stop", "devin-session-end": "SessionEnd",
			"devin-post-compaction": "PostCompaction",
		}},
		"Kimi Code": {registry: kimiCodeNativeEvents, want: map[string]string{
			"kimi-session-start": "SessionStart", "kimi-user-prompt-submit": "UserPromptSubmit",
			"kimi-pre-tool-use": "PreToolUse", "kimi-permission-request": "PermissionRequest",
			"kimi-permission-result": "PermissionResult", "kimi-post-tool-use": "PostToolUse",
			"kimi-post-tool-use-failure": "PostToolUseFailure", "kimi-stop": "Stop",
			"kimi-stop-failure": "StopFailure", "kimi-interrupt": "Interrupt", "kimi-session-end": "SessionEnd",
			"kimi-subagent-start": "SubagentStart", "kimi-subagent-stop": "SubagentStop",
			"kimi-pre-compaction": "PreCompact", "kimi-post-compaction": "PostCompact", "kimi-notification": "Notification",
		}},
		"GitHub Copilot": {registry: gitHubCopilotNativeEvents, want: map[string]string{
			"copilot-session-start": "SessionStart", "copilot-user-prompt-submit": "UserPromptSubmit",
			"copilot-pre-tool-use": "PreToolUse", "copilot-permission-request": "PermissionRequest",
			"copilot-post-tool-use": "PostToolUse", "copilot-post-tool-use-failure": "PostToolUseFailure",
			"copilot-stop": "Stop", "copilot-session-end": "SessionEnd", "copilot-notification": "Notification",
			"copilot-subagent-start": "subagentStart", "copilot-subagent-stop": "SubagentStop",
			"copilot-pre-compaction": "PreCompact",
		}},
		"Grok": {registry: grokNativeEvents, want: map[string]string{
			"grok-session-start": "session_start", "grok-user-prompt-submit": "user_prompt_submit",
			"grok-pre-tool-use": "pre_tool_use", "grok-post-tool-use": "post_tool_use",
			"grok-post-tool-use-failure": "post_tool_use_failure", "grok-permission-denied": "permission_denied",
			"grok-stop": "stop", "grok-stop-failure": "stop_failure", "grok-notification": "notification",
			"grok-subagent-start": "subagent_start", "grok-subagent-stop": "subagent_stop",
			"grok-pre-compaction": "pre_compact", "grok-post-compaction": "post_compact", "grok-session-end": "session_end",
		}},
	}
	for name, test := range registries {
		t.Run(name, func(t *testing.T) {
			if entries := test.registry.entries(); len(entries) != len(test.want) {
				t.Fatalf("registry has %d entries, want %d", len(entries), len(test.want))
			}
			for route, native := range test.want {
				binding, ok := test.registry.lookup(route)
				if !ok || binding.primary != native {
					t.Fatalf("lookup(%q) = %+v, %t", route, binding, ok)
				}
			}
			entries := test.registry.entries()
			original := entries[0]
			entries[0].primary = "forged"
			binding, ok := test.registry.lookup(original.route)
			if !ok || binding.primary != original.primary {
				t.Fatalf("caller mutation changed registry: %+v", binding)
			}
		})
	}
	if binding, ok := gitHubCopilotNativeEvents.lookup("copilot-subagent-start"); !ok || binding.primary != "subagentStart" || binding.alternate != "SubagentStart" || !binding.allowMissing {
		t.Fatalf("Copilot subagent compatibility contract = %+v, %t", binding, ok)
	}
}

func TestNativeMCPEnvelopePreservesPlatformParity(t *testing.T) {
	for _, test := range []struct {
		name         string
		platform     policy.MCPPlatform
		event        string
		successEvent string
		failureEvent string
		outcome      string
	}{
		{name: "Pi pre", platform: policy.MCPPlatformPi, event: "pi-pre-tool-use", successEvent: "pi-post-tool-use", failureEvent: "pi-post-tool-use-failure"},
		{name: "Pi failure", platform: policy.MCPPlatformPi, event: "pi-post-tool-use-failure", successEvent: "pi-post-tool-use", failureEvent: "pi-post-tool-use-failure", outcome: "failure"},
		{name: "OMP success", platform: policy.MCPPlatformOMP, event: "omp-post-tool-use", successEvent: "omp-post-tool-use", failureEvent: "omp-post-tool-use-failure", outcome: "success"},
		{name: "ZCode success", platform: policy.MCPPlatformZCode, event: "zcode-post-tool-use", successEvent: "zcode-post-tool-use", failureEvent: "zcode-post-tool-use-failure", outcome: "success"},
	} {
		t.Run(test.name, func(t *testing.T) {
			envelope := newNativeMCPEnvelope(test.platform, " tool ", json.RawMessage(`{"path":"README.md"}`), test.event, test.successEvent, test.failureEvent)
			want := map[string]interface{}{
				"platform": string(test.platform), "tool": "tool", "observed": false,
				"phase": string(MCPPhaseBefore), "blocking_pre_hook": true, "input_valid": true,
			}
			if test.outcome != "" {
				want["phase"] = string(MCPPhaseAfter)
				want["outcome"] = test.outcome
			}
			if got := mcpEnvelopeToMap(*envelope); !reflect.DeepEqual(got, want) {
				t.Fatalf("MCP envelope = %#v, want %#v", got, want)
			}
		})
	}
}

var benchmarkEventValid bool

func BenchmarkGitHubCopilotEventValidation(b *testing.B) {
	raw := map[string]interface{}{"hook_event_name": "PreToolUse"}
	b.Run("registry", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkEventValid = validateGitHubCopilotEvent("copilot-pre-tool-use", raw) == nil
		}
	})
	b.Run("per-event-map", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkEventValid = legacyGitHubCopilotEventValid("copilot-pre-tool-use", "PreToolUse")
		}
	})
}

func legacyGitHubCopilotEventValid(route, native string) bool {
	expected := map[string][]string{
		"copilot-session-start":         {"SessionStart"},
		"copilot-user-prompt-submit":    {"UserPromptSubmit"},
		"copilot-pre-tool-use":          {"PreToolUse"},
		"copilot-permission-request":    {"PermissionRequest"},
		"copilot-post-tool-use":         {"PostToolUse"},
		"copilot-post-tool-use-failure": {"PostToolUseFailure"},
		"copilot-stop":                  {"Stop"},
		"copilot-session-end":           {"SessionEnd"},
		"copilot-notification":          {"Notification"},
		"copilot-subagent-start":        {"subagentStart", "SubagentStart"},
		"copilot-subagent-stop":         {"SubagentStop"},
		"copilot-pre-compaction":        {"PreCompact"},
	}[route]
	for _, candidate := range expected {
		if native == candidate {
			return true
		}
	}
	return false
}
