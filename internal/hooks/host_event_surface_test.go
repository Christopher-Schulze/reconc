package hooks

import (
	"encoding/json"
	"testing"
)

// hostEventSurface is the event vocabulary a host accepts in its own hook
// configuration, transcribed from that host's published reference. Reconc must
// stay inside the surface (an invented name is a route the host silently drops)
// and must cover every entry in mustBind (a missing route is enforcement the
// user configured and does not get).
type hostEventSurface struct {
	kind     string
	accepted []string
	mustBind []string
}

// hostEventSurfaces pins the two hosts whose configuration takes an explicit,
// closed list of event keys. Plugin hosts negotiate their surface in
// TypeScript instead and are pinned by the adapter contract tests.
var hostEventSurfaces = []hostEventSurface{
	{
		// Codex: the eleven matcher groups in codex-rs/config/src/hook_config.rs.
		kind: KindCodex,
		accepted: []string{
			"PreToolUse", "PermissionRequest", "PostToolUse", "PreCompact", "PostCompact",
			"SessionStart", "SessionEnd", "UserPromptSubmit", "SubagentStart", "SubagentStop", "Stop",
		},
		mustBind: []string{
			"PreToolUse", "PermissionRequest", "PostToolUse", "PreCompact", "PostCompact",
			"SessionStart", "SessionEnd", "UserPromptSubmit", "SubagentStart", "SubagentStop", "Stop",
		},
	},
	{
		// Claude Code: the settings.json hook events. Reconc binds the subset its
		// own capability vocabulary covers; the rest stay unbound on purpose, so
		// only the accepted list carries them.
		kind: KindClaudeCode,
		accepted: []string{
			"SessionStart", "Setup", "UserPromptSubmit", "UserPromptExpansion", "PreToolUse",
			"PermissionRequest", "PermissionDenied", "PostToolUse", "PostToolUseFailure",
			"PostToolBatch", "Notification", "MessageDisplay", "SubagentStart", "SubagentStop",
			"TaskCreated", "TaskCompleted", "Stop", "StopFailure", "TeammateIdle",
			"InstructionsLoaded", "ConfigChange", "CwdChanged", "DirectoryAdded", "FileChanged",
			"WorktreeCreate", "WorktreeRemove", "PreCompact", "PostCompact", "Elicitation",
			"ElicitationResult", "SessionEnd",
		},
		mustBind: []string{
			"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest",
			"PermissionDenied", "PostToolUse", "PostToolUseFailure", "Notification",
			"SubagentStart", "SubagentStop", "Stop", "StopFailure", "PreCompact",
			"PostCompact", "SessionEnd",
		},
	},
}

// TestGeneratedHooksStayInsideHostEventSurface fails both ways: a route the host
// does not accept, and a documented event the registry or the generated artifact
// drops.
func TestGeneratedHooksStayInsideHostEventSurface(t *testing.T) {
	for _, surface := range hostEventSurfaces {
		t.Run(surface.kind, func(t *testing.T) {
			accepted := map[string]bool{}
			for _, event := range surface.accepted {
				accepted[event] = true
			}
			for _, event := range surface.mustBind {
				if !accepted[event] {
					t.Fatalf("test data is inconsistent: %q must be bound but is not an accepted event", event)
				}
			}

			platform, ok := PlatformForKind(surface.kind)
			if !ok {
				t.Fatalf("%s platform is not registered", surface.kind)
			}
			bound := map[string]bool{}
			for _, capability := range platform.Capabilities {
				if capability.Support == SupportUnsupported {
					continue
				}
				for _, binding := range capability.Bindings {
					if !accepted[binding.NativeEvent] {
						t.Errorf("registry binds %q, which %s does not accept in its hook configuration", binding.NativeEvent, surface.kind)
					}
					bound[binding.NativeEvent] = true
				}
			}
			for _, event := range surface.mustBind {
				if !bound[event] {
					t.Errorf("%s accepts %q but the registry does not bind it", surface.kind, event)
				}
			}

			artifact, err := Generate(surface.kind)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			var document struct {
				Hooks map[string]json.RawMessage `json:"hooks"`
			}
			if err := json.Unmarshal([]byte(artifact.Content), &document); err != nil {
				t.Fatalf("generated %s hooks are not valid JSON: %v", surface.kind, err)
			}
			for _, event := range surface.mustBind {
				if _, present := document.Hooks[event]; !present {
					t.Errorf("generated %s hooks omit the event %q", surface.kind, event)
				}
			}
			for event := range document.Hooks {
				if !accepted[event] {
					t.Errorf("generated %s hooks declare %q, which the host configuration does not accept", surface.kind, event)
				}
			}
		})
	}
}
