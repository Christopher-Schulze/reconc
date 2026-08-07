package hooks

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
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

// hostMatcherFires applies the matcher rule Claude Code and Codex both
// implement: a matcher built only from letters, digits, `_`, `-`, spaces, `,`
// and `|` is compared as an exact string with pipe alternatives, and anything
// else is an unanchored regular expression. Sources: Claude Code's hooks
// reference, and codex-rs/hooks/src/events/common.rs.
func hostMatcherFires(t *testing.T, matcher, tool string) bool {
	t.Helper()
	exactOnly := true
	for _, character := range matcher {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '_', character == '-', character == ' ', character == ',', character == '|':
		default:
			exactOnly = false
		}
	}
	if exactOnly {
		for _, alternative := range strings.Split(matcher, "|") {
			if alternative == tool {
				return true
			}
		}
		return false
	}
	pattern, err := regexp.Compile(matcher)
	if err != nil {
		t.Fatalf("matcher %q is not a valid regular expression: %v", matcher, err)
	}
	return pattern.MatchString(tool)
}

// TestToolMatchersSelectTheIntendedRoutes proves the installed matchers behave
// the way the hosts evaluate them, instead of only pinning their text. A
// matcher like `mcp__` reads fine and would still never fire, because both
// hosts compare a matcher without regular-expression characters literally.
func TestToolMatchersSelectTheIntendedRoutes(t *testing.T) {
	tools := []struct {
		name string
		mcp  bool
		// guarded reports whether the tool must reach the write/shell gate.
		guarded map[string]bool
	}{
		{name: "Write", guarded: map[string]bool{KindClaudeCode: true, KindCodex: true}},
		{name: "Edit", guarded: map[string]bool{KindClaudeCode: true, KindCodex: true}},
		{name: "Bash", guarded: map[string]bool{KindClaudeCode: true, KindCodex: true}},
		{name: "NotebookEdit", guarded: map[string]bool{KindClaudeCode: true}},
		{name: "apply_patch", guarded: map[string]bool{KindCodex: true}},
		{name: "mcp__filesystem__write_file", mcp: true},
		{name: "mcp__memory__query", mcp: true},
		{name: "mcp__plugin_my-plugin_db__query", mcp: true},
	}
	for _, kind := range []string{KindClaudeCode, KindCodex} {
		t.Run(kind, func(t *testing.T) {
			artifact, err := Generate(kind)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			var document struct {
				Hooks map[string][]struct {
					Matcher string `json:"matcher"`
					Hooks   []struct {
						Command string   `json:"command"`
						Args    []string `json:"args"`
					} `json:"hooks"`
				} `json:"hooks"`
			}
			if err := json.Unmarshal([]byte(artifact.Content), &document); err != nil {
				t.Fatalf("generated hooks are not valid JSON: %v", err)
			}
			groups, ok := document.Hooks["PreToolUse"]
			if !ok || len(groups) < 2 {
				t.Fatalf("PreToolUse must carry a built-in group and an MCP group, got %d", len(groups))
			}
			for _, tool := range tools {
				routes := map[string]bool{}
				for _, group := range groups {
					if !hostMatcherFires(t, group.Matcher, tool.name) {
						continue
					}
					for _, entry := range group.Hooks {
						routes[routeIdentity(entry.Command, entry.Args)] = true
					}
				}
				mcpRoute := strings.TrimSuffix(kind, "-code") + "-mcp-before"
				if tool.mcp {
					if !routes[mcpRoute] {
						t.Errorf("%s must reach the MCP route, fired %v", tool.name, keysOf(routes))
					}
					if len(routes) != 1 {
						t.Errorf("%s must reach only the MCP route, fired %v", tool.name, keysOf(routes))
					}
					continue
				}
				if routes[mcpRoute] {
					t.Errorf("built-in tool %s must not reach the MCP route", tool.name)
				}
				if tool.guarded[kind] && len(routes) == 0 {
					t.Errorf("%s must reach the write/shell gate on %s", tool.name, kind)
				}
				if !tool.guarded[kind] && len(routes) != 0 {
					t.Errorf("%s is not a guarded tool on %s but fired %v", tool.name, kind, keysOf(routes))
				}
			}
		})
	}
}

// routeIdentity extracts the Reconc route name from a generated hook entry,
// which is either an exec-form argument list or a shell command line.
func routeIdentity(command string, args []string) string {
	for _, arg := range args {
		if strings.Contains(arg, "-") && !strings.Contains(arg, "/") && !strings.Contains(arg, "$") {
			return arg
		}
	}
	for _, field := range strings.Fields(command) {
		trimmed := strings.Trim(field, `"';`)
		if strings.HasPrefix(trimmed, "claude-") || strings.HasPrefix(trimmed, "codex-") {
			return trimmed
		}
	}
	return command
}

func keysOf(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
